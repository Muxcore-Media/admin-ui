package handler

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Muxcore-Media/admin-ui/session"
	authv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/auth/v1"
	discoveryv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/discovery/v1"
	"github.com/Muxcore-Media/core/sdk/go/client"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type identityRPCStub struct {
	authv1.UnimplementedAuthServiceServer
	fail            map[string]codes.Code
	bodyError       map[string]string
	totpEnabled     bool
	discoveryFail   codes.Code
	allowAdmin      bool
	sessionRows     []*authv1.SessionInfo
	nextSessionPage string
	mu              sync.Mutex
	calls           map[string]int
	requests        map[string]proto.Message
	tokens          map[string][]string
}

func (s *identityRPCStub) intercept(ctx context.Context, req any, info *grpc.UnaryServerInfo, next grpc.UnaryHandler) (any, error) {
	method := info.FullMethod[strings.LastIndex(info.FullMethod, "/")+1:]
	s.mu.Lock()
	s.calls[method]++
	s.requests[method] = proto.Clone(req.(proto.Message))
	md, _ := metadata.FromIncomingContext(ctx)
	s.tokens[method] = append([]string(nil), md.Get("x-auth-token")...)
	s.mu.Unlock()
	if code := s.fail[method]; code != codes.OK {
		return nil, status.Error(code, "provider response")
	}
	switch method {
	case "ListUsers":
		return &authv1.ListUsersResponse{Users: []*authv1.UserInfo{{Id: "u1", Username: "alice"}}}, nil
	case "CreateUser":
		return &authv1.CreateUserResponse{UserId: "u2"}, nil
	case "DeleteUser":
		return &authv1.DeleteUserResponse{}, nil
	case "SetPassword":
		return &authv1.SetPasswordResponse{}, nil
	case "SetRoles":
		return &authv1.SetRolesResponse{}, nil
	case "TOTPStatus":
		return &authv1.TOTPStatusResponse{Enabled: s.totpEnabled}, nil
	case "EnableTOTP":
		return &authv1.EnableTOTPResponse{Secret: "test-secret", QrCodeUrl: "otpauth://totp/test"}, nil
	case "DisableTOTP":
		return &authv1.DisableTOTPResponse{}, nil
	case "ListAPITokens":
		return &authv1.ListAPITokensResponse{Tokens: []*authv1.APITokenInfo{{Id: "old", Name: "laptop", Prefix: "prefix"}}}, nil
	case "CreateAPIToken":
		return &authv1.CreateAPITokenResponse{Token: "one-time-new-token", Error: s.bodyError[method]}, nil
	case "DeleteAPIToken":
		return &authv1.DeleteAPITokenResponse{Error: s.bodyError[method]}, nil
	case "ListInvites":
		return &authv1.ListInvitesResponse{Invites: []*authv1.InviteInfo{
			{Id: "invite-1", Prefix: "active", Role: "user", MaxUses: 2},
			{Id: "invite-2", Prefix: "revoked", Role: "viewer", RevokedAt: "2026-10-07T00:00:00Z"},
		}}, nil
	case "CreateInvite":
		return &authv1.CreateInviteResponse{Token: "invite-secret", Error: s.bodyError[method]}, nil
	case "RevokeInvite":
		return &authv1.RevokeInviteResponse{Error: s.bodyError[method]}, nil
	case "Can":
		return &authv1.CanResponse{Allowed: s.allowAdmin}, nil
	case "ListSessions":
		return &authv1.ListSessionsResponse{Sessions: s.sessionRows, NextPageToken: s.nextSessionPage}, nil
	case "RevokeSession":
		return &authv1.RevokeSessionResponse{}, nil
	}
	return next(ctx, req)
}

