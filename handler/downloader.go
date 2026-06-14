package handler

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"google.golang.org/grpc"

	downloaderv1 "github.com/Muxcore-Media/downloader-native-torrent/proto/downloaderv1"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

const capDownloader = "downloader"

func (h *Handler) downloaderModuleAddr(ctx context.Context) (string, error) {
	mod, err := h.Core.Discovery.Resolve(ctx, "downloader-native-torrent")
	if err != nil {
		mods, err2 := h.Core.Discovery.FindByCapability(ctx, capDownloader)
		if err2 != nil || len(mods) == 0 {
			return "", fmt.Errorf("no downloader module: %w", err)
		}
		return mods[0].GetHttpAddr(), nil
	}
	return mod.GetHttpAddr(), nil
}

func (h *Handler) dialDownloader(ctx context.Context) (*grpc.ClientConn, downloaderv1.TorrentServiceClient, error) {
	addr, err := h.downloaderModuleAddr(ctx)
	if err != nil {
		return nil, nil, err
	}
	if addr == "" {
		return nil, nil, fmt.Errorf("downloader module has no gRPC address")
	}
	conn, err := h.cachedConn(ctx, addr)
	if err != nil {
		return nil, nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	return conn, downloaderv1.NewTorrentServiceClient(conn), nil
}

func (h *Handler) findLeakTestTorrent(ctx context.Context, client downloaderv1.TorrentServiceClient) *downloaderv1.TorrentInfo {
	all, err := client.ListTorrents(ctx, &downloaderv1.ListTorrentsRequest{Filter: "all"})
	if err != nil {
		return nil
	}
	for _, t := range all.GetTorrents() {
		if t.GetLabel() == "ipleak-test" {
			return t
		}
	}
	return nil
}

func (h *Handler) DownloadsPage(w http.ResponseWriter, r *http.Request) {
	filter := r.URL.Query().Get("filter")
	if filter == "" {
		filter = "active"
	}
	ctx := r.Context()

	_, client, err := h.dialDownloader(ctx)
	if err != nil {
		slog.Warn("downloader: dial failed", "error", err)
		content := templates.DownloadsPage(nil, filter, nil, nil, "", nil)
		nav := h.nav(r.URL.Path)
		component := templates.Layout("Downloads", nav, content)
		h.render(w, r, component)
		return
	}

	torrents, err := client.ListTorrents(ctx, &downloaderv1.ListTorrentsRequest{Filter: filter})
	if err != nil {
		slog.Warn("downloader: ListTorrents failed", "error", err)
		content := templates.DownloadsPage(nil, filter, nil, nil, "", nil)
		nav := h.nav(r.URL.Path)
		component := templates.Layout("Downloads", nav, content)
		h.render(w, r, component)
		return
	}

	vpn, _ := client.VpnStatus(ctx, &downloaderv1.VpnStatusRequest{})
	nat, _ := client.NatPmpStatus(ctx, &downloaderv1.NatPmpStatusRequest{})
	leakTorrent := h.findLeakTestTorrent(ctx, client)

	content := templates.DownloadsPage(torrents.GetTorrents(), filter, vpn, nat, "downloader-native-torrent", leakTorrent)
	nav := h.nav(r.URL.Path)
	component := templates.Layout("Downloads", nav, content)
	h.render(w, r, component)
}

func (h *Handler) TorrentList(w http.ResponseWriter, r *http.Request) {
	filter := r.URL.Query().Get("filter")
	if filter == "" {
		filter = "active"
	}
	ctx := r.Context()

	_, client, err := h.dialDownloader(ctx)
	if err != nil {
		w.Write([]byte(`<div class="text-sm text-red-400">Downloader unavailable</div>`))
		return
	}

	torrents, err := client.ListTorrents(ctx, &downloaderv1.ListTorrentsRequest{Filter: filter})
	if err != nil {
		w.Write([]byte(`<div class="text-sm text-red-400">Failed to list torrents</div>`))
		return
	}

	component := templates.TorrentTable(torrents.GetTorrents(), "downloader-native-torrent")
	h.render(w, r, component)
}

func (h *Handler) TorrentAddForm(w http.ResponseWriter, r *http.Request) {
	component := templates.AddTorrentModal()
	h.render(w, r, component)
}

func (h *Handler) TorrentAdd(w http.ResponseWriter, r *http.Request) {
	limitBody(w, r)
	if err := r.ParseForm(); err != nil {
		toast(w, "error", "Invalid form data")
		w.Write([]byte(``))
		return
	}

	uri := r.FormValue("uri")
	if uri == "" {
		toast(w, "error", "URI is required")
		w.Write([]byte(``))
		return
	}

	_, client, err := h.dialDownloader(r.Context())
	if err != nil {
		toast(w, "error", "Downloader unavailable")
		w.Write([]byte(``))
		return
	}

	resp, err := client.AddTorrent(r.Context(), &downloaderv1.AddTorrentRequest{
		Uri:      uri,
		SavePath: r.FormValue("save_path"),
		Label:    r.FormValue("label"),
		Paused:   r.FormValue("paused") == "on",
	})
	if err != nil {
		slog.Warn("downloader: AddTorrent failed", "error", err)
		toast(w, "error", "Add failed: "+err.Error())
		w.Write([]byte(``))
		return
	}

	slog.Info("torrent added", "id", resp.GetId(), "name", resp.GetName())
	h.auditLog(r.Context(), SessionFromContext(r.Context()).Username, "torrent.add", "torrent", resp.GetId(), map[string]string{
		"name": resp.GetName(),
	})

	toast(w, "success", fmt.Sprintf("Added: %s", resp.GetName()))
	w.Write([]byte(``))
}

func (h *Handler) TorrentDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	_, client, err := h.dialDownloader(r.Context())
	if err != nil {
		toast(w, "error", "Downloader unavailable")
		return
	}

	deleteFiles := r.FormValue("delete_files") == "true"
	_, err = client.RemoveTorrent(r.Context(), &downloaderv1.RemoveTorrentRequest{
		Id:          id,
		DeleteFiles: deleteFiles,
	})
	if err != nil {
		slog.Warn("downloader: RemoveTorrent failed", "id", id, "error", err)
		toast(w, "error", "Remove failed")
		return
	}

	h.auditLog(r.Context(), SessionFromContext(r.Context()).Username, "torrent.remove", "torrent", id, nil)
	toast(w, "success", "Torrent removed")
	w.Write([]byte(``))
}

