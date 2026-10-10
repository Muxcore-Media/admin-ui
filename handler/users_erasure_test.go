package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"google.golang.org/grpc/codes"

	authv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/auth/v1"

	"github.com/Muxcore-Media/admin-ui/session"
)

func withTestSession(r *http.Request, bearer string) *http.Request {
	s := &session.Session{UserID: "admin-1", Username: "root", Roles: []string{"admin"}, AuthLocalToken: bearer}
	return r.WithContext(context.WithValue(r.Context(), ctxSessionKey, s))
}

func deleteUserRequest(userID string) *http.Request {
	r := httptest.NewRequest(http.MethodDelete, "/users/"+userID, nil)
	r.SetPathValue("id", userID)
	return r
}

type auditCapture struct {
	mu      sync.Mutex
	actions []string
	details []map[string]string
}

func (a *auditCapture) hook(_, action, _, _ string, details map[string]string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.actions = append(a.actions, action)
	a.details = append(a.details, details)
}

func TestUsersDeleteSendsCurrentAdminBearer(t *testing.T) {
	stub := &identityRPCStub{deleteErasureID: "er-opaque-1"}
	h := setupIdentityHandler(t, stub)
	var audit auditCapture
	h.AuditHook = audit.hook
	triggered := 0
	h.ErasureTrigger = func() { triggered++ }

	w := httptest.NewRecorder()
	h.UsersDelete(w, withTestSession(deleteUserRequest("u-victim"), "admin-bearer-xyz"))

	if w.Header().Get("HX-Redirect") != "/users" {
		t.Fatalf("delete did not succeed: %d %s", w.Code, w.Body.String())
	}
	if got := stub.tokens["DeleteUser"]; len(got) != 1 || got[0] != "admin-bearer-xyz" {
		t.Fatalf("DeleteUser x-auth-token = %v, want the signed-in administrator's bearer", got)
	}
	if got := stub.requests["DeleteUser"].(*authv1.DeleteUserRequest).GetUserId(); got != "u-victim" {
		t.Errorf("DeleteUser user id = %q", got)
	}
	if triggered != 1 {
		t.Errorf("erasure reconciler triggered %d times, want 1", triggered)
	}
	if len(audit.details) != 1 || audit.details[0]["erasure_id"] != "er-opaque-1" {
		t.Errorf("audit details = %v, want the erasure id", audit.details)
	}
}

func TestUsersDeleteWithoutBearerNeverCallsDeleteUser(t *testing.T) {
	cases := map[string]*http.Request{
		"no session":        deleteUserRequest("u-victim"),
		"empty bearer":      withTestSession(deleteUserRequest("u-victim"), ""),
		"whitespace bearer": withTestSession(deleteUserRequest("u-victim"), "  \t"),
	}
	for name, r := range cases {
		t.Run(name, func(t *testing.T) {
			stub := &identityRPCStub{}
			h := setupIdentityHandler(t, stub)
			triggered := false
			h.ErasureTrigger = func() { triggered = true }

			w := httptest.NewRecorder()
			h.UsersDelete(w, r)

			if n := stub.calls["DeleteUser"]; n != 0 {
				t.Fatalf("DeleteUser reached the provider %d time(s) without an administrator bearer", n)
			}
			if w.Header().Get("HX-Redirect") != "" {
				t.Fatal("refused delete reported success")
			}
			if !strings.Contains(w.Body.String(), "sign-in") {
				t.Errorf("no explanation: %s", w.Body.String())
			}
			if triggered {
				t.Error("refused delete triggered an erasure sweep")
			}
		})
	}
}

func TestUsersDeleteProviderRefusalIsNotSuccess(t *testing.T) {
	for _, code := range []codes.Code{codes.PermissionDenied, codes.Unauthenticated, codes.FailedPrecondition} {
		t.Run(code.String(), func(t *testing.T) {
			stub := &identityRPCStub{fail: map[string]codes.Code{"DeleteUser": code}}
			h := setupIdentityHandler(t, stub)
			triggered := false
			h.ErasureTrigger = func() { triggered = true }
			w := httptest.NewRecorder()
			h.UsersDelete(w, withTestSession(deleteUserRequest("u-victim"), "admin-bearer"))
			if w.Header().Get("HX-Redirect") != "" || triggered {
				t.Fatalf("refusal reported as success (redirect=%q triggered=%v)", w.Header().Get("HX-Redirect"), triggered)
			}
			if !strings.Contains(w.Body.String(), "delete failed") {
				t.Errorf("body = %s", w.Body.String())
			}
		})
	}
}