func setupIdentityHandler(t *testing.T, stub *identityRPCStub) *Handler {
	t.Helper()
	stub.calls = map[string]int{}
	stub.requests = map[string]proto.Message{}
	stub.tokens = map[string][]string{}
	authLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	authSrv := grpc.NewServer(grpc.UnaryInterceptor(stub.intercept))
	authv1.RegisterAuthServiceServer(authSrv, stub)
	go func() { _ = authSrv.Serve(authLis) }()
	t.Cleanup(func() { authSrv.Stop(); _ = authLis.Close() })
	discLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	discSrv := grpc.NewServer(grpc.UnaryInterceptor(func(ctx context.Context, req any, info *grpc.UnaryServerInfo, next grpc.UnaryHandler) (any, error) {
		if stub.discoveryFail != codes.OK {
			return nil, status.Error(stub.discoveryFail, "discovery unavailable")
		}
		return next(ctx, req)
	}))
	provider := &discoveryv1.ModuleInfoProto{Id: "custom-identity-provider", HttpAddr: authLis.Addr().String()}
	discoveryv1.RegisterDiscoveryServiceServer(discSrv, capDiscovery{mods: map[string]*discoveryv1.ModuleInfoProto{"auth": provider, "authorizer": provider}})
	go func() { _ = discSrv.Serve(discLis) }()
	t.Cleanup(func() { discSrv.Stop(); _ = discLis.Close() })
	core, err := client.Dial(discLis.Addr().String(), client.WithInsecure())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = core.Close() })
	h := New(nil, session.NewStore(time.Hour), false, "test", nil, true, "", nil, nil)
	h.Core = core
	return h
}

type identityHandlerCase struct {
	name, method, path, form, rpc string
	handle                        func(*Handler, http.ResponseWriter, *http.Request)
}

var identityCases = []identityHandlerCase{
	{"users", "GET", "/users", "", "ListUsers", (*Handler).UsersPage},
	{"create-user-form", "GET", "/users/create-form", "", "ListUsers", (*Handler).UsersCreateForm},
	{"user-detail", "GET", "/users/u1/detail", "", "ListUsers", (*Handler).UsersDetail},
	{"create-user", "POST", "/users", "username=bob&password=secret", "CreateUser", (*Handler).UsersCreate},
	{"delete-user", "DELETE", "/users/u1", "", "DeleteUser", (*Handler).UsersDelete},
	{"password", "POST", "/users/u1/password", "password=secret", "SetPassword", (*Handler).UsersSetPassword},
	{"roles", "POST", "/users/u1/roles", "roles=viewer", "SetRoles", (*Handler).UsersSetRoles},
	{"totp-status", "GET", "/users/u1/totp", "", "TOTPStatus", (*Handler).UsersTOTPStatus},
	{"totp-toggle-status", "POST", "/users/u1/totp", "", "TOTPStatus", (*Handler).UsersTOTP},
	{"totp-enable", "POST", "/users/u1/totp", "", "EnableTOTP", (*Handler).UsersTOTP},
	{"totp-disable", "POST", "/users/u1/totp", "", "DisableTOTP", (*Handler).UsersTOTP},
	{"user-tokens", "GET", "/users/u1/tokens", "", "ListAPITokens", (*Handler).UsersTokens},
	{"create-user-token", "POST", "/users/u1/tokens", "name=laptop", "CreateAPIToken", (*Handler).UsersTokens},
	{"delete-user-token", "DELETE", "/users/u1/tokens/old", "", "DeleteAPIToken", (*Handler).UsersTokens},
	{"keys-users", "GET", "/keys", "", "ListUsers", (*Handler).APIKeysPage},
	{"keys-tokens", "GET", "/keys", "", "ListAPITokens", (*Handler).APIKeysPage},
	{"create-key", "POST", "/keys/create", "user_id=u1&name=laptop", "CreateAPIToken", (*Handler).APIKeysCreate},
	{"rotate-key", "POST", "/keys/old/rotate?user=u1&name=laptop", "", "CreateAPIToken", (*Handler).APIKeysRotate},
	{"revoke-key", "POST", "/keys/old/revoke?user=u1", "", "DeleteAPIToken", (*Handler).APIKeysRevoke},
	{"invites", "GET", "/invites", "", "ListInvites", (*Handler).InvitesPage},
	{"create-invite", "POST", "/invites", "role=user&max_uses=1&ttl_hours=168", "CreateInvite", (*Handler).InvitesCreate},
	{"revoke-invite", "POST", "/invites/invite-1/revoke", "", "RevokeInvite", (*Handler).InvitesRevoke},
}

func identityCase(t *testing.T, name string) identityHandlerCase {
	t.Helper()
	for _, tc := range identityCases {
		if tc.name == name {
			return tc
		}
	}
	t.Fatalf("missing handler fixture %s", name)
	return identityHandlerCase{}
}

