package handler

import (
	"encoding/json"
	"net/http"
	"strings"
)

// notificationEventRequestReady is the mesh event type operators should wire for
// Seerr-style outbound alerts when a watched request becomes playable (has_file),
// not when status=available. Emitters: request-media / playback-monitor (follow-up).
const notificationEventRequestReady = "media.request.ready"

// notificationEventGuardViolation matches playback-monitor's allowed guard rule type.
const notificationEventGuardViolation = "guard.violation"

func notificationDestinationCreateBody(r *http.Request) ([]byte, error) {
	destType := strings.TrimSpace(r.FormValue("type"))
	config := map[string]string{}
	switch destType {
	case "apprise":
		config["urls"] = strings.TrimSpace(r.FormValue("apprise_urls"))
	default:
		config["webhook_url"] = strings.TrimSpace(r.FormValue("webhook_url"))
	}
	enabled := r.FormValue("enabled") == "1" || strings.EqualFold(r.FormValue("enabled"), "true")
	return json.Marshal(map[string]any{
		"name":    strings.TrimSpace(r.FormValue("name")),
		"type":    destType,
		"enabled": enabled,
		"config":  config,
		"events":  r.Form["events"],
	})
}

func notificationRuleCreateBody(r *http.Request) ([]byte, error) {
	enabled := r.FormValue("enabled") == "1" || strings.EqualFold(r.FormValue("enabled"), "true")
	return json.Marshal(map[string]any{
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
}
