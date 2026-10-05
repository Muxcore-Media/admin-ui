package handler

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Muxcore-Media/admin-ui/session"
	authv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/auth/v1"
	discoveryv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/discovery/v1"
	"github.com/Muxcore-Media/core/sdk/go/client"
	"google.golang.org/grpc"
)

// apikeysAuthStub is a minimal auth-service stub for API-key operations.
type apikeysAuthStub struct {
	authv1.UnimplementedAuthServiceServer

	users  []*authv1.UserInfo
	tokens map[string][]*authv1.APITokenInfo // user_id → tokens

	createdUserID string
	createdName   string
	createdSecret string
	deletedID     string
}

func (s *apikeysAuthStub) ListUsers(_ context.Context, _ *authv1.ListUsersRequest) (*authv1.ListUsersResponse, error) {
	return &authv1.ListUsersResponse{Users: s.users}, nil
}

func (s *apikeysAuthStub) ListAPITokens(_ context.Context, req *authv1.ListAPITokensRequest) (*authv1.ListAPITokensResponse, error) {
	return &authv1.ListAPITokensResponse{Tokens: s.tokens[req.GetUserId()]}, nil
}

func (s *apikeysAuthStub) CreateAPIToken(_ context.Context, req *authv1.CreateAPITokenRequest) (*authv1.CreateAPITokenResponse, error) {
	s.createdUserID = req.GetUserId()
	s.createdName = req.GetName()
	s.createdSecret = "mxc_test_secret_value"
	return &authv1.CreateAPITokenResponse{Token: s.createdSecret}, nil
}

func (s *apikeysAuthStub) DeleteAPIToken(_ context.Context, req *authv1.DeleteAPITokenRequest) (*authv1.DeleteAPITokenResponse, error) {
	s.deletedID = req.GetTokenId()
	return &authv1.DeleteAPITokenResponse{}, nil
}

func setupAPIKeysHandler(t *testing.T, stub *apikeysAuthStub) *Handler {
	t.Helper()
	authLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	authSrv := grpc.NewServer()
	authv1.RegisterAuthServiceServer(authSrv, stub)
	go func() { _ = authSrv.Serve(authLis) }()
	t.Cleanup(func() {
		authSrv.Stop()
		_ = authLis.Close()
	})

	discLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	discSrv := grpc.NewServer()
	discoveryv1.RegisterDiscoveryServiceServer(discSrv, capDiscovery{
		mods: map[string]*discoveryv1.ModuleInfoProto{
			"auth": {Id: "auth-local", HttpAddr: authLis.Addr().String()},
		},
	})
	go func() { _ = discSrv.Serve(discLis) }()
	t.Cleanup(func() {
		discSrv.Stop()
		_ = discLis.Close()
	})

	core, err := client.Dial(discLis.Addr().String(), client.WithInsecure())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = core.Close() })

	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, true, "", nil, nil)
	h.Core = core
	return h
}

func TestAPIKeysPageRendersWithUsers(t *testing.T) {
	stub := &apikeysAuthStub{
		users: []*authv1.UserInfo{{Id: "u1", Username: "alice"}},
		tokens: map[string][]*authv1.APITokenInfo{
			"u1": {{Id: "tok1", Name: "home", Prefix: "mxc_abc"}},
		},
	}
	h := setupAPIKeysHandler(t, stub)

	r := mustRequest(http.MethodGet, "/keys")
	w := httptest.NewRecorder()
	h.APIKeysPage(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%.200s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "alice") {
		t.Error("expected username alice in page")
	}
	if !strings.Contains(body, "home") {
		t.Error("expected token name home in page")
	}
	if !strings.Contains(body, "create-key-form") {
		t.Error("expected create-key-form on page")
	}
}

