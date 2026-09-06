package handler

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Muxcore-Media/admin-ui/session"
	discoveryv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/discovery/v1"
	"github.com/Muxcore-Media/core/sdk/go/client"
	"google.golang.org/grpc"
)

func TestNotificationEventRequestReadyConstant(t *testing.T) {
	if notificationEventRequestReady != "media.request.ready" {
		t.Fatalf("got %q", notificationEventRequestReady)
	}
}

func TestNotificationDestinationCreateBodyIncludesReadyEvent(t *testing.T) {
	body := strings.NewReader("name=Discord+ready&type=discord&webhook_url=https%3A%2F%2Fdiscord.com%2Fapi%2Fwebhooks%2Fx&enabled=1&events=" + notificationEventRequestReady)
	r := httptest.NewRequest(http.MethodPost, "/streams/notifications/destinations/create", body)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := r.ParseForm(); err != nil {
		t.Fatal(err)
	}
	raw, err := notificationDestinationCreateBody(r)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	events, ok := payload["events"].([]any)
	if !ok || len(events) != 1 {
		t.Fatalf("events=%#v", payload["events"])
	}
	if events[0] != notificationEventRequestReady {
		t.Fatalf("got event %v", events[0])
	}
	cfg, ok := payload["config"].(map[string]any)
	if !ok || cfg["webhook_url"] != "https://discord.com/api/webhooks/x" {
		t.Fatalf("config=%#v", payload["config"])
	}
	if payload["type"] != "discord" {
		t.Fatalf("type=%v", payload["type"])
	}
}

func TestNotificationRuleCreateBodyIncludesReadyEvent(t *testing.T) {
	body := strings.NewReader(
		"name=Request+ready&event_type=" + notificationEventRequestReady +
			"&title_template=%7B%7Btitle%7D%7D+is+ready&message_template=%7B%7Brequested_by%7D%7D&severity=success&enabled=1",
	)
	r := httptest.NewRequest(http.MethodPost, "/streams/notifications/create", body)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := r.ParseForm(); err != nil {
		t.Fatal(err)
	}
	raw, err := notificationRuleCreateBody(r)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["event_type"] != notificationEventRequestReady {
		t.Fatalf("event_type=%v", payload["event_type"])
	}
	if payload["title_template"] != "{{title}} is ready" {
		t.Fatalf("title_template=%v", payload["title_template"])
	}
}

func TestNotificationDestinationCreateBodyWebhookType(t *testing.T) {
	body := strings.NewReader("name=Generic&type=webhook&webhook_url=https%3A%2F%2Fexample.com%2Fhook&enabled=1&events=" + notificationEventRequestReady)
	r := httptest.NewRequest(http.MethodPost, "/streams/notifications/destinations/create", body)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := r.ParseForm(); err != nil {
		t.Fatal(err)
	}
	raw, err := notificationDestinationCreateBody(r)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["type"] != "webhook" {
		t.Fatalf("type=%v", payload["type"])
	}
}

type monitorNotificationStub struct {
	mu       sync.Mutex
	destBody []byte
	ruleBody []byte
}

func setupNotificationsHandler(t *testing.T, stub *monitorNotificationStub) *Handler {
	t.Helper()
	monitorMux := http.NewServeMux()
	monitorMux.HandleFunc("GET /notification/rules", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"rules": []any{}})
	})
	monitorMux.HandleFunc("GET /notification/destinations", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"destinations": []any{}})
	})
	monitorMux.HandleFunc("POST /notification/destinations", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		stub.mu.Lock()
		stub.destBody = append([]byte(nil), body...)
		stub.mu.Unlock()
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"destination": map[string]any{"id": "dest_1"}})
	})
	monitorMux.HandleFunc("POST /notification/rules", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		stub.mu.Lock()
		stub.ruleBody = append([]byte(nil), body...)
		stub.mu.Unlock()
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"rule": map[string]any{"id": "rule_1"}})
	})
	monitorSrv := httptest.NewServer(monitorMux)
	t.Cleanup(monitorSrv.Close)

	host, port, err := net.SplitHostPort(monitorSrv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	httpAddr := net.JoinHostPort(host, port)

	discLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	discSrv := grpc.NewServer()
	discoveryv1.RegisterDiscoveryServiceServer(discSrv, capDiscovery{
		mods: map[string]*discoveryv1.ModuleInfoProto{
			capPlaybackMonitor: {Id: "playback-monitor", HttpAddr: httpAddr},
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

func TestStreamsNotificationsDestinationCreatePostsReadyEvent(t *testing.T) {
	stub := &monitorNotificationStub{}
	h := setupNotificationsHandler(t, stub)

	body := strings.NewReader(
		"name=Discord+ready&type=discord&webhook_url=https%3A%2F%2Fdiscord.com%2Fapi%2Fwebhooks%2Fx&enabled=1&events=" + notificationEventRequestReady,
	)
	r := httptest.NewRequest(http.MethodPost, "/streams/notifications/destinations/create", body)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.StreamsNotificationsDestinationCreate(w, r)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("status %d", w.Code)
	}
	stub.mu.Lock()
	raw := append([]byte(nil), stub.destBody...)
	stub.mu.Unlock()
	if len(raw) == 0 {
		t.Fatal("expected POST body to monitor")
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	events := payload["events"].([]any)
	if len(events) != 1 || events[0] != notificationEventRequestReady {
		t.Fatalf("events=%#v", payload["events"])
	}
}

func TestStreamsNotificationsRuleCreatePostsReadyEvent(t *testing.T) {
	stub := &monitorNotificationStub{}
	h := setupNotificationsHandler(t, stub)

	body := strings.NewReader(
		"name=Request+ready&event_type=" + notificationEventRequestReady +
			"&title_template=Ready%3A+%7B%7Btitle%7D%7D&message_template=%7B%7Brequested_by%7D%7D&severity=success&enabled=1",
	)
	r := httptest.NewRequest(http.MethodPost, "/streams/notifications/create", body)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.StreamsNotificationsCreate(w, r)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("status %d", w.Code)
	}
	stub.mu.Lock()
	raw := append([]byte(nil), stub.ruleBody...)
	stub.mu.Unlock()
	if len(raw) == 0 {
		t.Fatal("expected POST body to monitor")
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["event_type"] != notificationEventRequestReady {
		t.Fatalf("event_type=%v", payload["event_type"])
	}
}

func TestStreamsNotificationsPageShowsReadyEvent(t *testing.T) {
	stub := &monitorNotificationStub{}
	h := setupNotificationsHandler(t, stub)
	r := mustRequest("GET", "/streams/notifications")
	w := httptest.NewRecorder()
	h.StreamsNotificationsPage(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, notificationEventRequestReady) {
		t.Fatalf("expected ready event in page, got: %s", truncate(body, 800))
	}
	if !strings.Contains(body, "library ready / playable") {
		t.Fatal("expected ready event helper text")
	}
}