func identityRequest(tc identityHandlerCase) *http.Request {
	r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.form))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.SetPathValue("id", "u1")
	r.SetPathValue("tokenId", "old")
	return r
}

func TestIdentityUnsupportedOperationsSuppressControls(t *testing.T) {
	for _, tc := range identityCases {
		t.Run(tc.name, func(t *testing.T) {
			stub := &identityRPCStub{fail: map[string]codes.Code{tc.rpc: codes.Unimplemented}, totpEnabled: tc.rpc == "DisableTOTP"}
			h := setupIdentityHandler(t, stub)
			w := httptest.NewRecorder()
			tc.handle(h, w, identityRequest(tc))
			body := w.Body.String()
			if w.Code != http.StatusOK || !strings.Contains(body, `data-testid="identity-unsupported"`) {
				t.Fatalf("expected explanatory unsupported state, status=%d body=%s", w.Code, body)
			}
			for _, control := range []string{"<form", "hx-post=", "hx-delete=", "Toggle TOTP", "No users found", "No API tokens yet"} {
				if strings.Contains(body, control) {
					t.Errorf("unsupported operation exposes %q", control)
				}
			}
			if w.Header().Get("Location") != "" || w.Header().Get("HX-Redirect") != "" {
				t.Fatal("unsupported operation reported success via redirect")
			}
			if stub.calls[tc.rpc] != 1 {
				t.Fatalf("expected actual %s capability result", tc.rpc)
			}
			if tc.name == "rotate-key" && stub.calls["DeleteAPIToken"] != 0 {
				t.Fatal("failed creation must not delete old token")
			}
		})
	}
}

func TestIdentityErrorsAreNotUnsupportedOrSuccessful(t *testing.T) {
	for _, code := range []codes.Code{codes.Unavailable, codes.PermissionDenied, codes.Unauthenticated} {
		for _, tc := range identityCases {
			t.Run(code.String()+"/"+tc.name, func(t *testing.T) {
				stub := &identityRPCStub{fail: map[string]codes.Code{tc.rpc: code}, totpEnabled: tc.rpc == "DisableTOTP"}
				h := setupIdentityHandler(t, stub)
				w := httptest.NewRecorder()
				tc.handle(h, w, identityRequest(tc))
				body := w.Body.String()
				if strings.Contains(body, `data-testid="identity-unsupported"`) {
					t.Fatal("backend failure mislabeled as unsupported")
				}
				if w.Header().Get("Location") != "" || w.Header().Get("HX-Redirect") != "" {
					t.Fatal("backend failure reported success")
				}
				if !strings.Contains(strings.ToLower(body), "failed") && !strings.Contains(strings.ToLower(body), "could not") && !strings.Contains(strings.ToLower(body), "create token:") && !strings.Contains(body, "permission") && !strings.Contains(body, "identity session") {
					t.Fatalf("failure has no explanation: %s", body)
				}
				if strings.Contains(body, "No users found") || strings.Contains(body, "No API tokens yet") {
					t.Fatal("provider error looks like empty data")
				}
				if tc.name == "users" || tc.name == "keys-users" || tc.name == "keys-tokens" || tc.name == "invites" {
					if code == codes.PermissionDenied && !strings.Contains(body, "permission") {
						t.Fatal("permission failure described as provider outage")
					}
					if code == codes.Unauthenticated && !strings.Contains(body, "Sign in again") {
						t.Fatal("expired identity session has no sign-in guidance")
					}
				}
				if tc.name == "create-key" || tc.name == "rotate-key" || tc.name == "revoke-key" || tc.name == "create-invite" || tc.name == "revoke-invite" {
					if w.Code != identityErrorStatus(status.Error(code, "test")) {
						t.Errorf("wrong error status: %d", w.Code)
					}
				}
			})
		}
	}
}

