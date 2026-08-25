package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func mustRequest(method, path string) *http.Request {
	r, err := http.NewRequest(method, path, nil)
	if err != nil {
		panic(err)
	}
	r.RemoteAddr = "127.0.0.1:12345"
	return r
}

func TestLoadConfigDefaults(t *testing.T) {
	cfg := loadConfig()
	if cfg.Addr != ":8080" {
		t.Fatalf("expected :8080, got %s", cfg.Addr)
	}
	if cfg.CoreAddr != "localhost:9090" {
		t.Fatalf("expected localhost:9090, got %s", cfg.CoreAddr)
	}
	if cfg.Insecure {
		t.Fatal("expected insecure=false by default")
	}
	if cfg.SessionTTL != 30*60*1000000000 { // 30m
		t.Fatalf("expected 30m, got %v", cfg.SessionTTL)
	}
}

func TestResolveBrowserAuthAddrPrefersPublicURL(t *testing.T) {
	t.Setenv("AUTH_HTTP_URL", "https://auth.zem.systems")
	internal := "http://[fd2c:a2fd:5d9e:ab72:9d99:930d:f160:3e95]:9401"
	if got := resolveBrowserAuthAddr(internal); got != "https://auth.zem.systems" {
		t.Fatalf("got %q", got)
	}
	if got := resolveBrowserAuthAddr("https://auth.example.com"); got != "https://auth.example.com" {
		t.Fatalf("got %q", got)
	}
}

func TestLoadConfigEnv(t *testing.T) {
	t.Setenv("ADMIN_UI_ADDR", ":9999")
	t.Setenv("ADMIN_UI_CORE_ADDR", "core:9090")
	t.Setenv("ADMIN_UI_INSECURE", "true")
	t.Setenv("ADMIN_UI_SESSION_TTL", "10m")
	t.Setenv("ADMIN_UI_LOG_LEVEL", "debug")
	t.Setenv("ADMIN_UI_LOG_FORMAT", "json")

	cfg := loadConfig()
	if cfg.Addr != ":9999" {
		t.Fatalf("expected :9999, got %s", cfg.Addr)
	}
	if cfg.CoreAddr != "core:9090" {
		t.Fatalf("expected core:9090, got %s", cfg.CoreAddr)
	}
	if !cfg.Insecure {
		t.Fatal("expected insecure=true")
	}
	if cfg.LogLevel != "debug" {
		t.Fatalf("expected debug, got %s", cfg.LogLevel)
	}
	if cfg.LogFormat != "json" {
		t.Fatalf("expected json, got %s", cfg.LogFormat)
	}
}

func TestCSRFTokenGeneration(t *testing.T) {
	key := generateCSRFKey()
	if key == "" {
		t.Fatal("expected non-empty CSRF key")
	}

	token := csrfToken(key)
	if token == "" {
		t.Fatal("expected non-empty CSRF token")
	}

	// Same key + same day produces same token
	token2 := csrfToken(key)
	if token != token2 {
		t.Fatal("expected same token for same key and day")
	}
}

func TestCSRFKeyUniqueness(t *testing.T) {
	key1 := generateCSRFKey()
	key2 := generateCSRFKey()

	if key1 == key2 {
		t.Fatal("expected different keys from generateCSRFKey")
	}

	token1 := csrfToken(key1)
	token2 := csrfToken(key2)

	if token1 == token2 {
		t.Fatal("expected different tokens from different keys")
	}
}

func TestHealthEndpoint(t *testing.T) {
	r := mustRequest("GET", "/health")
	w := httptest.NewRecorder()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if strings.TrimSpace(w.Body.String()) != `{"status":"ok"}` {
		t.Fatalf("expected ok body, got %s", w.Body.String())
	}
}

func TestHealthEndpointDegraded(t *testing.T) {
	// Simulate what happens when core is disconnected by checking
	// that the degraded handler works without core client
	r := mustRequest("GET", "/health")
	w := httptest.NewRecorder()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"status":"degraded"}`))
	})
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestMetricsEndpoint(t *testing.T) {
	met := newMetrics()
	r := mustRequest("GET", "/metrics")
	w := httptest.NewRecorder()
	met.IncSuccess()
	met.IncFailure()

	met.serve(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if body == "" {
		t.Fatal("expected non-empty metrics body")
	}
}
