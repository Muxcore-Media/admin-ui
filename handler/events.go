package handler

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

type eventRing struct {
	mu     sync.Mutex
	events []templates.EventItem
	cap    int
}

func newEventRing(cap int) *eventRing {
	return &eventRing{cap: cap}
}

func (r *eventRing) Push(e templates.EventItem) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
	if len(r.events) > r.cap {
		r.events = r.events[len(r.events)-r.cap:]
	}
}

func (r *eventRing) Snapshot() []templates.EventItem {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]templates.EventItem, len(r.events))
	copy(out, r.events)
	return out
}

func (h *Handler) startEventSubscription(ctx context.Context) {
	if h.Core == nil {
		return
	}

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			ch, cancel, err := h.Core.Events.Subscribe(ctx, "*")
			if err != nil {
				h.coreConnected = false
				slog.Warn("event subscription failed, retrying in 10s", "error", err)
				select {
				case <-ctx.Done():
					return
				case <-time.After(10 * time.Second):
				}
				continue
			}

			h.coreConnected = true
			slog.Info("event subscription established")
			func() {
				defer cancel()
				for {
					select {
					case <-ctx.Done():
						return
					case ev, ok := <-ch:
						if !ok {
							return
						}
						item := templates.EventItem{
							ID:        ev.GetId(),
							Type:      ev.GetType(),
							Source:    ev.GetSource(),
							Timestamp: time.Unix(ev.GetTimestamp(), 0),
						}
						h.events.Push(item)
					}
				}
			}()
		}
	}()
}

func (h *Handler) EventsPage(w http.ResponseWriter, r *http.Request) {
	filter := normalizeEventFilter(r.URL.Query().Get("filter"))
	events := filterEventItems(h.eventsNewestFirst(), filter)
	stats := h.collectSubscriptionStats()

	content := templates.EventsPage(templates.EventsPageData{
		Events: events,
		Stats:  stats,
		Filter: filter,
	})
	nav := h.nav(r.URL.Path)
	component := templates.Layout("Events", nav, content)
	h.render(w, r, component)
}

func (h *Handler) EventsStream(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	w.Header().Set("Cache-Control", "no-cache")

	filter := normalizeEventFilter(r.URL.Query().Get("filter"))
	events := filterEventItems(h.eventsNewestFirst(), filter)
	component := templates.EventTable(events)
	h.render(w, r, component)
}

func (h *Handler) eventsNewestFirst() []templates.EventItem {
	events := h.events.Snapshot()
	for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
		events[i], events[j] = events[j], events[i]
	}
	return events
}

func normalizeEventFilter(filter string) string {
	switch strings.ToLower(strings.TrimSpace(filter)) {
	case "", "all":
		return "all"
	case "health", "degraded", "download":
		return strings.ToLower(filter)
	default:
		return "all"
	}
}

func filterEventItems(events []templates.EventItem, filter string) []templates.EventItem {
	if filter == "" || filter == "all" {
		return events
	}
	out := make([]templates.EventItem, 0, len(events))
	for _, e := range events {
		switch filter {
		case "health":
			if e.Type == "module.degraded" || e.Type == "module.stale" || strings.HasPrefix(e.Type, "health.") {
				out = append(out, e)
			}
		case "degraded":
			if e.Type == "module.degraded" {
				out = append(out, e)
			}
		case "download":
			if strings.HasPrefix(e.Type, "download.") {
				out = append(out, e)
			}
		}
	}
	return out
}

func (h *Handler) EventStatsPanel(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	w.Header().Set("Cache-Control", "no-cache")

	stats := h.collectSubscriptionStats()
	component := templates.SubscriptionStatsPanel(stats)
	h.render(w, r, component)
}

func (h *Handler) collectSubscriptionStats() []templates.EventSubscriptionStat {
	events := h.events.Snapshot()
	typeCount := make(map[string]int)
	for _, e := range events {
		typeCount[e.Type]++
	}

	stats := make([]templates.EventSubscriptionStat, 0, len(typeCount))
	for typ, count := range typeCount {
		stats = append(stats, templates.EventSubscriptionStat{
			EventType: typ,
			Count:     count,
		})
	}
	return stats
}