func (h *Handler) TorrentDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ctx := r.Context()

	_, client, err := h.dialDownloader(ctx)
	if err != nil {
		slog.Warn("downloader: dial failed", "error", err)
		nav := h.nav(r.URL.Path)
		content := templates.ErrorPage(503, http.StatusText(http.StatusServiceUnavailable))
		component := templates.Layout("Error", nav, content)
		h.render(w, r, component)
		return
	}

	resp, err := client.GetTorrent(ctx, &downloaderv1.GetTorrentRequest{Id: id})
	if err != nil {
		slog.Warn("downloader: GetTorrent failed", "id", id, "error", err)
		nav := h.nav(r.URL.Path)
		content := templates.ErrorPage(404, "Torrent not found")
		component := templates.Layout("Not Found", nav, content)
		h.render(w, r, component)
		return
	}

	content := templates.TorrentDetailPage(resp.GetTorrent(), "downloader-native-torrent")
	nav := h.nav(r.URL.Path)
	component := templates.Layout(resp.GetTorrent().GetName(), nav, content)
	h.render(w, r, component)
}

func (h *Handler) VpnPanel(w http.ResponseWriter, r *http.Request) {
	_, client, err := h.dialDownloader(r.Context())
	if err != nil {
		w.Write([]byte(`<div class="text-sm text-red-400">Downloader unavailable</div>`))
		return
	}

	vpn, _ := client.VpnStatus(r.Context(), &downloaderv1.VpnStatusRequest{})
	nat, _ := client.NatPmpStatus(r.Context(), &downloaderv1.NatPmpStatusRequest{})

	component := templates.VpnStatusPanel(vpn, "downloader-native-torrent")
	h.render(w, r, component)
	component2 := templates.NatPmpStatusPanel(nat)
	h.render(w, r, component2)
}

func (h *Handler) VpnStart(w http.ResponseWriter, r *http.Request) {
	_, client, err := h.dialDownloader(r.Context())
	if err != nil {
		w.Write([]byte(`<div class="text-sm text-red-400">Downloader unavailable</div>`))
		return
	}

	resp, err := client.VpnStart(r.Context(), &downloaderv1.VpnStartRequest{
		ConfigFile:        r.FormValue("config_file"),
		EnableKillSwitch:  r.FormValue("kill_switch") == "true",
		TorrentListenPort: parseInt32(r.FormValue("listen_port"), 6881),
		EnableNatPmp:      r.FormValue("nat_pmp") == "true",
	})
	if err != nil {
		toast(w, "error", "VPN start failed: "+err.Error())
		return
	}

	if resp.GetStarted() {
		toast(w, "success", fmt.Sprintf("VPN started on %s (%s)", resp.GetInterfaceName(), resp.GetLocalIp()))
	} else if resp.GetError() != "" {
		toast(w, "error", "VPN start failed: "+resp.GetError())
	}

	redirectToDownloadsPage(w, r)
}

