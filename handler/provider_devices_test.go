package handler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/Muxcore-Media/admin-ui/session"
	authv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/auth/v1"
	"golang.org/x/net/html"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
)

const deviceTestBearer = "current-provider-bearer-secret"

func providerDevicesRequest(method, path string, form url.Values, bearer string) *http.Request {
	r := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("X-Auth-Token", "browser-forged-token")
	r.Header.Set("Authorization", "Bearer browser-forged-token")
	r.AddCookie(&http.Cookie{Name: "session", Value: "browser-cookie-secret"})
	ctx := context.WithValue(r.Context(), ctxSessionKey, &session.Session{UserID: "admin", Username: "Administrator", AuthLocalToken: bearer})
	ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs("x-auth-token", "stale-one", "x-auth-token", "stale-two"))
	return r.WithContext(ctx)
}

func assertDeviceCaller(t *testing.T, stub *identityRPCStub, method string) {
	t.Helper()
	if got := stub.tokens[method]; !reflect.DeepEqual(got, []string{deviceTestBearer}) {
		t.Fatalf("%s caller metadata=%v, want exactly one current bearer", method, got)
	}
}

func deviceElements(t *testing.T, body, tag string) []*html.Node {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	var found []*html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == tag {
			found = append(found, n)
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	return found
}

func deviceAttr(n *html.Node, name string) string {
	for _, a := range n.Attr {
		if a.Key == name {
			return a.Val
		}
	}
	return ""
}

func TestProviderDevicesListAndPagination(t *testing.T) {
	cursor := "opaque + /?&= cursor"
	filter := "user /?&+"
	stub := &identityRPCStub{sessionRows: []*authv1.SessionInfo{
		{SessionId: "opaque-session-1", UserId: filter, Username: "Alice <Admin>", Kind: "full", CreatedAt: "2026-10-07T01:00:00Z", ExpiresAt: "2026-10-08T01:00:00Z"},
		{SessionId: "opaque-session-2", UserId: "other-user", Username: "Bob", Kind: "api-token"},
	}, nextSessionPage: cursor}
	h := setupIdentityHandler(t, stub)
	localCookie, _ := h.Sessions.Create("local-user", "Local-only username", nil, nil)
	for _, userID := range []string{"", filter} {
		t.Run(fmt.Sprintf("filter-%q", userID), func(t *testing.T) {
			path := "/devices?" + url.Values{"user_id": {userID}, "page_token": {"previous + /?&="}}.Encode()
			w := httptest.NewRecorder()
			h.DevicesPage(w, providerDevicesRequest("GET", path, nil, deviceTestBearer))
			if w.Code != 200 {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			req := stub.requests["ListSessions"].(*authv1.ListSessionsRequest)
			if req.GetUserId() != userID || req.GetPageToken() != "previous + /?&=" || req.GetPageSize() != 100 {
				t.Fatalf("request=%+v", req)
			}
			assertDeviceCaller(t, stub, "ListSessions")
			body := w.Body.String()
			for _, expected := range []string{"Alice &lt;Admin&gt;", "Bob", "Signed-in session", "API-token session", "does not revoke the API key", "2026-10-08T01:00:00Z"} {
				if !strings.Contains(body, expected) {
					t.Errorf("missing %q", expected)
				}
			}
			if strings.Index(body, "opaque-session-1") > strings.Index(body, "opaque-session-2") {
				t.Error("provider ordering changed")
			}
			for _, secret := range []string{deviceTestBearer, "browser-cookie-secret", localCookie, session.ID(localCookie), "Local-only username", "browser-forged-token", "stale-one"} {
				if strings.Contains(body, secret) {
					t.Errorf("provider page exposed local/auth value %q", secret)
				}
			}
			var next string
			for _, link := range deviceElements(t, body, "a") {
				if deviceAttr(link, "rel") == "next" {
					next = deviceAttr(link, "href")
				}
			}
			u, err := url.Parse(next)
			if err != nil || u.Path != "/devices" || u.Query().Get("page_token") != cursor || u.Query().Get("user_id") != userID {
				t.Fatalf("next link=%q err=%v", next, err)
			}
			for _, form := range deviceElements(t, body, "form") {
				if deviceAttr(form, "method") != "GET" {
					continue
				}
				var inspect func(*html.Node)
				inspect = func(n *html.Node) {
					if n.Data == "input" && deviceAttr(n, "name") == "page_token" {
						t.Error("filter form preserves obsolete cursor")
					}
					for c := n.FirstChild; c != nil; c = c.NextSibling {
						inspect(c)
					}
				}
				inspect(form)
			}
		})
	}
	if stub.calls["ListUsers"] != 0 {
		t.Fatal("session list should not depend on user CRUD capability")
	}
}

func TestProviderDevicesEmptyPage(t *testing.T) {
	stub := &identityRPCStub{}
	h := setupIdentityHandler(t, stub)
	w := httptest.NewRecorder()
	h.DevicesPage(w, providerDevicesRequest("GET", "/devices?user_id=unknown-user", nil, deviceTestBearer))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `data-testid="provider-sessions-empty"`) || strings.Contains(w.Body.String(), `rel="next"`) {
		t.Fatalf("unexpected empty page: status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestProviderDevicesErrors(t *testing.T) {
	for _, method := range []string{"ListSessions", "RevokeSession"} {
		for _, code := range []codes.Code{codes.Unimplemented, codes.Unauthenticated, codes.PermissionDenied, codes.InvalidArgument, codes.Unavailable, codes.Internal, codes.DeadlineExceeded} {
			t.Run(method+"/"+code.String(), func(t *testing.T) {
				stub := &identityRPCStub{fail: map[string]codes.Code{method: code}}
				h := setupIdentityHandler(t, stub)
				audits := 0
				h.AuditHook = func(_, _, _, _ string, _ map[string]string) { audits++ }
				w := httptest.NewRecorder()
				if method == "ListSessions" {
					h.DevicesPage(w, providerDevicesRequest("GET", "/devices?page_token=invalid", nil, deviceTestBearer))
				} else {
					h.DevicesProviderRevoke(w, providerDevicesRequest("POST", "/devices/provider/revoke", url.Values{"user_id": {"u1"}, "session_id": {"s1"}}, deviceTestBearer))
				}
				want := map[codes.Code]int{codes.Unimplemented: 200, codes.Unauthenticated: 401, codes.PermissionDenied: 403, codes.InvalidArgument: 400}[code]
				if want == 0 {
					want = 502
				}
				if w.Code != want {
					t.Fatalf("status=%d want=%d", w.Code, want)
				}
				body := w.Body.String()
				unsupported := strings.Contains(body, `data-testid="identity-unsupported"`)
				if unsupported != (code == codes.Unimplemented) {
					t.Errorf("unsupported state=%t for %s", unsupported, code)
				}
				if code != codes.Unimplemented && !strings.Contains(body, `data-testid="provider-sessions-error"`) {
					t.Error("missing error state")
				}
				if strings.Contains(body, `action="/devices/provider/revoke"`) || strings.Contains(body, "provider-sessions-empty") || w.Header().Get("Location") != "" || audits != 0 {
					t.Errorf("failure exposed controls, empty state, or success: location=%q audit=%d", w.Header().Get("Location"), audits)
				}
				if !strings.Contains(body, "/devices?scope=local") {
					t.Error("local session view inaccessible")
				}
				assertDeviceCaller(t, stub, method)
			})
		}
	}
}

func TestProviderDevicesDiscoveryFailure(t *testing.T) {
	for _, method := range []string{"list", "revoke"} {
		t.Run(method, func(t *testing.T) {
			stub := &identityRPCStub{discoveryFail: codes.Unimplemented}
			h := setupIdentityHandler(t, stub)
			w := httptest.NewRecorder()
			if method == "list" {
				h.DevicesPage(w, providerDevicesRequest("GET", "/devices", nil, deviceTestBearer))
			} else {
				h.DevicesProviderRevoke(w, providerDevicesRequest("POST", "/devices/provider/revoke", url.Values{"user_id": {"u1"}, "session_id": {"s1"}}, deviceTestBearer))
			}
			if w.Code != 503 || strings.Contains(w.Body.String(), "identity-unsupported") || stub.calls["ListSessions"]+stub.calls["RevokeSession"] != 0 {
				t.Fatalf("discovery failure: status=%d calls=%v", w.Code, stub.calls)
			}
		})
	}
}

func TestProviderDevicesMissingBearer(t *testing.T) {
	for _, method := range []string{"list", "revoke"} {
		for _, bearer := range []string{"", "   ", "no-session"} {
			t.Run(method+"/"+bearer, func(t *testing.T) {
				stub := &identityRPCStub{}
				h := setupIdentityHandler(t, stub)
				w := httptest.NewRecorder()
				r := providerDevicesRequest("POST", "/devices/provider/revoke", url.Values{"user_id": {"u1"}, "session_id": {"s1"}}, bearer)
				if method == "list" {
					r = providerDevicesRequest("GET", "/devices", nil, bearer)
				}
				if bearer == "no-session" {
					r = r.WithContext(context.Background())
				}
				if method == "list" {
					h.DevicesPage(w, r)
				} else {
					h.DevicesProviderRevoke(w, r)
				}
				if w.Code != 401 || len(stub.calls) != 0 {
					t.Fatalf("missing bearer: status=%d calls=%v", w.Code, stub.calls)
				}
			})
		}
	}
}

func TestProviderDevicesRevokeExactPair(t *testing.T) {
	stub := &identityRPCStub{}
	h := setupIdentityHandler(t, stub)
	localCookie, _ := h.Sessions.Create("local-user", "Local", nil, nil)
	var audit []string
	h.AuditHook = func(actor, action, resource, id string, details map[string]string) {
		audit = append(audit, actor, action, resource, id, details["user_id"])
	}
	targetUser, targetSession := "target /?&+", "opaque /?&+=<> session"
	for i := 0; i < 2; i++ { // Provider success is idempotent, including an unknown pair.
		w := httptest.NewRecorder()
		form := url.Values{"user_id": {targetUser}, "session_id": {targetSession}, "filter_user_id": {"different-filter"}, "page_token": {"obsolete-cursor"}}
		h.DevicesProviderRevoke(w, providerDevicesRequest("POST", "/devices/provider/revoke?user_id=wrong-query-user", form, deviceTestBearer))
		if w.Code != 303 {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
		req := stub.requests["RevokeSession"].(*authv1.RevokeSessionRequest)
		if req.GetUserId() != targetUser || req.GetSessionId() != targetSession {
			t.Fatalf("wrong revoke pair: %+v", req)
		}
		assertDeviceCaller(t, stub, "RevokeSession")
		location, err := url.Parse(w.Header().Get("Location"))
		if err != nil || location.Path != "/devices" || location.Query().Get("user_id") != "different-filter" || location.Query().Get("page_token") != "" || location.Query().Get("revoked") != "1" {
			t.Fatalf("redirect=%v err=%v", location, err)
		}
		if _, ok := h.Sessions.Get(localCookie); !ok {
			t.Fatal("provider revoke removed local cookie session")
		}
	}
	if len(audit) != 10 || audit[0] != "admin" || audit[1] != "admin.session.revoke" || audit[3] != targetSession || audit[4] != targetUser {
		t.Fatalf("audit=%v", audit)
	}
	for _, secret := range []string{localCookie, deviceTestBearer, "browser-cookie-secret", "obsolete-cursor"} {
		if strings.Contains(strings.Join(audit, " "), secret) {
			t.Error("audit disclosed credential or cursor")
		}
	}
	// Even equal strings belong to different namespaces; a provider action
	// must never interpret an opaque ID as a local cookie hash.
	w := httptest.NewRecorder()
	h.DevicesProviderRevoke(w, providerDevicesRequest("POST", "/devices/provider/revoke", url.Values{"user_id": {"local-user"}, "session_id": {session.ID(localCookie)}}, deviceTestBearer))
	if _, ok := h.Sessions.Get(localCookie); !ok || w.Code != 303 {
		t.Fatal("provider management ID was confused with a local cookie hash")
	}
}

func TestProviderDevicesRevokeMissingPair(t *testing.T) {
	for _, form := range []url.Values{{}, {"user_id": {"u"}}, {"session_id": {"s"}}, {"user_id": {" "}, "session_id": {"s"}}} {
		t.Run(form.Encode(), func(t *testing.T) {
			stub := &identityRPCStub{}
			h := setupIdentityHandler(t, stub)
			w := httptest.NewRecorder()
			h.DevicesProviderRevoke(w, providerDevicesRequest("POST", "/devices/provider/revoke?user_id=query&session_id=query", form, deviceTestBearer))
			if w.Code != 400 || len(stub.calls) != 0 || w.Header().Get("Location") != "" {
				t.Fatalf("missing pair: status=%d calls=%v", w.Code, stub.calls)
			}
		})
	}
}

func TestProviderDevicesLocalIDsStayLocal(t *testing.T) {
	stub := &identityRPCStub{}
	h := setupIdentityHandler(t, stub)
	localCookie, _ := h.Sessions.Create("local-user", "Local", nil, nil)
	id := session.ID(localCookie)
	r := providerDevicesRequest("POST", "/devices/"+id+"/rename", url.Values{"label": {"Desk"}}, "")
	r.SetPathValue("token", id)
	w := httptest.NewRecorder()
	h.DevicesRename(w, r)
	if sess, ok := h.Sessions.Get(localCookie); !ok || sess.Label != "Desk" {
		t.Fatal("local rename failed")
	}
	if w.Header().Get("Location") != "/devices?scope=local" {
		t.Fatal("rename did not return to local view")
	}
	w = httptest.NewRecorder()
	h.DevicesPage(w, providerDevicesRequest("GET", "/devices?scope=local", nil, ""))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Local admin-panel sessions") || !strings.Contains(w.Body.String(), "Desk") || strings.Contains(w.Body.String(), `action="/devices/provider/revoke"`) {
		t.Fatal("local view mixed with provider sessions")
	}
	r = providerDevicesRequest("POST", "/devices/"+id+"/revoke", nil, "")
	r.SetPathValue("token", id)
	h.DevicesRevoke(httptest.NewRecorder(), r)
	if _, ok := h.Sessions.Get(localCookie); ok || len(stub.calls) != 0 {
		t.Fatalf("local revoke failed or called provider: %v", stub.calls)
	}
}

func TestProviderDevicesRouteAuthorization(t *testing.T) {
	for _, method := range []string{"GET", "POST"} {
		for _, role := range []string{"anonymous", "denied", "admin"} {
			t.Run(method+"/"+role, func(t *testing.T) {
				stub := &identityRPCStub{
					allowAdmin: role == "admin",
					validateResp: &authv1.ValidateResponse{
						Valid: true, UserId: "admin", Username: "Admin", Roles: []string{"admin"},
					},
				}
				h := setupIdentityHandler(t, stub)
				mux := http.NewServeMux()
				h.RegisterRoutes(mux)
				path, rpc := "/devices", "ListSessions"
				if method == "POST" {
					path, rpc = "/devices/provider/revoke", "RevokeSession"
				}
				r := httptest.NewRequest(method, path, strings.NewReader(url.Values{"user_id": {"target"}, "session_id": {"opaque-id"}}.Encode()))
				r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				r.Header.Set("X-Auth-Token", "forged-browser-token")
				if role != "anonymous" {
					cookie, _ := h.Sessions.Create("admin", "Admin", nil, nil)
					h.Sessions.BindAuthLocalToken(cookie, deviceTestBearer)
					r.AddCookie(&http.Cookie{Name: "session", Value: cookie})
				}
				w := httptest.NewRecorder()
				mux.ServeHTTP(w, r)
				if role == "admin" {
					if stub.calls[rpc] != 1 {
						t.Fatalf("authorized request did not reach %s: status=%d", rpc, w.Code)
					}
					assertDeviceCaller(t, stub, rpc)
				} else if stub.calls[rpc] != 0 {
					t.Fatal("unauthorized caller reached session RPC")
				}
			})
		}
	}
}