func TestIdentitySupportedPanelsKeepControls(t *testing.T) {
	cases := []struct {
		tc   identityHandlerCase
		want string
	}{
		{identityCase(t, "users"), "Add User"},
		{identityCase(t, "create-user-form"), "user-create-username"},
		{identityCase(t, "user-detail"), "data-totp-panel"},
		{identityCase(t, "totp-status"), "Toggle TOTP"},
		{identityCase(t, "user-tokens"), "Token name"},
		{identityCase(t, "keys-tokens"), "create-key-form"},
		{identityCase(t, "invites"), "invites-create-form"},
	}
	for _, tc := range cases {
		t.Run(tc.tc.name, func(t *testing.T) {
			h := setupIdentityHandler(t, &identityRPCStub{})
			w := httptest.NewRecorder()
			tc.tc.handle(h, w, identityRequest(tc.tc))
			if !strings.Contains(w.Body.String(), tc.want) {
				t.Fatalf("supported panel missing %q: %s", tc.want, w.Body.String())
			}
			if strings.Contains(w.Body.String(), `data-testid="identity-unsupported"`) {
				t.Fatal("supported provider hidden")
			}
			if tc.tc.name == "user-detail" && strings.Contains(w.Body.String(), "Toggle TOTP") {
				t.Fatal("TOTP mutation exposed before status probe")
			}
		})
	}
}

func TestInviteRPCPreservesFormSemanticsAndSessionIdentity(t *testing.T) {
	cases := []struct {
		name, form, tenant string
		uses               int32
		ttl                int64
		wantTenant         string
	}{
		{"single", "role=viewer&max_uses=1&ttl_hours=168", "household", 1, 604800, "household"},
		{"unlimited-zero", "role=user&max_uses=0&ttl_hours=0", "", -1, 0, ""},
		{"unlimited-negative", "role=user&max_uses=-1&ttl_hours=-1", "household", -1, 0, "household"},
		{"multiple-explicit-tenant", "role=manager&max_uses=5&ttl_hours=24&tenant_id=+override+", "household", 5, 86400, "override"},
		{"missing-values", "role=user", "", -1, 0, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := &identityRPCStub{}
			h := setupIdentityHandler(t, stub)
			r := httptest.NewRequest(http.MethodPost, "/invites", strings.NewReader(tc.form))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			r = r.WithContext(context.WithValue(r.Context(), ctxSessionKey, &session.Session{UserID: "u1", TenantID: tc.tenant, AuthLocalToken: "session-token"}))
			w := httptest.NewRecorder()
			h.InvitesCreate(w, r)
			if w.Code != http.StatusSeeOther {
				t.Fatalf("create failed: %d %s", w.Code, w.Body.String())
			}
			got := stub.requests["CreateInvite"].(*authv1.CreateInviteRequest)
			if got.GetMaxUses() != tc.uses || got.GetTtlSeconds() != tc.ttl || got.GetTenantId() != tc.wantTenant {
				t.Fatalf("wrong invite request: %+v", got)
			}
			location, err := url.Parse(w.Header().Get("Location"))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(location.Query().Get("created"), "/invite/redeem?token=invite-secret") {
				t.Fatal("created invite link missing token")
			}
			list := httptest.NewRecorder()
			h.InvitesPage(list, httptest.NewRequest(http.MethodGet, "/invites", nil).WithContext(r.Context()))
			if !strings.Contains(list.Body.String(), "Revoke invite active") || strings.Contains(list.Body.String(), "Revoke invite revoked") {
				t.Fatal("invite list does not preserve revoked state")
			}
			r.SetPathValue("id", "invite-1")
			revoke := httptest.NewRecorder()
			h.InvitesRevoke(revoke, r)
			if revoke.Code != http.StatusSeeOther {
				t.Fatalf("revoke failed: %d", revoke.Code)
			}
			if stub.requests["RevokeInvite"].(*authv1.RevokeInviteRequest).GetInviteId() != "invite-1" {
				t.Fatal("revoke changed the invite identifier")
			}
			for _, method := range []string{"ListInvites", "CreateInvite", "RevokeInvite"} {
				if got := stub.tokens[method]; len(got) != 1 || got[0] != "session-token" {
					t.Errorf("%s lost signed-in session: %v", method, got)
				}
			}
		})
	}
}

