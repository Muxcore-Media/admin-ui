package handler

import (
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

type promMetric struct {
	name   string
	help   string
	typ    string
	values []promValue
}

type promValue struct {
	labels map[string]string
	value  float64
}

func (h *Handler) MetricsPage(w http.ResponseWriter, r *http.Request) {
	if h.Core == nil {
		nav := h.nav(r.URL.Path)
		content := templates.MetricsPage(nil)
		component := templates.Layout("Metrics", nav, content)
		h.render(w, r, component)
		return
	}

	// Fetch and parse the prometheus metrics from core's /metrics endpoint.
	// We use core's HTTP address from discovery.
	members, _, err := h.Core.Discovery.Members(r.Context())
	if err != nil || len(members) == 0 {
		nav := h.nav(r.URL.Path)
		content := templates.MetricsPage(nil)
		component := templates.Layout("Metrics", nav, content)
		h.render(w, r, component)
		return
	}

	// Use the leader's HTTP address
	leaderAddr := ""
	for _, m := range members {
		// Try to find the leader or just use first member
		if leaderAddr == "" {
			leaderAddr = m.GetHttpAddr()
		}
	}

	if leaderAddr == "" {
		nav := h.nav(r.URL.Path)
		content := templates.MetricsPage(nil)
		component := templates.Layout("Metrics", nav, content)
		h.render(w, r, component)
		return
	}

	// The core exposes metrics on its HTTP port at /metrics
	metricsURL := fmt.Sprintf("http://%s/metrics", leaderAddr)
	resp, err := http.Get(metricsURL)
	if err != nil {
		slog.Warn("metrics: fetch failed", "url", metricsURL, "error", err)
		nav := h.nav(r.URL.Path)
		content := templates.MetricsPage(nil)
		component := templates.Layout("Metrics", nav, content)
		h.render(w, r, component)
		return
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		slog.Warn("metrics: read failed", "error", err)
		nav := h.nav(r.URL.Path)
		content := templates.MetricsPage(nil)
		component := templates.Layout("Metrics", nav, content)
		h.render(w, r, component)
		return
	}

	metrics := parsePrometheus(string(raw))

	nav := h.nav(r.URL.Path)
	content := templates.MetricsPage(metrics)
	component := templates.Layout("Metrics", nav, content)
	h.render(w, r, component)
}

func parsePrometheus(raw string) []templates.MetricGroup {
	lines := strings.Split(raw, "\n")

	var groups []templates.MetricGroup
	var current templates.MetricGroup

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "# HELP ") {
			if current.Name != "" {
				groups = append(groups, current)
			}
			current = templates.MetricGroup{}
			current.Name = strings.Fields(line)[2]
			current.Help = strings.Join(strings.Fields(line)[3:], " ")
			continue
		}

		if strings.HasPrefix(line, "# TYPE ") {
			fields := strings.Fields(line)
			if len(fields) >= 4 {
				current.Type = fields[3]
			}
			continue
		}

		if strings.HasPrefix(line, "#") {
			continue
		}

		// Parse metric line: name{labels} value
		name, value, labels := parseMetricLine(line)
		if name != "" {
			current.Metrics = append(current.Metrics, templates.MetricEntry{
				Name:   name,
				Value:  formatMetricValue(value),
				Labels: labels,
			})
		}
	}

	if current.Name != "" {
		groups = append(groups, current)
	}

	return groups
}

func parseMetricLine(line string) (name string, value float64, labels map[string]string) {
	// Strip comment if any
	if idx := strings.IndexByte(line, '#'); idx != -1 {
		line = strings.TrimSpace(line[:idx])
	}

	// Find the value at the end
	lastSpace := strings.LastIndexByte(line, ' ')
	if lastSpace == -1 {
		return "", 0, nil
	}

	valStr := strings.TrimSpace(line[lastSpace+1:])
	v, err := strconv.ParseFloat(valStr, 64)
	if err != nil {
		return "", 0, nil
	}
	value = v

	metricPart := strings.TrimSpace(line[:lastSpace])

	// Check for labels
	if braceIdx := strings.IndexByte(metricPart, '{'); braceIdx != -1 {
		name = metricPart[:braceIdx]
		labelStr := metricPart[braceIdx+1:]
		if endBrace := strings.LastIndexByte(labelStr, '}'); endBrace != -1 {
			labelStr = labelStr[:endBrace]
		}
		labels = make(map[string]string)
		for _, pair := range strings.Split(labelStr, ",") {
			pair = strings.TrimSpace(pair)
			if eq := strings.IndexByte(pair, '='); eq != -1 {
				key := pair[:eq]
				val := strings.Trim(pair[eq+1:], "\"")
				labels[key] = val
			}
		}
	} else {
		name = metricPart
	}

	return name, value, labels
}

func formatMetricValue(v float64) string {
	if v == math.Trunc(v) {
		return fmt.Sprintf("%.0f", v)
	}
	return fmt.Sprintf("%.2f", v)
}
