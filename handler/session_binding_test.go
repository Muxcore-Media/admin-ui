package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	authv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/auth/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func currentSessionStub() *identityRPCStub {
	return &identityRPCStub{
		allowAdmin: true,
		validateResp: &authv1.ValidateResponse{
			Valid: true, UserId: "u1", Username: "alice", TenantId: "tenant", Roles: []string{"admin"},
		},
	}
}

func TestSessionChecksReplaceInheritedCredentials(t *testing.T) {
	for _, bound := range []bool{true, false} {
		t.Run(map[bool]string{true: "bound", false: "local-only"}[bound], func(t *testing.T) {
			stub := currentSessionStub()
			h := setupIdentityHandler(t, stub)
			bearer := "current-provider-bearer"
			if !bound {
				bearer = ""
			}
			cookie := boundSession(t, h, "u1", "alice", "tenant", bearer, []string{"admin"})
			r := sessionRequest("/protected", cookie)
			md := metadata.Pairs("x-auth-token", "stale-one", "x-auth-token", "stale-two", "authorization", "Bearer stale-one", "authorization", "Bearer stale-two")
			r = r.WithContext(metadata.NewOutgoingContext(r.Context(), md))
			ran := false
			w := httptest.NewRecorder()
			h.requireAuth(func(w http.ResponseWriter, r *http.Request) {
				ran = true
				if _, bounded := r.Context().Deadline(); bounded {
					t.Error("check deadline leaked into protected handler")
				}
				w.WriteHeader(http.StatusNoContent)
			})(w, r)
			if !ran || w.Code != http.StatusNoContent {
				t.Fatalf("valid session blocked: ran=%v status=%d", ran, w.Code)
			}
			wantOrder := []string{"Can"}
			if bound {
				wantOrder = []string{"Validate", "Can"}
				if stub.requests["Validate"].(*authv1.ValidateRequest).GetToken() != bearer {
					t.Fatal("Validate used a different bearer")
				}
			}
			if !reflect.DeepEqual(stub.order, wantOrder) {
				t.Fatalf("checks=%v, want %v", stub.order, wantOrder)
			}
			for _, rpc := range wantOrder {
				if bound {
					if !reflect.DeepEqual(stub.tokens[rpc], []string{bearer}) || !reflect.DeepEqual(stub.authorization[rpc], []string{"Bearer " + bearer}) {
						t.Fatalf("%s did not replace both credential headers with exactly the stored bearer", rpc)
					}
				} else if len(stub.tokens[rpc]) != 0 || len(stub.authorization[rpc]) != 0 {
					t.Fatalf("%s inherited credentials for a local-only session", rpc)
				}
			}
			can := stub.requests["Can"].(*authv1.CanRequest)
			if can.GetUserId() != "u1" || can.GetAction() != "admin.access" || can.GetResource() != "admin.ui" {
				t.Fatal("Can used the wrong bound identity or permission")
			}
			if !reflect.DeepEqual(md.Get("x-auth-token"), []string{"stale-one", "stale-two"}) || !reflect.DeepEqual(md.Get("authorization"), []string{"Bearer stale-one", "Bearer stale-two"}) {
				t.Fatal("check mutated parent metadata")
			}
		})
	}
}

