package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

func (h *Handler) StreamsNotificationsPage(w http.ResponseWriter, r *http.Request) {
	pageCtx, cancel := context.WithTimeout(r.Context(), playbackMonitorDialTimeout+2*playbackMonitorReadTimeout+time.Second)
	defer cancel()

	data := templates.StreamsNotificationsPageData{}
	if msg := strings.TrimSpace(r.URL.Query().Get("success")); msg != "" {
		data.Success = msg
	}
	if msg := strings.TrimSpace(r.URL.Query().Get("error")); msg != "" {
		data.Error = msg
	}

	rules, err := h.fetchNotificationRules(pageCtx, "")
	if err != nil {
		data.SoftNote = true
		if h.Core != nil {
			data.Error = err.Error()
		}
		h.renderStreamsNotifications(w, r, data)
		return
	}
	dests, _ := h.fetchNotificationDestinations(pageCtx)
	for _, dest := range dests {
		data.Destinations = append(data.Destinations, templates.NotificationDestinationRow{
			ID:           dest.ID,
			Name:         dest.Name,
			Type:         dest.Type,
			Enabled:      dest.Enabled,
			EnabledLabel: boolLabel(dest.Enabled),
			EventsLabel:  strings.Join(dest.Events, ", "),
		})
	}
	for _, rule := range rules {
		data.Rules = append(data.Rules, templates.NotificationRuleRow{
			ID:              rule.ID,
			Name:            rule.Name,
			Enabled:         rule.Enabled,
			EnabledLabel:    boolLabel(rule.Enabled),
			EventType:       rule.EventType,
			TitleTemplate:   rule.TitleTemplate,
			MessageTemplate: rule.MessageTemplate,
			Severity:        rule.Severity,
			TranscodeOnly:   rule.Filters.TranscodeOnly,
		})
	}
	h.renderStreamsNotifications(w, r, data)
}

func (h *Handler) StreamsNotificationsCreate(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), playbackMonitorDialTimeout+playbackMonitorReadTimeout)
	defer cancel()

	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/streams/notifications?error=invalid+form", http.StatusSeeOther)
		return
	}
	enabled := r.FormValue("enabled") == "1" || strings.EqualFold(r.FormValue("enabled"), "true")
	body, _ := json.Marshal(map[string]any{
		"name":             strings.TrimSpace(r.FormValue("name")),
		"event_type":       strings.TrimSpace(r.FormValue("event_type")),
		"title_template":   strings.TrimSpace(r.FormValue("title_template")),
		"message_template": strings.TrimSpace(r.FormValue("message_template")),
		"severity":         strings.TrimSpace(r.FormValue("severity")),
		"enabled":          enabled,
		"destination_ids":  r.Form["destination_ids"],
		"filters": map[string]any{
			"transcode_only": r.FormValue("transcode_only") == "1",
		},
	})
	if _, code, err := h.monitorHTTPRequest(ctx, http.MethodPost, "/notification/rules", body); err != nil || code >= 300 {
		msg := "create+failed"
		if err != nil {
			msg = err.Error()
		}
		http.Redirect(w, r, "/streams/notifications?error="+url.QueryEscape(msg), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/streams/notifications?success=Rule+created", http.StatusSeeOther)
}

func (h *Handler) StreamsNotificationsDelete(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), playbackMonitorDialTimeout+playbackMonitorReadTimeout)
	defer cancel()

	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/streams/notifications?error=invalid+form", http.StatusSeeOther)
		return
	}
	id := strings.TrimSpace(r.FormValue("id"))
	if id == "" {
		http.Redirect(w, r, "/streams/notifications?error=missing+rule+id", http.StatusSeeOther)
		return
	}
	if _, code, err := h.monitorHTTPRequest(ctx, http.MethodDelete, "/notification/rules/"+id, nil); err != nil || code >= 300 {
		msg := "delete+failed"
		if err != nil {
			msg = err.Error()
		}
		http.Redirect(w, r, "/streams/notifications?error="+url.QueryEscape(msg), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/streams/notifications?success=Rule+deleted", http.StatusSeeOther)
}

func (h *Handler) StreamsNotificationsDestinationCreate(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), playbackMonitorDialTimeout+playbackMonitorReadTimeout)
	defer cancel()

	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/streams/notifications?error=invalid+form", http.StatusSeeOther)
		return
	}
	destType := strings.TrimSpace(r.FormValue("type"))
	config := map[string]string{}
	switch destType {
	case "apprise":
		config["urls"] = strings.TrimSpace(r.FormValue("apprise_urls"))
	default:
		config["webhook_url"] = strings.TrimSpace(r.FormValue("webhook_url"))
	}
	enabled := r.FormValue("enabled") == "1" || strings.EqualFold(r.FormValue("enabled"), "true")
	body, _ := json.Marshal(map[string]any{
		"name":    strings.TrimSpace(r.FormValue("name")),
		"type":    destType,
		"enabled": enabled,
		"config":  config,
		"events":  r.Form["events"],
	})
	if _, code, err := h.monitorHTTPRequest(ctx, http.MethodPost, "/notification/destinations", body); err != nil || code >= 300 {
		msg := "destination+create+failed"
		if err != nil {
			msg = err.Error()
		}
		http.Redirect(w, r, "/streams/notifications?error="+url.QueryEscape(msg), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/streams/notifications?success=Destination+created", http.StatusSeeOther)
}

func (h *Handler) StreamsNotificationsDestinationDelete(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), playbackMonitorDialTimeout+playbackMonitorReadTimeout)
	defer cancel()

	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/streams/notifications?error=invalid+form", http.StatusSeeOther)
		return
	}
	id := strings.TrimSpace(r.FormValue("id"))
	if id == "" {
		http.Redirect(w, r, "/streams/notifications?error=missing+destination+id", http.StatusSeeOther)
		return
	}
	if _, code, err := h.monitorHTTPRequest(ctx, http.MethodDelete, "/notification/destinations/"+id, nil); err != nil || code >= 300 {
		msg := "destination+delete+failed"
		if err != nil {
			msg = err.Error()
		}
		http.Redirect(w, r, "/streams/notifications?error="+url.QueryEscape(msg), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/streams/notifications?success=Destination+deleted", http.StatusSeeOther)
}

func (h *Handler) StreamsNotificationsDestinationTest(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), playbackMonitorDialTimeout+playbackMonitorReadTimeout)
	defer cancel()

	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/streams/notifications?error=invalid+form", http.StatusSeeOther)
		return
	}
	id := strings.TrimSpace(r.FormValue("id"))
	if id == "" {
		http.Redirect(w, r, "/streams/notifications?error=missing+destination+id", http.StatusSeeOther)
		return
	}
	if _, code, err := h.monitorHTTPRequest(ctx, http.MethodPost, "/notification/destinations/"+id+"/test", nil); err != nil || code >= 300 {
		msg := "destination+test+failed"
		if err != nil {
			msg = err.Error()
		}
		http.Redirect(w, r, "/streams/notifications?error="+url.QueryEscape(msg), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/streams/notifications?success=Test+notification+sent", http.StatusSeeOther)
}

type notificationDestinationJSON struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	Enabled bool     `json:"enabled"`
	Events  []string `json:"events"`
}

