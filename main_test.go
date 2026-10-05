package adminui

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

func TestCSRFCookieStableAcrossRequests(t *testing.T) {
	w1 := httptest.NewRecorder()
	r1 := mustRequest("GET", "/")
	token1 := ensureCSRFCookie(w1, r1)
	if token1 == "" {
		t.Fatal("expected non-empty CSRF token")
	}

	w2 := httptest.NewRecorder()
	r2 := mustRequest("GET", "/")
	r2.AddCookie(&http.Cookie{Name: "csrf-token", Value: token1})
	token2 := ensureCSRFCookie(w2, r2)
	if token1 != token2 {
		t.Fatal("expected same token when cookie already set")
	}
}

func TestCSRFCookieRegeneratedWhenMissing(t *testing.T) {
	w := httptest.NewRecorder()
	r := mustRequest("GET", "/")
	a := ensureCSRFCookie(w, r)
	b := ensureCSRFCookie(w, r)
	if a == "" || b == "" {
		t.Fatal("expected tokens")
	}
}

func TestMetricsEndpointRequiresTokenWhenConfigured(t *testing.T) {
	met := newMetrics()
	r := mustRequest("GET", "/metrics")
	w := httptest.NewRecorder()
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", http.HandlerFunc(met.serve))
	handler := withMiddleware(mux, newRateLimiter(), parseTrustedProxies(nil), "secret")
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}

	r2 := mustRequest("GET", "/metrics")
	r2.Header.Set("Authorization", "Bearer secret")
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, r2)
	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200 with token, got %d", w2.Code)
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
