package main

import (
	"fmt"
	"net/http"
	"runtime"
	"sync/atomic"
	"time"
)

type metrics struct {
	requests     atomic.Int64
	activeSess   atomic.Int64
	loginFails   atomic.Int64
	loginSuccess atomic.Int64
	startTime    time.Time
}

func newMetrics() *metrics {
	return &metrics{startTime: time.Now()}
}

func (m *metrics) IncSuccess() {
	m.loginSuccess.Add(1)
}

func (m *metrics) IncFailure() {
	m.loginFails.Add(1)
}

func (m *metrics) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	uptime := time.Since(m.startTime).Seconds()

	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	_, _ = fmt.Fprintf(w, `# HELP admin_ui_requests_total Total HTTP requests processed
# TYPE admin_ui_requests_total counter
admin_ui_requests_total %d

# HELP admin_ui_active_sessions Current active sessions
# TYPE admin_ui_active_sessions gauge
admin_ui_active_sessions %d

# HELP admin_ui_login_success_total Successful logins
# TYPE admin_ui_login_success_total counter
admin_ui_login_success_total %d

# HELP admin_ui_login_failures_total Failed login attempts
# TYPE admin_ui_login_failures_total counter
admin_ui_login_failures_total %d

# HELP admin_ui_uptime_seconds Server uptime
# TYPE admin_ui_uptime_seconds gauge
admin_ui_uptime_seconds %.0f

# HELP admin_ui_go_mem_alloc_bytes Current memory allocation
# TYPE admin_ui_go_mem_alloc_bytes gauge
admin_ui_go_mem_alloc_bytes %d

# HELP admin_ui_go_goroutines Current goroutine count
# TYPE admin_ui_go_goroutines gauge
admin_ui_go_goroutines %d
`,
		m.requests.Load(),
		m.activeSess.Load(),
		m.loginSuccess.Load(),
		m.loginFails.Load(),
		uptime,
		memStats.Alloc,
		runtime.NumGoroutine(),
	)
}