func TestSessionBindingChangesDuringChecks(t *testing.T) {
	for _, rpc := range []string{"Validate", "Can", "local-only-Can"} {
		for _, change := range []string{"revoke", "rebind"} {
			for _, outcome := range []string{"allowed", "invalid", "unauthenticated", "denied", "unavailable"} {
				if outcome == "invalid" && rpc != "Validate" {
					continue
				}
				t.Run(rpc+"/"+change+"/"+outcome, func(t *testing.T) {
					stub := currentSessionStub()
					h := setupIdentityHandler(t, stub)
					bearer, method := "old-bearer", rpc
					if rpc == "local-only-Can" {
						bearer, method = "", "Can"
					}
					cookie := boundSession(t, h, "u1", "alice", "tenant", bearer, []string{"admin"})
					switch outcome {
					case "invalid":
						stub.validateResp = &authv1.ValidateResponse{Valid: false}
					case "unauthenticated":
						stub.fail = map[string]codes.Code{method: codes.Unauthenticated}
					case "denied":
						stub.fail = map[string]codes.Code{method: codes.PermissionDenied}
					case "unavailable":
						stub.fail = map[string]codes.Code{method: codes.Unavailable}
					}
					stub.onCall = func(_ context.Context, called string) {
						if called == method {
							if change == "revoke" {
								h.Sessions.Revoke(cookie)
							} else {
								h.Sessions.BindAuthLocalToken(cookie, "replacement-bearer")
							}
						}
					}
					w := httptest.NewRecorder()
					guard, ran := revalidateGuard(h)
					guard(w, sessionRequest("/protected", cookie))
					if *ran {
						t.Fatal("protected handler ran after its local binding changed")
					}
					if rpc == "Validate" && stub.calls["Can"] != 0 {
						t.Fatal("changed validation binding reached authorization")
					}
					stored, exists := h.Sessions.Get(cookie)
					if change == "revoke" {
						if exists || w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/login" {
							t.Fatalf("revoked binding: exists=%v status=%d", exists, w.Code)
						}
						assertSessionCleared(t, w)
					} else {
						if !exists || stored.AuthLocalToken != "replacement-bearer" || w.Code != http.StatusServiceUnavailable {
							t.Fatalf("replacement binding lost or accepted: exists=%v status=%d", exists, w.Code)
						}
						assertSessionKept(t, w)
					}
				})
			}
		}
	}
}

func TestSessionCheckStatusSemantics(t *testing.T) {
	for _, rpc := range []string{"Validate", "Can", "local-only-Can"} {
		for _, htmx := range []bool{false, true} {
			for _, code := range []codes.Code{codes.Unauthenticated, codes.PermissionDenied, codes.Internal, codes.Unavailable, codes.DeadlineExceeded, codes.Canceled, codes.Unimplemented, codes.InvalidArgument, codes.Unknown} {
				t.Run(rpc+"/"+code.String()+map[bool]string{true: "/htmx", false: "/browser"}[htmx], func(t *testing.T) {
					stub := currentSessionStub()
					bearer, method := "current-bearer", rpc
					if rpc == "local-only-Can" {
						bearer, method = "", "Can"
					}
					stub.fail = map[string]codes.Code{method: code}
					h := setupIdentityHandler(t, stub)
					cookie := boundSession(t, h, "u1", "alice", "tenant", bearer, []string{"admin"})
					before, _ := h.Sessions.Get(cookie)
					r := sessionRequest("/protected", cookie)
					r.Method = http.MethodPost
					if htmx {
						r.Header.Set("HX-Request", "true")
					}
					w := httptest.NewRecorder()
					guard, ran := revalidateGuard(h)
					guard(w, r)
					if *ran || (rpc == "Validate" && stub.calls["Can"] != 0) {
						t.Fatal("failed check reached a protected action or later authorization")
					}
					after, retained := h.Sessions.Get(cookie)
					want := http.StatusServiceUnavailable
					switch code {
					case codes.Unauthenticated:
						want = http.StatusSeeOther
						if htmx {
							want = http.StatusOK
							if w.Header().Get("HX-Redirect") != "/login" {
								t.Fatal("missing HTMX login navigation")
							}
						} else if w.Header().Get("Location") != "/login" {
							t.Fatal("missing browser login navigation")
						}
						if retained {
							t.Fatal("invalid session retained")
						}
						assertSessionCleared(t, w)
					case codes.PermissionDenied:
						want = http.StatusForbidden
					}
					if code != codes.Unauthenticated {
						assertSessionKept(t, w)
						if !retained || !reflect.DeepEqual(before, after) || w.Header().Get("HX-Redirect") != "" || w.Header().Get("Location") != "" {
							t.Fatal("denial/outage changed session or navigated away")
						}
					}
					if w.Code != want {
						t.Fatalf("status=%d, want %d", w.Code, want)
					}
					if strings.Contains(w.Body.String(), "current-bearer") || strings.Contains(w.Body.String(), "provider response") {
						t.Fatal("response exposed provider error or credentials")
					}
				})
			}
		}
	}
}