func TestInviteInvalidNumbersDoNotReachProvider(t *testing.T) {
	for _, form := range []string{"max_uses=2147483648", "max_uses=-2147483649", "max_uses=oops", "ttl_hours=9223372036854775807", "ttl_hours=oops"} {
		t.Run(form, func(t *testing.T) {
			stub := &identityRPCStub{}
			h := setupIdentityHandler(t, stub)
			r := httptest.NewRequest(http.MethodPost, "/invites", strings.NewReader(form))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			w := httptest.NewRecorder()
			h.InvitesCreate(w, r)
			if w.Code != http.StatusBadRequest || stub.calls["CreateInvite"] != 0 {
				t.Fatalf("invalid form reached provider: status=%d", w.Code)
			}
		})
	}
}

func TestIdentityResponseErrorsDoNotReportSuccess(t *testing.T) {
	for _, name := range []string{"delete-user-token", "revoke-key", "create-invite", "revoke-invite"} {
		tc := identityCase(t, name)
		t.Run(tc.name, func(t *testing.T) {
			stub := &identityRPCStub{bodyError: map[string]string{tc.rpc: "operation denied"}}
			h := setupIdentityHandler(t, stub)
			w := httptest.NewRecorder()
			tc.handle(h, w, identityRequest(tc))
			if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "operation denied") || w.Header().Get("Location") != "" || w.Header().Get("HX-Redirect") != "" {
				t.Fatalf("response error reported success: %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestAPIKeyRotationKeepsNewSecretWhenRevocationFails(t *testing.T) {
	for _, tc := range []struct {
		name               string
		code               codes.Code
		bodyError, message string
	}{
		{"unsupported", codes.Unimplemented, "", "old token remains active"},
		{"unavailable", codes.Unavailable, "", "revocation of the old token was not confirmed"},
		{"response-error", codes.OK, "revocation denied", "revocation of the old token was not confirmed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stub := &identityRPCStub{fail: map[string]codes.Code{"DeleteAPIToken": tc.code}, bodyError: map[string]string{"DeleteAPIToken": tc.bodyError}}
			h := setupIdentityHandler(t, stub)
			w := httptest.NewRecorder()
			h.APIKeysRotate(w, identityRequest(identityCase(t, "rotate-key")))
			body := w.Body.String()
			for _, want := range []string{"rotation-incomplete", "copy-once-panel", "one-time-new-token", tc.message} {
				if !strings.Contains(body, want) {
					t.Errorf("partial rotation lost %q: %s", want, body)
				}
			}
			if w.Code != http.StatusOK || strings.Contains(body, "API Key Rotated") || w.Header().Get("Location") != "" {
				t.Fatal("partial rotation reported full success or hid new token")
			}
			if stub.calls["CreateAPIToken"] != 1 || stub.calls["DeleteAPIToken"] != 1 {
				t.Fatal("rotation retried or skipped a mutation")
			}
		})
	}
}

func TestIdentityUnsupportedHandlingDoesNotBypassAuthorization(t *testing.T) {
	stub := &identityRPCStub{fail: map[string]codes.Code{"ListInvites": codes.Unimplemented}}
	h := setupIdentityHandler(t, stub)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	for _, path := range []string{"/users", "/keys", "/invites"} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/login" {
			t.Fatalf("%s bypassed login", path)
		}
	}
	token, err := h.Sessions.CreateWithTenant("u1", "viewer", "", []string{"viewer"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/invites", nil)
	r.AddCookie(&http.Cookie{Name: "session", Value: token})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if !strings.Contains(w.Body.String(), "Error 403") || stub.calls["Can"] != 1 || stub.calls["ListInvites"] != 0 {
		t.Fatal("denied user reached capability handler")
	}
}

func TestIdentityDiscoveryFailureIsNotACapabilityResponse(t *testing.T) {
	stub := &identityRPCStub{discoveryFail: codes.Unimplemented}
	h := setupIdentityHandler(t, stub)
	w := httptest.NewRecorder()
	h.APIKeysPage(w, httptest.NewRequest(http.MethodGet, "/keys", nil))
	if strings.Contains(w.Body.String(), `data-testid="identity-unsupported"`) || !strings.Contains(w.Body.String(), `data-testid="keys-error"`) {
		t.Fatalf("discovery failure mislabeled as an identity capability: %s", w.Body.String())
	}
	if len(stub.calls) != 0 {
		t.Fatal("discovery failure reached identity provider")
	}
}
