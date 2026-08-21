package handler

import (
	"net/http"

	monitorv1 "github.com/Muxcore-Media/playback-monitor/proto/monitorv1"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

func (h *Handler) StreamsServersPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	data := templates.StreamsServersPageData{}

	client, closer, err := h.withPlaybackMonitorClient(ctx)
	if err != nil {
		data.SoftNote = true
		if h.Core != nil {
			data.Error = err.Error()
		}
		h.renderStreamsServers(w, r, data)
		return
	}
	defer closer()

	resp, err := client.ListServers(ctx, &monitorv1.ListServersRequest{})
	if err != nil {
		data.Error = err.Error()
		h.renderStreamsServers(w, r, data)
		return
	}
	for _, s := range resp.GetServers() {
		data.Servers = append(data.Servers, templates.StreamServerRow{
			ID:             s.GetId(),
			Name:           s.GetName(),
			Type:           s.GetType(),
			SourceModule:   s.GetSourceModule(),
			ActiveSessions: int(s.GetActiveSessions()),
		})
	}
	h.renderStreamsServers(w, r, data)
}

func (h *Handler) renderStreamsServers(w http.ResponseWriter, r *http.Request, data templates.StreamsServersPageData) {
	content := templates.StreamsServersPage(data)
	nav := h.nav("/streams/servers")
	h.render(w, r, templates.Layout("Media servers", nav, content))
}
