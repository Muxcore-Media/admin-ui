package handler

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Muxcore-Media/admin-ui/session"
)

type seenReq struct {
	hdr  http.Header
	body string
}

func fakeUpstream(t *testing.T, seen *[]seenReq) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		*seen = append(*seen, seenReq{r.Header.Clone(), string(b)})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func idSession() *session.Session {
	return &session.Session{UserID: "uid-42", Username: "alice", Roles: []string{"admin"}, AuthLocalToken: "al-secret"}
}

func TestRequestMediaCarriesUserIDAndBearer(t *testing.T) {
	var seen []seenReq
	srv := fakeUpstream(t, &seen)
	h := New(nil, session.NewStore(0), false, "test", nil, false, "", nil, nil)
	h.RequestMediaURL = srv.URL

	// GET path
	ctx := context.WithValue(context.Background(), ctxSessionKey, idSession())
	if _, _, err := h.requestGET(ctx, srv.URL, "/api/requests"); err != nil {
		t.Fatal(err)
	}
	// approve path
	r := requestWithSession("POST", "/request/r1/approve", idSession())
	r.SetPathValue("id", "r1")
	h.RequestApprove(httptest.NewRecorder(), r)
	// create path
	r = requestWithSession("POST", "/request", idSession())
	r.Body = io.NopCloser(strings.NewReader("tmdb_id=1&title=T"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.RequestCreate(httptest.NewRecorder(), r)

	if len(seen) != 3 {
		t.Fatalf("expected 3 upstream calls, got %d", len(seen))
	}
	for i, s := range seen {
		if got := s.hdr.Get("Authorization"); got != "Bearer al-secret" {
			t.Errorf("call %d Authorization = %q", i, got)
		}
		if got := s.hdr.Get("X-Caller-Id"); got != "uid-42" {
			t.Errorf("call %d X-Caller-Id = %q, want user id", i, got)
		}
		if strings.Contains(s.body, "alice") {
			t.Errorf("call %d body leaks username as identity: %s", i, s.body)
		}
	}
}

func TestRequestMediaNoTokenNoAuthorization(t *testing.T) {
	var seen []seenReq
	srv := fakeUpstream(t, &seen)
	h := New(nil, session.NewStore(0), false, "test", nil, false, "", nil, nil)
	sess := &session.Session{UserID: "uid-42", Username: "alice"}
	ctx := context.WithValue(context.Background(), ctxSessionKey, sess)
	if _, _, err := h.requestGET(ctx, srv.URL, "/api/requests"); err != nil {
		t.Fatal(err)
	}
	if got := seen[0].hdr.Get("Authorization"); got != "" {
		t.Fatalf("unexpected Authorization %q", got)
	}
	if seen[0].hdr.Get("X-Caller-Id") != "uid-42" {
		t.Fatal("X-Caller-Id missing")
	}
}

func TestSchedulerHTTPDoToken(t *testing.T) {
	var seen []seenReq
	srv := fakeUpstream(t, &seen)
	do := func() {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/list", nil)
		resp, err := schedulerHTTPDo(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
	}
	t.Setenv("ADMIN_UI_SCHEDULER_TOKEN", "")
	t.Setenv("SCHEDULER_HTTP_TOKEN", "")
	do()
	t.Setenv("SCHEDULER_HTTP_TOKEN", "fallback")
	do()
	t.Setenv("ADMIN_UI_SCHEDULER_TOKEN", "primary")
	do()
	if seen[0].hdr.Get("Authorization") != "" {
		t.Errorf("auth header present when unset: %q", seen[0].hdr.Get("Authorization"))
	}
	if got := seen[1].hdr.Get("Authorization"); got != "Bearer fallback" {
		t.Errorf("fallback: %q", got)
	}
	if got := seen[2].hdr.Get("Authorization"); got != "Bearer primary" {
		t.Errorf("primary: %q", got)
	}
}

func TestMonitorSummaryToken(t *testing.T) {
	var seen []seenReq
	srv := fakeUpstream(t, &seen)
	h := New(nil, session.NewStore(0), false, "test", nil, false, "", nil, nil)
	h.HealthMonitorURL = srv.URL
	call := func() {
		h.MonitorSummary(httptest.NewRecorder(), mustRequest("GET", "/dashboard/monitor"))
	}
	t.Setenv("ADMIN_UI_HEALTH_MONITOR_TOKEN", "")
	t.Setenv("HEALTH_MONITOR_HTTP_TOKEN", "")
	call()
	t.Setenv("HEALTH_MONITOR_HTTP_TOKEN", "fallback")
	call()
	t.Setenv("ADMIN_UI_HEALTH_MONITOR_TOKEN", "primary")
	call()
	if len(seen) != 3 {
		t.Fatalf("got %d calls", len(seen))
	}
	if seen[0].hdr.Get("Authorization") != "" {
		t.Errorf("auth header present when unset")
	}
	if got := seen[1].hdr.Get("Authorization"); got != "Bearer fallback" {
		t.Errorf("fallback: %q", got)
	}
	if got := seen[2].hdr.Get("Authorization"); got != "Bearer primary" {
		t.Errorf("primary: %q", got)
	}
}
