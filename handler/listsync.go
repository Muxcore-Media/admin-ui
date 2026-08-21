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

func sourceRowFromProto(s *listsyncv1.ListSource) templates.ListSyncSourceRow {
	return templates.ListSyncSourceRow{
		ID:               s.GetId(),
		Name:             s.GetName(),
		Type:             s.GetType(),
		Enabled:          s.GetEnabled(),
		Username:         s.GetUsername(),
		ClientID:         s.GetClientId(),
		ListURL:          s.GetListUrl(),
		IntervalMin:      int(s.GetSyncIntervalMinutes()),
		LastSynced:       s.GetLastSynced(),
		BaseURL:          s.GetBaseUrl(),
		QualityProfileID: s.GetQualityProfileId(),
		RootFolderPath:   s.GetRootFolderPath(),
		CleanLibraryLevel:   s.GetCleanLibraryLevel(),
		TagIDs:              s.GetTagIds(),
		MonitorMode:         s.GetMonitorMode(),
		MinimumAvailability: s.GetMinimumAvailability(),
		SearchOnAdd:         s.GetSearchOnAdd(),
	}
}

func (h *Handler) listSyncFindSource(ctx context.Context, client listsyncv1.ListSyncServiceClient, id string) (*listsyncv1.ListSource, error) {
	resp, err := client.ListSources(ctx, &listsyncv1.ListSourcesRequest{})
	if err != nil {
		return nil, err
	}
	for _, s := range resp.GetSources() {
		if s.GetId() == id {
			return s, nil
		}
	}
	return nil, fmt.Errorf("source not found")
}

func parseSearchOnAddForm(r *http.Request) *bool {
	v := r.FormValue("search_on_add") == "1"
	return boolPtr(v)
}

func parseListSyncSourceForm(r *http.Request, existing *listsyncv1.ListSource) *listsyncv1.UpdateSourceRequest {
	interval, _ := strconv.Atoi(strings.TrimSpace(r.FormValue("sync_interval_minutes")))
	if interval <= 0 {
		interval = 60
	}
	req := &listsyncv1.UpdateSourceRequest{
		Name:                strings.TrimSpace(r.FormValue("name")),
		Username:            strings.TrimSpace(r.FormValue("username")),
		ClientId:            strings.TrimSpace(r.FormValue("client_id")),
		ListUrl:             strings.TrimSpace(r.FormValue("list_url")),
		SyncIntervalMinutes: int32(interval),
		BaseUrl:             strings.TrimSpace(r.FormValue("base_url")),
		ApiKey:              strings.TrimSpace(r.FormValue("api_key")),
		QualityProfileId:    strings.TrimSpace(r.FormValue("quality_profile_id")),
		RootFolderPath:      strings.TrimSpace(r.FormValue("root_folder_path")),
		CleanLibraryLevel:   strings.TrimSpace(r.FormValue("clean_library_level")),
		TagIds:              strings.TrimSpace(r.FormValue("tag_ids")),
		MonitorMode:         strings.TrimSpace(r.FormValue("monitor_mode")),
		MinimumAvailability: strings.TrimSpace(r.FormValue("minimum_availability")),
		SearchOnAdd:         parseSearchOnAddForm(r),
	}
	if existing != nil {
		req.Id = existing.GetId()
		enabled := r.FormValue("enabled") == "1"
		req.Enabled = boolPtr(enabled)
	}
	return req
}

func boolPtr(v bool) *bool { return &v }

func (h *Handler) listSyncFormOptions(ctx context.Context) ([]templates.ProfileOption, []templates.RootOption) {
	profiles := h.listProfileOptions(ctx)
	roots := h.listRootOptions(ctx, "")
	return profiles, roots
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
		data.Sources = append(data.Sources, sourceRowFromProto(s))
	}
	data.Profiles, data.Roots = h.listSyncFormOptions(ctx)
	h.renderListSync(w, r, data)
}

func (h *Handler) renderListSync(w http.ResponseWriter, r *http.Request, data templates.ListSyncPageData) {
	content := templates.ListSyncPage(data)
	nav := h.nav(r.URL.Path)
	component := templates.Layout("List Sync", nav, content)
	h.render(w, r, component)
}

