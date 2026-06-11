package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

const (
	capAudit = "audit"
	methodQuery = "Query"
)

type queryRequest struct {
	Actor    string `json:"Actor"`
	Action   string `json:"Action"`
	Resource string `json:"Resource"`
	From     string `json:"From"`
	To       string `json:"To"`
	TraceID  string `json:"TraceID"`
}

type auditEntryJSON struct {
	ID         string `json:"ID"`
	Timestamp  int64  `json:"Timestamp"`
	Actor      string `json:"Actor"`
	Action     string `json:"Action"`
	Resource   string `json:"Resource"`
	ResourceID string `json:"ResourceID"`
	TraceID    string `json:"TraceID"`
	NodeID     string `json:"NodeID"`
}

func (h *Handler) AuditPage(w http.ResponseWriter, r *http.Request) {
	filter := templates.ParseAuditFilters(r)

	page := 1
	if p := r.URL.Query().Get("page"); p != "" {
		if v, err := strconv.Atoi(p); err == nil && v > 0 {
			page = v
		}
	}

	mod, err := h.Core.Discovery.FindByCapability(r.Context(), capAudit)
	if err != nil || len(mod) == 0 {
		slog.Warn("audit: no audit module found")
		nav := templates.Nav(navLinks, "/audit")
		content := templates.AuditNoModule()
		component := templates.Layout("Audit Log", nav, content)
		h.render(w, r, component)
		return
	}

	req := queryRequest{
		Actor:    filter.Actor,
		Action:   filter.Action,
		Resource: filter.Resource,
		TraceID:  filter.TraceID,
	}
	if filter.From != "" {
		if t, err := time.Parse("2006-01-02T15:04", filter.From); err == nil {
			req.From = t.Format(time.RFC3339)
		}
	}
	if filter.To != "" {
		if t, err := time.Parse("2006-01-02T15:04", filter.To); err == nil {
			req.To = t.Format(time.RFC3339)
		}
	}

	payload, err := json.Marshal(req)
	if err != nil {
		slog.Error("audit: marshal query", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	raw, err := h.Core.Mesh.Call(r.Context(), mod[0].GetId(), methodQuery, payload)
	if err != nil {
		slog.Warn("audit: mesh call failed", "error", err)
	}

	var entries []auditEntryJSON
	if raw != nil {
		if err := json.Unmarshal(raw, &entries); err != nil {
			slog.Warn("audit: unmarshal response", "error", err)
		}
	}

	perPage := 50
	total := len(entries)
	totalPages := (total + perPage - 1) / perPage
	if totalPages < 1 {
		totalPages = 1
	}

	start := (page - 1) * perPage
	if start > total {
		start = total
	}
	end := start + perPage
	if end > total {
		end = total
	}

	var items []templates.AuditEntryItem
	for _, e := range entries[start:end] {
		items = append(items, templates.AuditEntryItem{
			ID:         e.ID,
			Timestamp:  time.Unix(e.Timestamp, 0),
			Actor:      e.Actor,
			Action:     e.Action,
			Resource:   e.Resource,
			ResourceID: e.ResourceID,
			TraceID:    e.TraceID,
			NodeID:     e.NodeID,
		})
	}

	data := templates.AuditPageData{
		Entries:    items,
		Filter:     filter,
		Page:       page,
		TotalPages: totalPages,
	}

	nav := templates.Nav(navLinks, "/audit")
	content := templates.AuditPage(data)
	component := templates.Layout("Audit Log", nav, content)
	h.render(w, r, component)
}