func TestUsersPageShowsErasurePendingAndCompleteFromProviderStatus(t *testing.T) {
	stub := &identityRPCStub{erasureStatus: []*authv1.ErasureStatus{
		{ErasureId: "er-old", DeletedAt: "2026-10-01T09:00:00Z", Complete: true, Modules: []*authv1.ErasureModuleStatus{
			{ModuleId: "userdata-local", Outcome: authv1.ErasureOutcome_ERASURE_OUTCOME_OK, AckedAt: "2026-10-01T09:01:00Z", Required: true},
		}},
		{ErasureId: "er-open", DeletedAt: "2026-10-09T09:00:00Z", Modules: []*authv1.ErasureModuleStatus{
			{ModuleId: "userdata-local", Outcome: authv1.ErasureOutcome_ERASURE_OUTCOME_OK, AckedAt: "2026-10-09T09:01:00Z", Required: true},
			{ModuleId: "request-media", Required: true},
			{ModuleId: "admin-ui", Outcome: authv1.ErasureOutcome_ERASURE_OUTCOME_FAILED, DetailCode: "sessions_persist_failed", AckedAt: "2026-10-09T09:02:00Z", Required: true},
		}},
	}}
	h := setupIdentityHandler(t, stub)

	w := httptest.NewRecorder()
	h.UsersPage(w, withTestSession(httptest.NewRequest(http.MethodGet, "/users", nil), "admin-bearer-xyz"))
	body := w.Body.String()

	// Incomplete first, then the completed one.
	open, done := strings.Index(body, `data-erasure-id="er-open"`), strings.Index(body, `data-erasure-id="er-old"`)
	if open < 0 || done < 0 || open > done {
		t.Fatalf("erasure rows missing or out of order (open=%d done=%d)", open, done)
	}
	for _, want := range []string{
		`data-erasure-id="er-open" data-complete="false"`,
		`data-erasure-id="er-old" data-complete="true"`,
		`data-module="request-media" data-state="pending"`,
		`data-module="admin-ui" data-state="failed"`,
		`(sessions_persist_failed)`,
		`2026-10-09T09:02:00Z`,
		`Not yet acknowledged`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("users page missing %q", want)
		}
	}

	if stub.calls["GetUserErasureStatus"] == 0 {
		t.Fatal("status was not read from GetUserErasureStatus")
	}
	if got := stub.tokens["GetUserErasureStatus"]; len(got) != 1 || got[0] != "admin-bearer-xyz" {
		t.Errorf("GetUserErasureStatus x-auth-token = %v, want the administrator bearer", got)
	}
	// admin-ui is not on the ledger consumer allowlist and must not try to be.
	for _, rpc := range []string{"ListUserErasures", "AckUserErasure"} {
		if stub.calls[rpc] != 0 {
			t.Errorf("/users called %s; the page must use GetUserErasureStatus only", rpc)
		}
	}
}

func TestUsersPageErasureStatusDegradesWithoutHidingUsers(t *testing.T) {
	cases := map[codes.Code]string{
		codes.Unimplemented:    "does not report account erasure status",
		codes.PermissionDenied: "do not have permission",
		codes.Unavailable:      "could not be loaded",
		codes.Unauthenticated:  "Sign in again",
	}
	for code, want := range cases {
		t.Run(code.String(), func(t *testing.T) {
			stub := &identityRPCStub{fail: map[string]codes.Code{"GetUserErasureStatus": code}}
			h := setupIdentityHandler(t, stub)
			w := httptest.NewRecorder()
			h.UsersPage(w, withTestSession(httptest.NewRequest(http.MethodGet, "/users", nil), "b"))
			body := w.Body.String()
			if !strings.Contains(body, `data-testid="erasure-unavailable"`) || !strings.Contains(body, want) {
				t.Fatalf("expected an unavailable panel containing %q, got: %s", want, truncate(body, 600))
			}
			if !strings.Contains(body, "alice") {
				t.Error("user list hidden by an erasure status failure")
			}
			if strings.Contains(body, `data-testid="erasure-row"`) {
				t.Error("rows rendered without a provider answer")
			}
		})
	}
}

func TestBuildErasureStatusOrdersIncompleteFirstAndCapsComplete(t *testing.T) {
	var in []*authv1.ErasureStatus
	for _, id := range []string{"c1", "c2", "c3", "c4", "c5", "c6", "c7"} {
		in = append(in, &authv1.ErasureStatus{ErasureId: id, Complete: true})
	}
	in = append(in, &authv1.ErasureStatus{ErasureId: "open-1"}, &authv1.ErasureStatus{ErasureId: "open-2"})
	got := buildErasureStatus(in)

	var ids []string
	for _, r := range got.Rows {
		ids = append(ids, r.ErasureID)
	}
	want := []string{"open-1", "open-2", "c7", "c6", "c5", "c4", "c3"}
	if strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Fatalf("rows = %v, want %v", ids, want)
	}
	if !got.Truncated {
		t.Error("capped history not flagged")
	}
}
