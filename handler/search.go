package handler

import (
	"context"
	"net/http"
	"strings"

	authv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/auth/v1"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

func (h *Handler) Search(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" || len(q) < 2 {
		w.Write(nil)
		return
	}
	qLower := strings.ToLower(q)

	var results []templates.SearchResult

	// Search modules
	if h.Core != nil {
		results = append(results, h.searchModules(r.Context(), qLower)...)
		results = append(results, h.searchAudit(r.Context(), qLower)...)
		results = append(results, h.searchUsers(r.Context(), qLower)...)
	}

	// Search events (from the in-memory ring buffer)
	results = append(results, h.searchEvents(qLower)...)

	if len(results) == 0 {
		w.Write([]byte(`<div class="p-4 text-sm text-gray-500">No results for "` + q + `".</div>`))
		return
	}

	if len(results) > 20 {
		results = results[:20]
	}

	component := templates.SearchResults(q, results)
	h.render(w, r, component)
}

func (h *Handler) searchModules(ctx context.Context, q string) []templates.SearchResult {
	members, _, err := h.Core.Discovery.Members(ctx)
	if err != nil {
		return nil
	}

	seen := make(map[string]bool)
	var out []templates.SearchResult

	for _, node := range members {
		for _, modID := range node.GetModules() {
			if seen[modID] {
				continue
			}
			seen[modID] = true

			info, err := h.Core.Discovery.Resolve(ctx, modID)
			if err != nil || info == nil {
				continue
			}

			name := info.GetName()
			if !matches(q, name, modID) {
				continue
			}

			out = append(out, templates.SearchResult{
				Label:   name,
				Sub:     modID,
				URL:     "/modules/" + modID,
				Section: "Modules",
			})
		}
	}
	return out
}

func (h *Handler) searchAudit(ctx context.Context, q string) []templates.SearchResult {
	if h.Core.Audit == nil {
		return nil
	}

	entries, err := h.Core.Audit.Query(ctx, q, "", "", "", "", "", 10)
	if err != nil || len(entries) == 0 {
		return nil
	}

	out := make([]templates.SearchResult, 0, len(entries))
	for _, e := range entries {
		out = append(out, templates.SearchResult{
			Label:   e.GetAction(),
			Sub:     e.GetActor() + " — " + e.GetResource(),
			URL:     "/audit?actor=" + e.GetActor(),
			Section: "Audit",
		})
	}
	return out
}

func (h *Handler) searchUsers(ctx context.Context, q string) []templates.SearchResult {
	ac, _, err := h.authClient(ctx)
	if err != nil {
		return nil
	}

	resp, err := ac.ListUsers(ctx, &authv1.ListUsersRequest{})
	if err != nil {
		return nil
	}

	var out []templates.SearchResult
	for _, u := range resp.GetUsers() {
		if !matches(q, u.GetUsername(), u.GetId()) {
			continue
		}
		out = append(out, templates.SearchResult{
			Label:   u.GetUsername(),
			Sub:     u.GetId(),
			URL:     "/users",
			Section: "Users",
		})
	}
	return out
}

func (h *Handler) searchEvents(q string) []templates.SearchResult {
	events := h.events.Snapshot()
	var out []templates.SearchResult
	for _, e := range events {
		if !matches(q, e.Type, e.Source) {
			continue
		}
		out = append(out, templates.SearchResult{
			Label:   e.Type,
			Sub:     e.Source,
			URL:     "/events",
			Section: "Events",
		})
		if len(out) >= 10 {
			break
		}
	}
	return out
}

func matches(q, a, b string) bool {
	return strings.Contains(strings.ToLower(a), q) || strings.Contains(strings.ToLower(b), q)
}