func TestSessionPolicyDenialIsForbidden(t *testing.T) {
	for _, bound := range []bool{true, false} {
		stub := currentSessionStub()
		stub.allowAdmin = false
		h := setupIdentityHandler(t, stub)
		bearer := "provider-bearer"
		if !bound {
			bearer = ""
		}
		cookie := boundSession(t, h, "u1", "alice", "tenant", bearer, []string{"admin"})
		w := httptest.NewRecorder()
		guard, ran := revalidateGuard(h)
		guard(w, sessionRequest("/protected", cookie))
		if *ran || w.Code != http.StatusForbidden {
			t.Fatalf("policy denial: ran=%v status=%d", *ran, w.Code)
		}
		assertSessionKept(t, w)
	}
}

func TestSessionDiscoveryFailuresKeepCookie(t *testing.T) {
	for _, capability := range []string{"auth", "authorizer"} {
		for _, code := range []codes.Code{codes.Unavailable, codes.Unauthenticated, codes.PermissionDenied} {
			t.Run(capability+"/"+code.String(), func(t *testing.T) {
				stub := currentSessionStub()
				stub.onDiscovery = func(_ context.Context, found string) error {
					if found == capability {
						return status.Error(code, "discovery failed")
					}
					return nil
				}
				h := setupIdentityHandler(t, stub)
				cookie := boundSession(t, h, "u1", "alice", "tenant", "bearer", []string{"admin"})
				w := httptest.NewRecorder()
				guard, ran := revalidateGuard(h)
				guard(w, sessionRequest("/protected", cookie))
				if *ran || w.Code != http.StatusServiceUnavailable || stub.calls["Can"] != 0 {
					t.Fatalf("discovery failure: ran=%v status=%d", *ran, w.Code)
				}
				assertSessionKept(t, w)
				if _, exists := h.Sessions.Get(cookie); !exists {
					t.Fatal("discovery failure revoked the session")
				}
			})
		}
	}
}

func TestSessionChecksBoundDiscoveryAndRPC(t *testing.T) {
	for _, phase := range []string{"auth", "Validate", "authorizer", "Can"} {
		t.Run(phase, func(t *testing.T) {
			started := make(chan time.Duration, 1)
			block := func(ctx context.Context) {
				deadline, _ := ctx.Deadline()
				started <- time.Until(deadline)
				<-ctx.Done()
			}
			stub := currentSessionStub()
			stub.onCall = func(ctx context.Context, rpc string) {
				if rpc == phase {
					block(ctx)
				}
			}
			stub.onDiscovery = func(ctx context.Context, capability string) error {
				if capability == phase {
					block(ctx)
					return ctx.Err()
				}
				return nil
			}
			h := setupIdentityHandler(t, stub)
			cookie := boundSession(t, h, "u1", "alice", "tenant", "bearer", []string{"admin"})
			r := sessionRequest("/protected", cookie)
			ctx, cancel := context.WithCancel(r.Context())
			defer cancel()
			w := httptest.NewRecorder()
			guard, ran := revalidateGuard(h)
			done := make(chan struct{})
			go func() {
				defer close(done)
				guard(w, r.WithContext(ctx))
			}()
			select {
			case remaining := <-started:
				if remaining <= 0 || remaining > 8*time.Second {
					t.Errorf("%s deadline remaining=%s, want positive and at most eight seconds", phase, remaining)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("check did not start")
			}
			cancel()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("cancellation did not stop the request")
			}
			if *ran || w.Code != http.StatusServiceUnavailable {
				t.Fatalf("canceled check: ran=%v status=%d", *ran, w.Code)
			}
			assertSessionKept(t, w)
			if _, exists := h.Sessions.Get(cookie); !exists {
				t.Fatal("cancellation revoked the session")
			}
		})
	}
}

// Exercise the conditional deletion itself, after the post-RPC check's window.
func TestSessionRejectionCannotDeleteReplacement(t *testing.T) {
	stub := currentSessionStub()
	h := setupIdentityHandler(t, stub)
	cookie := boundSession(t, h, "u1", "alice", "tenant", "old-bearer", []string{"admin"})
	snap, _ := h.Sessions.Snapshot(cookie)
	h.Sessions.BindAuthLocalToken(cookie, "replacement-bearer")
	w := httptest.NewRecorder()
	h.rejectBoundSession(w, sessionRequest("/protected", cookie), cookie, snap, "unauthenticated")
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d", w.Code)
	}
	assertSessionKept(t, w)
	if current, exists := h.Sessions.Get(cookie); !exists || current.AuthLocalToken != "replacement-bearer" {
		t.Fatal("late rejection deleted the replacement binding")
	}
}