func (h *Handler) fetchNotificationDestinations(ctx context.Context) ([]notificationDestinationJSON, error) {
	body, code, err := h.monitorHTTPRequest(ctx, http.MethodGet, "/notification/destinations", nil)
	if err != nil {
		return nil, err
	}
	if code >= 300 {
		return nil, fmt.Errorf("monitor returned %d: %s", code, strings.TrimSpace(string(body)))
	}
	var resp struct {
		Destinations []notificationDestinationJSON `json:"destinations"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	return resp.Destinations, nil
}

type notificationRuleJSON struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Enabled         bool   `json:"enabled"`
	EventType       string `json:"event_type"`
	TitleTemplate   string `json:"title_template"`
	MessageTemplate string `json:"message_template"`
	Severity        string `json:"severity"`
	Filters         struct {
		TranscodeOnly bool `json:"transcode_only"`
	} `json:"filters"`
}

func (h *Handler) fetchNotificationRules(ctx context.Context, eventType string) ([]notificationRuleJSON, error) {
	path := "/notification/rules"
	if eventType != "" {
		path += "?event_type=" + url.QueryEscape(eventType)
	}
	body, code, err := h.monitorHTTPRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	if code >= 300 {
		return nil, fmt.Errorf("monitor returned %d: %s", code, strings.TrimSpace(string(body)))
	}
	var resp struct {
		Rules []notificationRuleJSON `json:"rules"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	return resp.Rules, nil
}

func (h *Handler) monitorHTTPRequest(ctx context.Context, method, path string, body []byte) ([]byte, int, error) {
	dialCtx, dialCancel := context.WithTimeout(ctx, playbackMonitorDialTimeout)
	base, err := h.playbackMonitorHTTPBase(dialCtx)
	dialCancel()
	if err != nil {
		return nil, 0, err
	}
	var reader io.Reader
	if len(body) > 0 {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, reader)
	if err != nil {
		return nil, 0, err
	}
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	client := &http.Client{Timeout: playbackMonitorReadTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	out, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return out, resp.StatusCode, nil
}

func (h *Handler) renderStreamsNotifications(w http.ResponseWriter, r *http.Request, data templates.StreamsNotificationsPageData) {
	content := templates.StreamsNotificationsPage(data)
	nav := h.nav("/streams/notifications")
	h.render(w, r, templates.Layout("Notification rules", nav, content))
}
