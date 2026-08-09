package handler

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	jellyfinv1 "github.com/Muxcore-Media/jellyfin/proto/jellyfinv1"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

const capPlaybackJellyfin = "playback.jellyfin"

func (h *Handler) jellyfinModuleAddr(ctx context.Context) (string, string, error) {
	if h.Core == nil {
		return "", "", fmt.Errorf("core unavailable")
	}
	mods, err := h.Core.Discovery.FindByCapability(ctx, capPlaybackJellyfin)
	if err != nil {
		return "", "", err
	}
	if len(mods) == 0 {
		return "", "", fmt.Errorf("no module with capability %s", capPlaybackJellyfin)
	}
	mod := mods[0]
	addr := normalizeDialAddr(mod.GetId(), mod.GetHttpAddr())
	if addr == "" {
		return "", "", fmt.Errorf("jellyfin module has no dial address")
	}
	return mod.GetId(), addr, nil
}

func (h *Handler) JellyfinStatusPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	data := templates.JellyfinPageData{
		Flash: r.URL.Query().Get("synced"),
		Error: r.URL.Query().Get("error"),
	}

	_, addr, err := h.jellyfinModuleAddr(ctx)
	if err != nil {
		slog.Warn("jellyfin: resolve failed", "error", err)
		data.Error = err.Error()
		h.renderJellyfin(w, r, data)
		return
	}
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		data.Error = fmt.Sprintf("dial %s: %v", addr, err)
		h.renderJellyfin(w, r, data)
		return
	}
	defer conn.Close()

	client := jellyfinv1.NewJellyfinBridgeClient(conn)
	st, err := client.Status(ctx, &jellyfinv1.StatusRequest{})
	if err != nil {
		slog.Warn("jellyfin: Status failed", "error", err)
		data.Error = err.Error()
		h.renderJellyfin(w, r, data)
		return
	}
	data.Configured = st.GetConfigured()
	data.BaseURL = st.GetBaseUrl()
	data.ConflictMode = st.GetConflictMode()
	data.ItemLinks = int(st.GetItemLinks())
	data.SessionsPoll = st.GetSessionsPollEnabled()
	data.SoftNote = !st.GetConfigured()

	list, err := client.ListItemLinks(ctx, &jellyfinv1.ListItemLinksRequest{})
	if err != nil {
		slog.Warn("jellyfin: ListItemLinks failed", "error", err)
	} else {
		for i, link := range list.GetLinks() {
			if i >= 25 {
				break
			}
			data.Links = append(data.Links, templates.JellyfinLinkItem{
				MuxID:  link.GetMuxcoreId(),
				JFID:   link.GetJellyfinId(),
				Title:  link.GetTitle(),
				Path:   link.GetPath(),
			})
		}
	}

	h.renderJellyfin(w, r, data)
}

func (h *Handler) renderJellyfin(w http.ResponseWriter, r *http.Request, data templates.JellyfinPageData) {
	content := templates.JellyfinPage(data)
	nav := h.nav(r.URL.Path)
	component := templates.Layout("Jellyfin", nav, content)
	h.render(w, r, component)
}

// JellyfinSync soft-calls SyncLibrary (empty skip when unconfigured).
func (h *Handler) JellyfinSync(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	_, addr, err := h.jellyfinModuleAddr(ctx)
	if err != nil {
		http.Redirect(w, r, "/jellyfin?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		http.Redirect(w, r, "/jellyfin?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer conn.Close()
	client := jellyfinv1.NewJellyfinBridgeClient(conn)
	resp, err := client.SyncLibrary(ctx, &jellyfinv1.SyncLibraryRequest{Direction: "both"})
	if err != nil {
		http.Redirect(w, r, "/jellyfin?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	msg := fmt.Sprintf("scanned=%d upserted=%d", resp.GetScanned(), resp.GetUpserted())
	if len(resp.GetErrors()) > 0 {
		msg += " note=" + resp.GetErrors()[0]
	}
	http.Redirect(w, r, "/jellyfin?synced="+url.QueryEscape(msg), http.StatusSeeOther)
}
