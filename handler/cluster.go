package handler

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	discoveryv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/discovery/v1"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

const (
	clusterDialTimeout = 3 * time.Second
	clusterReadTimeout = 5 * time.Second
	clusterPageTimeout = clusterDialTimeout + clusterReadTimeout + time.Second
)

func (h *Handler) ClusterPage(w http.ResponseWriter, r *http.Request) {
	pageCtx, pageCancel := context.WithTimeout(r.Context(), clusterPageTimeout)
	defer pageCancel()

	dialCtx, dialCancel := context.WithTimeout(pageCtx, clusterDialTimeout)
	members, leaderID, err := h.Core.Discovery.Members(dialCtx)
	dialCancel()
	if err != nil {
		slog.Warn("cluster: Members call failed", "error", err)
		nav := h.nav(r.URL.Path)
		content := templates.ClusterPage(templates.ClusterPageData{})
		component := templates.Layout("Cluster", nav, content)
		h.render(w, r, component)
		return
	}

	nodes := make([]templates.NodeCardData, 0, len(members))
	for _, m := range members {
		modules := make([]templates.NodeModuleEntry, 0, len(m.GetModules()))
		for _, modID := range m.GetModules() {
			healthErr := m.GetModuleHealth()[modID]
			modules = append(modules, templates.NodeModuleEntry{
				ID:      modID,
				Healthy: healthErr == "",
			})
		}

		nodes = append(nodes, templates.NodeCardData{
			ID:          m.GetId(),
			GRPCAddr:    m.GetGrpcAddr(),
			HTTPAddr:    m.GetHttpAddr(),
			IsLeader:    m.GetId() == leaderID,
			ModuleCount: len(modules),
			Modules:     modules,
		})
	}

	data := templates.ClusterPageData{Nodes: nodes}
	content := templates.ClusterPage(data)
	nav := h.nav(r.URL.Path)
	component := templates.Layout("Cluster", nav, content)
	h.render(w, r, component)
}

func (h *Handler) ClusterNodes(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	w.Header().Set("Cache-Control", "no-cache")

	pageCtx, pageCancel := context.WithTimeout(r.Context(), clusterPageTimeout)
	defer pageCancel()

	dialCtx, dialCancel := context.WithTimeout(pageCtx, clusterDialTimeout)
	members, leaderID, err := h.Core.Discovery.Members(dialCtx)
	dialCancel()
	if err != nil {
		slog.Warn("cluster nodes: Members call failed", "error", err)
		w.Write([]byte(`<div class="col-span-full text-sm text-red-400">Failed to load nodes</div>`))
		return
	}

	for _, m := range members {
		modules := make([]templates.NodeModuleEntry, 0, len(m.GetModules()))
		for _, modID := range m.GetModules() {
			healthErr := m.GetModuleHealth()[modID]
			modules = append(modules, templates.NodeModuleEntry{
				ID:      modID,
				Healthy: healthErr == "",
			})
		}

		card := templates.NodeCard(templates.NodeCardData{
			ID:          m.GetId(),
			GRPCAddr:    m.GetGrpcAddr(),
			HTTPAddr:    m.GetHttpAddr(),
			IsLeader:    m.GetId() == leaderID,
			ModuleCount: len(modules),
			Modules:     modules,
		})
		h.render(w, r, card)
	}
}

// ClusterSSE streams cluster membership events via Server-Sent Events.
// The client uses hx-trigger="sse:cluster-update" to re-fetch /cluster/nodes.
func (h *Handler) ClusterSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ctx := r.Context()

	if h.Core == nil {
		http.Error(w, "core unavailable", http.StatusServiceUnavailable)
		return
	}
	stream, err := h.Core.Discovery.Raw().Watch(ctx, &discoveryv1.MembersRequest{})
	if err != nil {
		slog.Warn("cluster sse: Watch failed", "error", err)
		http.Error(w, "cluster watch unavailable", http.StatusInternalServerError)
		return
	}

	// Send an immediate event to trigger the initial render
	fmt.Fprintf(w, "event: cluster-update\ndata:\n\n")
	flusher.Flush()

	for {
		_, err := stream.Recv()
		if err != nil {
			slog.Debug("cluster sse: stream ended", "error", err)
			return
		}

		// Debounce: wait briefly for more events before signaling
		select {
		case <-time.After(100 * time.Millisecond):
		case <-ctx.Done():
			return
		}

		fmt.Fprintf(w, "event: cluster-update\ndata:\n\n")
		flusher.Flush()
	}
}
