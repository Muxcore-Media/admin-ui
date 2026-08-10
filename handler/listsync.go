package handler

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	listsyncv1 "github.com/Muxcore-Media/media-list-sync/proto/listsyncv1"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

const capMediaListSync = "media.listsync"

func (h *Handler) listSyncModuleAddr(ctx context.Context) (string, error) {
	if h.Core == nil {
		return "", fmt.Errorf("core unavailable")
	}
	mods, err := h.Core.Discovery.FindByCapability(ctx, capMediaListSync)
	if err != nil {
		return "", err
	}
	if len(mods) == 0 {
		return "", fmt.Errorf("no module with capability %s", capMediaListSync)
	}
	addr := normalizeDialAddr(mods[0].GetId(), mods[0].GetHttpAddr())
	if addr == "" {
		return "", fmt.Errorf("list-sync module has no dial address")
	}
	return addr, nil
}

func (h *Handler) withListSyncClient(ctx context.Context) (listsyncv1.ListSyncServiceClient, func(), error) {
	addr, err := h.listSyncModuleAddr(ctx)
	if err != nil {
		return nil, nil, err
	}
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	return listsyncv1.NewListSyncServiceClient(conn), func() { _ = conn.Close() }, nil
}

func (h *Handler) ListSyncPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	data := templates.ListSyncPageData{
		Flash: r.URL.Query().Get("synced"),
		Error: r.URL.Query().Get("error"),
	}
	if flash := r.URL.Query().Get("ok"); flash != "" {
		data.Flash = flash
	}

	client, closer, err := h.withListSyncClient(ctx)
	if err != nil {
		slog.Warn("list-sync: resolve failed", "error", err)
		data.Error = err.Error()
		h.renderListSync(w, r, data)
		return
	}
	defer closer()

	resp, err := client.ListSources(ctx, &listsyncv1.ListSourcesRequest{})
	if err != nil {
		slog.Warn("list-sync: ListSources failed", "error", err)
		data.Error = err.Error()
		h.renderListSync(w, r, data)
		return
	}
	for _, s := range resp.GetSources() {
		data.Sources = append(data.Sources, templates.ListSyncSourceRow{
			ID:          s.GetId(),
			Name:        s.GetName(),
			Type:        s.GetType(),
			Enabled:     s.GetEnabled(),
			Username:    s.GetUsername(),
			ListURL:     s.GetListUrl(),
			IntervalMin: int(s.GetSyncIntervalMinutes()),
			LastSynced:  s.GetLastSynced(),
			BaseURL:     s.GetBaseUrl(),
		})
	}
	h.renderListSync(w, r, data)
}

func (h *Handler) renderListSync(w http.ResponseWriter, r *http.Request, data templates.ListSyncPageData) {
	content := templates.ListSyncPage(data)
	nav := h.nav(r.URL.Path)
	component := templates.Layout("List Sync", nav, content)
	h.render(w, r, component)
}

func (h *Handler) ListSyncHistoryPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	data := templates.ListSyncHistoryPageData{}

	client, closer, err := h.withListSyncClient(ctx)
	if err != nil {
		data.Error = err.Error()
		h.renderListSyncHistory(w, r, data)
		return
	}
	defer closer()

	resp, err := client.GetHistory(ctx, &listsyncv1.GetHistoryRequest{Page: 1, PageSize: 50})
	if err != nil {
		slog.Warn("list-sync: GetHistory failed", "error", err)
		data.Error = err.Error()
		h.renderListSyncHistory(w, r, data)
		return
	}
	data.Total = int(resp.GetTotal())
	for _, e := range resp.GetEntries() {
		data.Entries = append(data.Entries, templates.ListSyncHistoryRow{
			ID:          e.GetId(),
			SourceName:  e.GetSourceName(),
			Status:      e.GetStatus(),
			ItemsFound:  int(e.GetItemsFound()),
			ItemsNew:    int(e.GetItemsNew()),
			Error:       e.GetError(),
			StartedAt:   e.GetStartedAt(),
			CompletedAt: e.GetCompletedAt(),
		})
	}
	h.renderListSyncHistory(w, r, data)
}

func (h *Handler) renderListSyncHistory(w http.ResponseWriter, r *http.Request, data templates.ListSyncHistoryPageData) {
	content := templates.ListSyncHistoryPage(data)
	nav := h.nav("/list-sync")
	component := templates.Layout("List Sync History", nav, content)
	h.render(w, r, component)
}

func (h *Handler) ListSyncNow(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	client, closer, err := h.withListSyncClient(ctx)
	if err != nil {
		http.Redirect(w, r, "/list-sync?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer closer()

	resp, err := client.SyncNow(ctx, &listsyncv1.SyncNowRequest{})
	if err != nil {
		http.Redirect(w, r, "/list-sync?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	msg := fmt.Sprintf("Sync complete: %d found, %d new", resp.GetItemsFound(), resp.GetItemsNew())
	http.Redirect(w, r, "/list-sync?synced="+url.QueryEscape(msg), http.StatusSeeOther)
}

func (h *Handler) ListSyncAddSource(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/list-sync?error="+url.QueryEscape("invalid form"), http.StatusSeeOther)
		return
	}
	interval, _ := strconv.Atoi(strings.TrimSpace(r.FormValue("sync_interval_minutes")))
	if interval <= 0 {
		interval = 60
	}

	ctx := r.Context()
	client, closer, err := h.withListSyncClient(ctx)
	if err != nil {
		http.Redirect(w, r, "/list-sync?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer closer()

	_, err = client.AddSource(ctx, &listsyncv1.AddSourceRequest{
		Name:                 strings.TrimSpace(r.FormValue("name")),
		Type:                 strings.TrimSpace(r.FormValue("type")),
		Username:             strings.TrimSpace(r.FormValue("username")),
		ClientId:             strings.TrimSpace(r.FormValue("client_id")),
		ListUrl:              strings.TrimSpace(r.FormValue("list_url")),
		SyncIntervalMinutes:  int32(interval),
		BaseUrl:              strings.TrimSpace(r.FormValue("base_url")),
		ApiKey:               strings.TrimSpace(r.FormValue("api_key")),
		QualityProfileId:     strings.TrimSpace(r.FormValue("quality_profile_id")),
		RootFolderPath:       strings.TrimSpace(r.FormValue("root_folder_path")),
	})
	if err != nil {
		http.Redirect(w, r, "/list-sync?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/list-sync?ok="+url.QueryEscape("Source added"), http.StatusSeeOther)
}

func (h *Handler) ListSyncRemoveSource(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Redirect(w, r, "/list-sync?error="+url.QueryEscape("missing source id"), http.StatusSeeOther)
		return
	}
	ctx := r.Context()
	client, closer, err := h.withListSyncClient(ctx)
	if err != nil {
		http.Redirect(w, r, "/list-sync?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer closer()

	_, err = client.RemoveSource(ctx, &listsyncv1.RemoveSourceRequest{Id: id})
	if err != nil {
		http.Redirect(w, r, "/list-sync?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/list-sync?ok="+url.QueryEscape("Source removed"), http.StatusSeeOther)
}
