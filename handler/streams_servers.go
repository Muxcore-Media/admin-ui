package handler

import (
	"context"
	"net/http"
	"time"

	monitorv1 "github.com/Muxcore-Media/playback-monitor/proto/monitorv1"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

const (
	streamsServersPageTimeout = playbackMonitorDialTimeout + playbackMonitorReadTimeout + time.Second
)

func (h *Handler) StreamsServersPage(w http.ResponseWriter, r *http.Request) {
	pageCtx, pageCancel := context.WithTimeout(r.Context(), streamsServersPageTimeout)
	defer pageCancel()

	data := templates.StreamsServersPageData{}

	dialCtx, dialCancel := context.WithTimeout(pageCtx, playbackMonitorDialTimeout)
	client, closer, err := h.withPlaybackMonitorClient(dialCtx)
	dialCancel()
	if err != nil {
		data.SoftNote = true
		if h.Core != nil {
			data.Error = err.Error()
		}
		h.renderStreamsServers(w, r, data)
		return
	}
	defer closer()

	readCtx, readCancel := context.WithTimeout(pageCtx, playbackMonitorReadTimeout)
	resp, err := client.ListServers(readCtx, &monitorv1.ListServersRequest{})
	readCancel()
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
