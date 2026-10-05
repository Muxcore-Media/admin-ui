package handler

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/Muxcore-Media/admin-ui/internal/meshdial"

	jellyfinv1 "github.com/Muxcore-Media/jellyfin/proto/jellyfinv1"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

const (
	capPlaybackJellyfin   = "playback.jellyfin"
	jellyfinDialTimeout   = 3 * time.Second
	jellyfinReadTimeout   = 5 * time.Second
	jellyfinPageTimeout   = jellyfinDialTimeout + 2*jellyfinReadTimeout + time.Second
	jellyfinActionTimeout = jellyfinDialTimeout + jellyfinReadTimeout + time.Second
)

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
	pageCtx, pageCancel := context.WithTimeout(r.Context(), jellyfinPageTimeout)
	defer pageCancel()

	data := templates.JellyfinPageData{
		Flash: r.URL.Query().Get("synced"),
		Error: r.URL.Query().Get("error"),
	}
	if refreshed := r.URL.Query().Get("refreshed"); refreshed != "" {
		data.Flash = refreshed
		data.RefreshStatus = refreshed
	} else {
		data.RefreshStatus = "Ready — Refresh JF library asks Jellyfin to rescan; Sync library rebuilds MuxCore item links."
	}

	dialCtx, dialCancel := context.WithTimeout(pageCtx, jellyfinDialTimeout)
	_, addr, err := h.jellyfinModuleAddr(dialCtx)
	dialCancel()
	if err != nil {
		slog.Warn("jellyfin: resolve failed", "error", err)
		data.Error = err.Error()
		h.renderJellyfin(w, r, data)
		return
	}
	conn, err := meshdial.NewClient(addr)
	if err != nil {
		data.Error = fmt.Sprintf("dial %s: %v", addr, err)
		h.renderJellyfin(w, r, data)
		return
	}
	defer func() { _ = conn.Close() }()

	client := jellyfinv1.NewJellyfinBridgeClient(conn)
	readCtx, readCancel := context.WithTimeout(pageCtx, jellyfinReadTimeout)
	st, err := client.Status(readCtx, &jellyfinv1.StatusRequest{})
	readCancel()
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

	readCtx, readCancel = context.WithTimeout(pageCtx, jellyfinReadTimeout)
	list, err := client.ListItemLinks(readCtx, &jellyfinv1.ListItemLinksRequest{})
	readCancel()
	if err != nil {
		slog.Warn("jellyfin: ListItemLinks failed", "error", err)
	} else {
		for i, link := range list.GetLinks() {
			if i >= 25 {
				break
			}
			item := templates.JellyfinLinkItem{
				MuxID: link.GetMuxcoreId(),
				JFID:  link.GetJellyfinId(),
				Title: link.GetTitle(),
				Path:  link.GetPath(),
			}
			if data.BaseURL != "" && item.JFID != "" {
				item.PlayURL = fmt.Sprintf("%s/web/index.html#!/details?id=%s", data.BaseURL, item.JFID)
			}
			data.Links = append(data.Links, item)
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
	pageCtx, pageCancel := context.WithTimeout(r.Context(), jellyfinActionTimeout)
	defer pageCancel()

	dialCtx, dialCancel := context.WithTimeout(pageCtx, jellyfinDialTimeout)
	_, addr, err := h.jellyfinModuleAddr(dialCtx)
	dialCancel()
	if err != nil {
		http.Redirect(w, r, "/jellyfin?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	conn, err := meshdial.NewClient(addr)
	if err != nil {
		http.Redirect(w, r, "/jellyfin?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer func() { _ = conn.Close() }()
	client := jellyfinv1.NewJellyfinBridgeClient(conn)
	readCtx, readCancel := context.WithTimeout(pageCtx, jellyfinReadTimeout)
	resp, err := client.SyncLibrary(readCtx, &jellyfinv1.SyncLibraryRequest{Direction: "both"})
	readCancel()
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

// JellyfinRefresh asks the live Jellyfin server to rescan its library.
func (h *Handler) JellyfinRefresh(w http.ResponseWriter, r *http.Request) {
	pageCtx, pageCancel := context.WithTimeout(r.Context(), jellyfinActionTimeout)
	defer pageCancel()

	dialCtx, dialCancel := context.WithTimeout(pageCtx, jellyfinDialTimeout)
	_, addr, err := h.jellyfinModuleAddr(dialCtx)
	dialCancel()
	if err != nil {
		http.Redirect(w, r, "/jellyfin?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	conn, err := meshdial.NewClient(addr)
	if err != nil {
		http.Redirect(w, r, "/jellyfin?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer func() { _ = conn.Close() }()
	client := jellyfinv1.NewJellyfinBridgeClient(conn)
	readCtx, readCancel := context.WithTimeout(pageCtx, jellyfinReadTimeout)
	resp, err := client.RefreshLibrary(readCtx, &jellyfinv1.RefreshLibraryRequest{})
	readCancel()
	if err != nil {
		http.Redirect(w, r, "/jellyfin?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	msg := "Refresh JF library requested"
	if resp.GetOk() {
		msg = "Refresh JF library ok"
	}
	http.Redirect(w, r, "/jellyfin?refreshed="+url.QueryEscape(msg), http.StatusSeeOther)
}