func (h *Handler) ListSyncEditPage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ctx := r.Context()
	data := templates.ListSyncEditPageData{
		Flash:       r.URL.Query().Get("ok"),
		Error:       r.URL.Query().Get("error"),
		TestMessage: r.URL.Query().Get("test"),
		TestOK:      r.URL.Query().Get("test_ok") == "1",
	}

	client, closer, err := h.withListSyncClient(ctx)
	if err != nil {
		data.Error = err.Error()
		h.renderListSyncEdit(w, r, data)
		return
	}
	defer closer()

	src, err := h.listSyncFindSource(ctx, client, id)
	if err != nil {
		data.Error = err.Error()
		h.renderListSyncEdit(w, r, data)
		return
	}
	data.Source = sourceRowFromProto(src)
	data.Profiles, data.Roots = h.listSyncFormOptions(ctx)
	h.renderListSyncEdit(w, r, data)
}

func (h *Handler) renderListSyncEdit(w http.ResponseWriter, r *http.Request, data templates.ListSyncEditPageData) {
	content := templates.ListSyncEditPage(data)
	nav := h.nav("/list-sync")
	component := templates.Layout("Edit List Source", nav, content)
	h.render(w, r, component)
}

func (h *Handler) ListSyncItemsPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	data := templates.ListSyncItemsPageData{}

	client, closer, err := h.withListSyncClient(ctx)
	if err != nil {
		data.Error = err.Error()
		h.renderListSyncItems(w, r, data)
		return
	}
	defer closer()

	sourcesResp, _ := client.ListSources(ctx, &listsyncv1.ListSourcesRequest{})
	sourceNames := map[string]string{}
	for _, s := range sourcesResp.GetSources() {
		sourceNames[s.GetId()] = s.GetName()
	}

	resp, err := client.GetItems(ctx, &listsyncv1.GetItemsRequest{Page: 1, PageSize: 100})
	if err != nil {
		data.Error = err.Error()
		h.renderListSyncItems(w, r, data)
		return
	}
	data.Total = int(resp.GetTotal())
	for _, item := range resp.GetItems() {
		data.Items = append(data.Items, templates.ListSyncItemRow{
			ID:            item.GetId(),
			SourceName:    sourceNames[item.GetSourceId()],
			ExternalID:    item.GetExternalId(),
			Title:         item.GetTitle(),
			MediaType:     item.GetMediaType(),
			Year:          int(item.GetYear()),
			Status:        item.GetStatus(),
			MatchedItemID: item.GetMatchedItemId(),
		})
	}
	h.renderListSyncItems(w, r, data)
}

func (h *Handler) renderListSyncItems(w http.ResponseWriter, r *http.Request, data templates.ListSyncItemsPageData) {
	content := templates.ListSyncItemsPage(data)
	nav := h.nav("/list-sync")
	component := templates.Layout("List Sync Items", nav, content)
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
			ItemsRemoved: int(e.GetItemsRemoved()),
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
	msg := fmt.Sprintf("Sync complete: %d found, %d new, %d removed", resp.GetItemsFound(), resp.GetItemsNew(), resp.GetItemsRemoved())
	http.Redirect(w, r, "/list-sync?synced="+url.QueryEscape(msg), http.StatusSeeOther)
}

