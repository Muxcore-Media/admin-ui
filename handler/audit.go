package handler

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

const (
	auditDialTimeout  = 3 * time.Second
	auditReadTimeout  = 5 * time.Second
	auditPageTimeout  = auditDialTimeout + auditReadTimeout + time.Second
)

func (h *Handler) AuditPage(w http.ResponseWriter, r *http.Request) {
	filter := templates.ParseAuditFilters(r)

	page := 1
	if p := r.URL.Query().Get("page"); p != "" {
		if v, err := strconv.Atoi(p); err == nil && v > 0 {
			page = v
		}
	}

	if h.Core == nil || h.Core.Audit == nil {
		slog.Warn("audit: core audit client unavailable")
		nav := h.nav(r.URL.Path)
		content := templates.AuditNoModule()
		component := templates.Layout("Audit Log", nav, content)
		h.render(w, r, component)
		return
	}

	from := ""
	to := ""
	if filter.From != "" {
		if t, err := time.Parse("2006-01-02T15:04", filter.From); err == nil {
			from = t.Format(time.RFC3339)
		}
	}
	if filter.To != "" {
		if t, err := time.Parse("2006-01-02T15:04", filter.To); err == nil {
			to = t.Format(time.RFC3339)
		}
	}

	perPage := 50
	maxResults := int32(page * perPage)
	if maxResults < 500 {
		maxResults = 500
	}

	pageCtx, pageCancel := context.WithTimeout(r.Context(), auditPageTimeout)
	defer pageCancel()

	readCtx, readCancel := context.WithTimeout(pageCtx, auditReadTimeout)
	entries, err := h.Core.Audit.Query(readCtx, filter.Actor, filter.Action, filter.Resource, filter.TraceID, from, to, maxResults)
	readCancel()
	if err != nil {
		slog.Warn("audit: query failed", "error", err)
		nav := h.nav(r.URL.Path)
		content := templates.AuditNoModule()
		component := templates.Layout("Audit Log", nav, content)
		h.render(w, r, component)
		return
	}

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
			ID:         e.GetId(),
			Timestamp:  time.Unix(e.GetTimestamp(), 0),
			Actor:      e.GetActor(),
			Action:     e.GetAction(),
			Resource:   e.GetResource(),
			ResourceID: e.GetResourceId(),
			TraceID:    e.GetTraceId(),
			NodeID:     e.GetNodeId(),
		})
	}

	data := templates.AuditPageData{
		Entries:    items,
		Filter:     filter,
		Page:       page,
		TotalPages: totalPages,
	}

	nav := h.nav(r.URL.Path)
	content := templates.AuditPage(data)
	component := templates.Layout("Audit Log", nav, content)
	h.render(w, r, component)
}
