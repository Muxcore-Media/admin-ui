package handler

import (
	"encoding/json"
	"net/http"

	monitorv1 "github.com/Muxcore-Media/playback-monitor/proto/monitorv1"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

type streamMapPin struct {
	ID      string  `json:"id"`
	User    string  `json:"user"`
	Title   string  `json:"title"`
	State   string  `json:"state"`
	IP      string  `json:"ip"`
	City    string  `json:"city"`
	Country string  `json:"country"`
	Lat     float64 `json:"lat"`
	Lon     float64 `json:"lon"`
}

func (h *Handler) StreamsMapPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	data := templates.StreamsMapPageData{}

	client, closer, err := h.withPlaybackMonitorClient(ctx)
	if err != nil {
		data.SoftNote = true
		if h.Core != nil {
			data.Error = err.Error()
		}
		h.renderStreamsMap(w, r, data)
		return
	}
	defer closer()

	if active, err := client.ListActiveSessions(ctx, &monitorv1.ListActiveSessionsRequest{Limit: 200}); err == nil {
		data.PinCount = len(mapPinsFromSessions(active.GetSessions()))
	}
	h.renderStreamsMap(w, r, data)
}

func (h *Handler) StreamsMapData(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	client, closer, err := h.withPlaybackMonitorClient(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer closer()

	resp, err := client.ListActiveSessions(ctx, &monitorv1.ListActiveSessionsRequest{Limit: 200})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	pins := mapPinsFromSessions(resp.GetSessions())
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"pins": pins})
}

func mapPinsFromSessions(sessions []*monitorv1.SessionRecord) []streamMapPin {
	out := make([]streamMapPin, 0)
	for _, s := range sessions {
		if s == nil {
			continue
		}
		lat, lon := s.GetGeoLat(), s.GetGeoLon()
		if lat == 0 && lon == 0 {
			continue
		}
		if s.GetGeoCountry() == "Local Network" {
			continue
		}
		user := s.GetUserName()
		if user == "" {
			user = s.GetUserId()
		}
		out = append(out, streamMapPin{
			ID:      s.GetId(),
			User:    user,
			Title:   s.GetTitle(),
			State:   protoSessionStateLabel(s.GetState()),
			IP:      s.GetIpAddress(),
			City:    s.GetGeoCity(),
			Country: s.GetGeoCountry(),
			Lat:     lat,
			Lon:     lon,
		})
	}
	return out
}

func (h *Handler) renderStreamsMap(w http.ResponseWriter, r *http.Request, data templates.StreamsMapPageData) {
	content := templates.StreamsMapPage(data)
	nav := h.nav("/streams/map")
	h.render(w, r, templates.Layout("Stream map", nav, content))
}