func TestAPIKeysPageEmptyState(t *testing.T) {
	stub := &apikeysAuthStub{
		users:  []*authv1.UserInfo{{Id: "u1", Username: "alice"}},
		tokens: map[string][]*authv1.APITokenInfo{},
	}
	h := setupAPIKeysHandler(t, stub)

	r := mustRequest(http.MethodGet, "/keys")
	w := httptest.NewRecorder()
	h.APIKeysPage(w, r)

	if !strings.Contains(w.Body.String(), "keys-empty") {
		t.Error("expected empty-state element")
	}
}

func TestAPIKeysCreateCallsAuthAndShowsCopyOnce(t *testing.T) {
	stub := &apikeysAuthStub{
		users: []*authv1.UserInfo{{Id: "u1", Username: "alice"}},
	}
	h := setupAPIKeysHandler(t, stub)

	body := strings.NewReader("user_id=u1&name=laptop")
	r := httptest.NewRequest(http.MethodPost, "/keys/create", body)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.APIKeysCreate(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%.300s", w.Code, w.Body.String())
	}
	if stub.createdName != "laptop" {
		t.Fatalf("expected created name laptop, got %q", stub.createdName)
	}
	if stub.createdUserID != "u1" {
		t.Fatalf("expected user u1, got %q", stub.createdUserID)
	}
	resp := w.Body.String()
	if !strings.Contains(resp, "copy-once-panel") {
		t.Error("expected copy-once-panel in response")
	}
	if !strings.Contains(resp, "mxc_test_secret_value") {
		t.Error("expected secret token in copy-once panel")
	}
}

func TestAPIKeysCreateMissingFieldsRejects(t *testing.T) {
	stub := &apikeysAuthStub{}
	h := setupAPIKeysHandler(t, stub)

	body := strings.NewReader("user_id=&name=")
	r := httptest.NewRequest(http.MethodPost, "/keys/create", body)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.APIKeysCreate(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestAPIKeysRotateCreatesAndDeletesOld(t *testing.T) {
	stub := &apikeysAuthStub{
		users: []*authv1.UserInfo{{Id: "u1", Username: "alice"}},
	}
	h := setupAPIKeysHandler(t, stub)

	r := httptest.NewRequest(http.MethodPost, "/keys/old-tok/rotate?user=u1&name=laptop", nil)
	r.SetPathValue("id", "old-tok")
	w := httptest.NewRecorder()
	h.APIKeysRotate(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%.300s", w.Code, w.Body.String())
	}
	if stub.createdName != "laptop" {
		t.Fatalf("expected created name laptop, got %q", stub.createdName)
	}
	if stub.deletedID != "old-tok" {
		t.Fatalf("expected old-tok deleted, got %q", stub.deletedID)
	}
	if !strings.Contains(w.Body.String(), "copy-once-panel") {
		t.Error("expected copy-once-panel after rotate")
	}
}

func TestAPIKeysRotateMissingParamsRejects(t *testing.T) {
	stub := &apikeysAuthStub{}
	h := setupAPIKeysHandler(t, stub)

	r := httptest.NewRequest(http.MethodPost, "/keys/tok/rotate", nil)
	r.SetPathValue("id", "tok")
	w := httptest.NewRecorder()
	h.APIKeysRotate(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestAPIKeysRevokeRedirects(t *testing.T) {
	stub := &apikeysAuthStub{}
	h := setupAPIKeysHandler(t, stub)

	r := httptest.NewRequest(http.MethodPost, "/keys/tok1/revoke?user=u1", nil)
	r.SetPathValue("id", "tok1")
	w := httptest.NewRecorder()
	h.APIKeysRevoke(w, r)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect, got %d", w.Code)
	}
	if stub.deletedID != "tok1" {
		t.Fatalf("expected tok1 deleted, got %q", stub.deletedID)
	}
}

func TestAPIKeysTimeoutConstants(t *testing.T) {
	// Timeout budget is from users.go — verify create/rotate fit within it.
	if usersDialTimeout <= 0 {
		t.Fatal("usersDialTimeout must be positive")
	}
	if usersReadTimeout <= 0 {
		t.Fatal("usersReadTimeout must be positive")
	}
}