func (h *Handler) ListSyncSourceNow(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ctx := r.Context()
	client, closer, err := h.withListSyncClient(ctx)
	if err != nil {
		http.Redirect(w, r, "/list-sync?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer closer()

	resp, err := client.SyncNow(ctx, &listsyncv1.SyncNowRequest{SourceId: id})
	if err != nil {
		http.Redirect(w, r, "/list-sync?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	msg := fmt.Sprintf("Source sync complete: %d found, %d new, %d removed", resp.GetItemsFound(), resp.GetItemsNew(), resp.GetItemsRemoved())
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

	searchOnAdd := parseSearchOnAddForm(r)
	_, err = client.AddSource(ctx, &listsyncv1.AddSourceRequest{
		Name:                strings.TrimSpace(r.FormValue("name")),
		Type:                strings.TrimSpace(r.FormValue("type")),
		Username:            strings.TrimSpace(r.FormValue("username")),
		ClientId:            strings.TrimSpace(r.FormValue("client_id")),
		ListUrl:             strings.TrimSpace(r.FormValue("list_url")),
		SyncIntervalMinutes: int32(interval),
		BaseUrl:             strings.TrimSpace(r.FormValue("base_url")),
		ApiKey:              strings.TrimSpace(r.FormValue("api_key")),
		QualityProfileId:    strings.TrimSpace(r.FormValue("quality_profile_id")),
		RootFolderPath:      strings.TrimSpace(r.FormValue("root_folder_path")),
		CleanLibraryLevel:   strings.TrimSpace(r.FormValue("clean_library_level")),
		TagIds:              strings.TrimSpace(r.FormValue("tag_ids")),
		MonitorMode:         strings.TrimSpace(r.FormValue("monitor_mode")),
		MinimumAvailability: strings.TrimSpace(r.FormValue("minimum_availability")),
		SearchOnAdd:         searchOnAdd,
	})
	if err != nil {
		http.Redirect(w, r, "/list-sync?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/list-sync?ok="+url.QueryEscape("Source added"), http.StatusSeeOther)
}

func (h *Handler) ListSyncUpdateSource(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/list-sync/sources/"+id+"/edit?error="+url.QueryEscape("invalid form"), http.StatusSeeOther)
		return
	}

	ctx := r.Context()
	client, closer, err := h.withListSyncClient(ctx)
	if err != nil {
		http.Redirect(w, r, "/list-sync/sources/"+id+"/edit?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer closer()

	existing, err := h.listSyncFindSource(ctx, client, id)
	if err != nil {
		http.Redirect(w, r, "/list-sync?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}

	req := parseListSyncSourceForm(r, existing)
	req.Id = id
	_, err = client.UpdateSource(ctx, req)
	if err != nil {
		http.Redirect(w, r, "/list-sync/sources/"+id+"/edit?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/list-sync/sources/"+id+"/edit?ok="+url.QueryEscape("Source updated"), http.StatusSeeOther)
}

func (h *Handler) ListSyncToggleSource(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ctx := r.Context()
	client, closer, err := h.withListSyncClient(ctx)
	if err != nil {
		http.Redirect(w, r, "/list-sync?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer closer()

	existing, err := h.listSyncFindSource(ctx, client, id)
	if err != nil {
		http.Redirect(w, r, "/list-sync?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}

	enabled := !existing.GetEnabled()
	_, err = client.UpdateSource(ctx, &listsyncv1.UpdateSourceRequest{
		Id:                  id,
		Name:                existing.GetName(),
		Username:            existing.GetUsername(),
		ClientId:            existing.GetClientId(),
		ListUrl:             existing.GetListUrl(),
		SyncIntervalMinutes: existing.GetSyncIntervalMinutes(),
		BaseUrl:             existing.GetBaseUrl(),
		QualityProfileId:    existing.GetQualityProfileId(),
		RootFolderPath:      existing.GetRootFolderPath(),
		CleanLibraryLevel:   existing.GetCleanLibraryLevel(),
		TagIds:              existing.GetTagIds(),
		MonitorMode:         existing.GetMonitorMode(),
		MinimumAvailability: existing.GetMinimumAvailability(),
		SearchOnAdd:         boolPtr(existing.GetSearchOnAdd()),
		Enabled:             boolPtr(enabled),
	})
	if err != nil {
		http.Redirect(w, r, "/list-sync?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	state := "disabled"
	if !existing.GetEnabled() {
		state = "enabled"
	}
	http.Redirect(w, r, "/list-sync?ok="+url.QueryEscape("Source "+state), http.StatusSeeOther)
}

func (h *Handler) ListSyncTestSource(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/list-sync/sources/"+id+"/edit?error="+url.QueryEscape("invalid form"), http.StatusSeeOther)
		return
	}

	ctx := r.Context()
	client, closer, err := h.withListSyncClient(ctx)
	if err != nil {
		http.Redirect(w, r, "/list-sync/sources/"+id+"/edit?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer closer()

	resp, err := client.TestSource(ctx, &listsyncv1.TestSourceRequest{SourceId: id})
	if err != nil {
		http.Redirect(w, r, "/list-sync/sources/"+id+"/edit?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	q := url.Values{}
	if resp.GetOk() {
		q.Set("test_ok", "1")
		q.Set("test", resp.GetMessage())
	} else {
		q.Set("test", resp.GetMessage())
	}
	http.Redirect(w, r, "/list-sync/sources/"+id+"/edit?"+q.Encode(), http.StatusSeeOther)
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