func (h *Handler) VpnStop(w http.ResponseWriter, r *http.Request) {
	_, client, err := h.dialDownloader(r.Context())
	if err != nil {
		w.Write([]byte(`<div class="text-sm text-red-400">Downloader unavailable</div>`))
		return
	}

	_, err = client.VpnStop(r.Context(), &downloaderv1.VpnStopRequest{})
	if err != nil {
		toast(w, "error", "VPN stop failed: "+err.Error())
		return
	}

	toast(w, "success", "VPN stopped")
	redirectToDownloadsPage(w, r)
}

func (h *Handler) IpLeakTestStart(w http.ResponseWriter, r *http.Request) {
	limitBody(w, r)
	if err := r.ParseForm(); err != nil {
		toast(w, "error", "Invalid form data")
		renderIpLeakPanel(w, r, nil, nil, "")
		return
	}

	uri := r.FormValue("uri")
	if uri == "" {
		toast(w, "error", "Magnet URI is required")
		renderIpLeakPanel(w, r, nil, nil, "")
		return
	}

	ctx := r.Context()
	_, client, err := h.dialDownloader(ctx)
	if err != nil {
		toast(w, "error", "Downloader unavailable")
		renderIpLeakPanel(w, r, nil, nil, "")
		return
	}

	resp, err := client.AddTorrent(ctx, &downloaderv1.AddTorrentRequest{
		Uri:   uri,
		Label: "ipleak-test",
	})
	if err != nil {
		toast(w, "error", "Leak test failed: "+err.Error())
		renderIpLeakPanel(w, r, nil, nil, "")
		return
	}

	slog.Info("ipleak test: added torrent", "id", resp.GetId(), "name", resp.GetName())
	h.auditLog(ctx, SessionFromContext(ctx).Username, "ipleak.start", "torrent", resp.GetId(), map[string]string{
		"name": resp.GetName(),
	})

	vpn, _ := client.VpnStatus(ctx, &downloaderv1.VpnStatusRequest{})
	leakTorrent := h.findLeakTestTorrent(ctx, client)
	toast(w, "success", "Leak test started. Check ipleak.net to verify your IP.")
	renderIpLeakPanel(w, r, vpn, leakTorrent, "downloader-native-torrent")
}

func (h *Handler) IpLeakTestRemove(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	_, client, err := h.dialDownloader(ctx)
	if err != nil {
		toast(w, "error", "Downloader unavailable")
		renderIpLeakPanel(w, r, nil, nil, "")
		return
	}

	leakTorrent := h.findLeakTestTorrent(ctx, client)
	if leakTorrent != nil {
		_, err = client.RemoveTorrent(ctx, &downloaderv1.RemoveTorrentRequest{
			Id:          leakTorrent.GetId(),
			DeleteFiles: true,
		})
		if err != nil {
			slog.Warn("ipleak: RemoveTorrent failed", "id", leakTorrent.GetId(), "error", err)
			toast(w, "error", "Failed to remove test torrent")
			vpn, _ := client.VpnStatus(ctx, &downloaderv1.VpnStatusRequest{})
			renderIpLeakPanel(w, r, vpn, leakTorrent, "downloader-native-torrent")
			return
		}
		h.auditLog(ctx, SessionFromContext(ctx).Username, "ipleak.remove", "torrent", leakTorrent.GetId(), nil)
	}

	vpn, _ := client.VpnStatus(ctx, &downloaderv1.VpnStatusRequest{})
	toast(w, "success", "Test torrent removed")
	renderIpLeakPanel(w, r, vpn, nil, "downloader-native-torrent")
}

func renderIpLeakPanel(w http.ResponseWriter, r *http.Request, vpn *downloaderv1.VpnStatusResponse, leakTorrent *downloaderv1.TorrentInfo, moduleID string) {
	component := templates.IpLeakTestPanel(vpn, leakTorrent, moduleID)
	if err := component.Render(r.Context(), w); err != nil {
		slog.Error("ipleak: template render failed", "error", err)
	}
}

func parseInt32(s string, def int32) int32 {
	if s == "" {
		return def
	}
	v, err := strconv.ParseInt(s, 10, 32)
	if err != nil {
		return def
	}
	return int32(v)
}

func redirectToDownloadsPage(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", "/downloads")
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, "/downloads", http.StatusSeeOther)
}
