package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	templates "github.com/Muxcore-Media/admin-ui/templ"
	backupv1 "github.com/Muxcore-Media/backup-local/muxcore/backup/v1"
	authv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/auth/v1"
)

const (
	capBackup    = "backup"
	capScheduler = "scheduler"
)

var (
	networkingMu   sync.Mutex
	networkingPath = envOr("ADMIN_UI_NETWORKING_FILE", filepath.Join(os.TempDir(), "muxcore-admin-networking.json"))
	parentalMu     sync.Mutex
	parentalPath   = envOr("ADMIN_UI_PARENTAL_FILE", filepath.Join(os.TempDir(), "muxcore-admin-parental.json"))
	livetvMu       sync.Mutex
	livetvPath     = envOr("ADMIN_UI_LIVETV_FILE", filepath.Join(os.TempDir(), "muxcore-admin-livetv.json"))
)

// --- API Keys catalog ---

func (h *Handler) APIKeysPage(w http.ResponseWriter, r *http.Request) {
	rows, errMsg := h.collectAPIKeys(r.Context())
	content := templates.APIKeysLivePage(rows, errMsg)
	nav := h.nav(r.URL.Path)
	h.render(w, r, templates.Layout("API Keys", nav, content))
}

func (h *Handler) APIKeysRevoke(w http.ResponseWriter, r *http.Request) {
	tokenID := r.PathValue("id")
	userID := r.URL.Query().Get("user")
	client, conn, err := h.authClient(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer conn.Close()
	_, _ = client.DeleteAPIToken(r.Context(), &authv1.DeleteAPITokenRequest{TokenId: tokenID})
	if sess := SessionFromContext(r.Context()); sess != nil {
		h.auditLog(r.Context(), sess.UserID, "admin.apikey.revoke", "token", tokenID, map[string]string{"user_id": userID})
	}
	http.Redirect(w, r, "/keys", http.StatusSeeOther)
}

func (h *Handler) collectAPIKeys(ctx context.Context) ([]templates.APIKeyRow, string) {
	client, conn, err := h.authClient(ctx)
	if err != nil {
		return nil, "auth unavailable: " + err.Error()
	}
	defer conn.Close()
	users, err := client.ListUsers(ctx, &authv1.ListUsersRequest{})
	if err != nil {
		return nil, "list users: " + err.Error()
	}
	var rows []templates.APIKeyRow
	for _, u := range users.GetUsers() {
		tok, err := client.ListAPITokens(ctx, &authv1.ListAPITokensRequest{UserId: u.GetId()})
		if err != nil {
			continue
		}
		for _, t := range tok.GetTokens() {
			rows = append(rows, templates.APIKeyRow{
				UserID:   u.GetId(),
				Username: u.GetUsername(),
				TokenID:  t.GetId(),
				Name:     t.GetName(),
				Prefix:   t.GetPrefix(),
				Scopes:   strings.Join(t.GetScopes(), ", "),
			})
		}
	}
	return rows, ""
}

// --- Backups ---

func (h *Handler) backupClient(ctx context.Context) (backupv1.BackupServiceClient, *grpc.ClientConn, error) {
	if h.Core == nil {
		return nil, nil, fmt.Errorf("core unavailable")
	}
	mods, err := h.Core.Discovery.FindByCapability(ctx, capBackup)
	if err != nil {
		return nil, nil, err
	}
	if len(mods) == 0 {
		return nil, nil, fmt.Errorf("no module with capability %s", capBackup)
	}
	mod := mods[0]
	addr := normalizeDialAddr(mod.GetId(), mod.GetHttpAddr())
	if addr == "" {
		return nil, nil, fmt.Errorf("backup module has no dial address")
	}
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, err
	}
	return backupv1.NewBackupServiceClient(conn), conn, nil
}

func (h *Handler) BackupsPage(w http.ResponseWriter, r *http.Request) {
	rows, errMsg := h.listBackupRows(r.Context())
	flash := r.URL.Query().Get("ok")
	content := templates.BackupsLivePage(rows, errMsg, flash)
	nav := h.nav(r.URL.Path)
	h.render(w, r, templates.Layout("Backups", nav, content))
}

func (h *Handler) listBackupRows(ctx context.Context) ([]templates.BackupRow, string) {
	client, conn, err := h.backupClient(ctx)
	if err != nil {
		return nil, err.Error()
	}
	defer conn.Close()
	resp, err := client.ListBackups(ctx, &backupv1.ListBackupsRequest{})
	if err != nil {
		return nil, err.Error()
	}
	rows := make([]templates.BackupRow, 0, len(resp.GetBackups()))
	for _, b := range resp.GetBackups() {
		ts := time.Unix(b.GetTimestampUnix(), 0).UTC().Format(time.RFC822)
		rows = append(rows, templates.BackupRow{
			ID:        b.GetId(),
			Timestamp: ts,
			Size:      formatBytes(b.GetSizeBytes()),
			Checksum:  b.GetChecksumSha256(),
			Modules:   strings.Join(b.GetModuleIds(), ", "),
		})
	}
	return rows, ""
}

func formatBytes(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	if n < 1024*1024 {
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	}
	return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
}

func (h *Handler) BackupsCreate(w http.ResponseWriter, r *http.Request) {
	client, conn, err := h.backupClient(r.Context())
	if err != nil {
		http.Redirect(w, r, "/backups?ok="+urlQuery(err.Error()), http.StatusSeeOther)
		return
	}
	defer conn.Close()
	sourcePaths := h.libraryBackupSourcePaths(r.Context())
	resp, err := client.CreateBackup(r.Context(), &backupv1.CreateBackupRequest{SourcePaths: sourcePaths})
	if err != nil {
		http.Redirect(w, r, "/backups?ok="+urlQuery(err.Error()), http.StatusSeeOther)
		return
	}
	msg := "created"
	if resp.GetBackup() != nil {
		msg = "created " + resp.GetBackup().GetId()
	}
	http.Redirect(w, r, "/backups?ok="+urlQuery(msg), http.StatusSeeOther)
}

func (h *Handler) libraryBackupSourcePaths(ctx context.Context) []string {
	seen := map[string]bool{}
	var paths []string
	add := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" {
			return
		}
		d := filepath.Dir(p)
		if d == "" || d == "." || seen[d] {
			return
		}
		seen[d] = true
		paths = append(paths, d)
	}
	add(os.Getenv("MOVIES_DB_PATH"))
	add(os.Getenv("TVSHOWS_DB_PATH"))
	if h.Core == nil {
		return paths
	}
	mods, err := h.Core.Discovery.FindByCapability(ctx, "backupable")
	if err != nil || len(mods) == 0 {
		return paths
	}
	// Capability presence confirms libraries advertise Backupable; env paths supply archive roots.
	_ = mods
	return paths
}

func (h *Handler) BackupsDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	client, conn, err := h.backupClient(r.Context())
	if err != nil {
		http.Redirect(w, r, "/backups?ok="+urlQuery(err.Error()), http.StatusSeeOther)
		return
	}
	defer conn.Close()
	_, err = client.DeleteBackup(r.Context(), &backupv1.DeleteBackupRequest{BackupId: id})
	msg := "deleted " + id
	if err != nil {
		msg = err.Error()
	}
	http.Redirect(w, r, "/backups?ok="+urlQuery(msg), http.StatusSeeOther)
}

func (h *Handler) BackupsRestore(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	_ = r.ParseForm()
	target := r.FormValue("target_path")
	client, conn, err := h.backupClient(r.Context())
	if err != nil {
		http.Redirect(w, r, "/backups?ok="+urlQuery(err.Error()), http.StatusSeeOther)
		return
	}
	defer conn.Close()
	resp, err := client.RestoreBackup(r.Context(), &backupv1.RestoreBackupRequest{BackupId: id, TargetPath: target})
	msg := "restored"
	if err != nil {
		msg = err.Error()
	} else if resp != nil {
		msg = fmt.Sprintf("restored %d files → %s", resp.GetFilesRestored(), target)
	}
	http.Redirect(w, r, "/backups?ok="+urlQuery(msg), http.StatusSeeOther)
}

func urlQuery(s string) string {
	return url.QueryEscape(s)
}

// --- Tasks (scheduler-cron HTTP) ---

func (h *Handler) schedulerHTTPBase(ctx context.Context) (string, error) {
	if h.Core == nil {
		return "", fmt.Errorf("core unavailable")
	}
	mods, err := h.Core.Discovery.FindByCapability(ctx, capScheduler)
	if err != nil {
		return "", err
	}
	if len(mods) == 0 {
		return "", fmt.Errorf("no module with capability %s", capScheduler)
	}
	mod := mods[0]
	addr := normalizeDialAddr(mod.GetId(), mod.GetHttpAddr())
	if addr == "" {
		return "", fmt.Errorf("scheduler has no HTTP address")
	}
	if !strings.HasPrefix(addr, "http") {
		addr = "http://" + addr
	}
	return strings.TrimRight(addr, "/"), nil
}

func (h *Handler) TasksPage(w http.ResponseWriter, r *http.Request) {
	rows, errMsg := h.listTaskRows(r.Context())
	flash := r.URL.Query().Get("ok")
	content := templates.TasksLivePage(rows, errMsg, flash)
	nav := h.nav(r.URL.Path)
	h.render(w, r, templates.Layout("Scheduled Tasks", nav, content))
}

func (h *Handler) listTaskRows(ctx context.Context) ([]templates.TaskRow, string) {
	base, err := h.schedulerHTTPBase(ctx)
	if err != nil {
		return nil, err.Error()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/list", nil)
	if err != nil {
		return nil, err.Error()
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err.Error()
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, fmt.Sprintf("scheduler /list: %s", strings.TrimSpace(string(body)))
	}
	var raw []map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, "decode tasks: " + err.Error()
	}
	rows := make([]templates.TaskRow, 0, len(raw))
	for _, t := range raw {
		rows = append(rows, templates.TaskRow{
			ID:        fmt.Sprint(t["id"]),
			Name:      fmt.Sprint(t["name"]),
			CronExpr:  fmt.Sprint(t["cron_expr"]),
			Status:    fmt.Sprint(t["status"]),
			CreatedAt: fmt.Sprint(t["created_at"]),
			LastFired: fmt.Sprint(t["last_fired"]),
		})
	}
	return rows, ""
}

func (h *Handler) TasksCancel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	base, err := h.schedulerHTTPBase(r.Context())
	if err != nil {
		http.Redirect(w, r, "/tasks?ok="+urlQuery(err.Error()), http.StatusSeeOther)
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodDelete, base+"/cancel/"+id, nil)
	if err != nil {
		http.Redirect(w, r, "/tasks?ok="+urlQuery(err.Error()), http.StatusSeeOther)
		return
	}
	resp, err := http.DefaultClient.Do(req)
	msg := "cancelled " + id
	if err != nil {
		msg = err.Error()
	} else {
		defer resp.Body.Close()
		if resp.StatusCode >= 300 {
			b, _ := io.ReadAll(resp.Body)
			msg = strings.TrimSpace(string(b))
		}
	}
	http.Redirect(w, r, "/tasks?ok="+urlQuery(msg), http.StatusSeeOther)
}

// --- Networking ---

type networkingFile struct {
	PublicURL      string `json:"public_url"`
	TrustedProxies string `json:"trusted_proxies"`
	PublishedHosts string `json:"published_hosts"`
	HTTPPort       string `json:"http_port"`
	HTTPSPort      string `json:"https_port"`
}

func loadNetworking() networkingFile {
	networkingMu.Lock()
	defer networkingMu.Unlock()
	raw, err := os.ReadFile(networkingPath)
	if err != nil {
		return networkingFile{HTTPPort: "80", HTTPSPort: "443"}
	}
	var n networkingFile
	if json.Unmarshal(raw, &n) != nil {
		return networkingFile{HTTPPort: "80", HTTPSPort: "443"}
	}
	return n
}

func saveNetworking(n networkingFile) error {
	networkingMu.Lock()
	defer networkingMu.Unlock()
	_ = os.MkdirAll(filepath.Dir(networkingPath), 0o700)
	raw, err := json.MarshalIndent(n, "", "  ")
	if err != nil {
		return err
	}
	tmp := networkingPath + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, networkingPath)
}

func (h *Handler) NetworkingPage(w http.ResponseWriter, r *http.Request) {
	n := loadNetworking()
	data := templates.NetworkingData{
		PublicURL:      n.PublicURL,
		TrustedProxies: n.TrustedProxies,
		PublishedHosts: n.PublishedHosts,
		HTTPPort:       n.HTTPPort,
		HTTPSPort:      n.HTTPSPort,
		RuntimePublic:  h.PublicURL,
		RuntimeTrusted: fmt.Sprintf("%d CIDR(s)", len(h.TrustedProxies)),
		Saved:          r.URL.Query().Get("saved") == "1",
	}
	content := templates.NetworkingEditPage(data)
	nav := h.nav(r.URL.Path)
	h.render(w, r, templates.Layout("Networking", nav, content))
}

func (h *Handler) NetworkingSave(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	n := networkingFile{
		PublicURL:      strings.TrimSpace(r.FormValue("public_url")),
		TrustedProxies: strings.TrimSpace(r.FormValue("trusted_proxies")),
		PublishedHosts: strings.TrimSpace(r.FormValue("published_hosts")),
		HTTPPort:       strings.TrimSpace(r.FormValue("http_port")),
		HTTPSPort:      strings.TrimSpace(r.FormValue("https_port")),
	}
	if err := saveNetworking(n); err != nil {
		data := templates.NetworkingData{
			PublicURL: n.PublicURL, TrustedProxies: n.TrustedProxies, PublishedHosts: n.PublishedHosts,
			HTTPPort: n.HTTPPort, HTTPSPort: n.HTTPSPort, Error: err.Error(),
			RuntimePublic: h.PublicURL, RuntimeTrusted: fmt.Sprintf("%d CIDR(s)", len(h.TrustedProxies)),
		}
		content := templates.NetworkingEditPage(data)
		nav := h.nav(r.URL.Path)
		h.render(w, r, templates.Layout("Networking", nav, content))
		return
	}
	http.Redirect(w, r, "/networking?saved=1", http.StatusSeeOther)
}

// --- Parental controls (admin-local store until auth UserInfo grows fields) ---

type parentalSettings struct {
	MaxParentalRating string `json:"max_parental_rating"`
	BlockedTags       string `json:"blocked_tags"`
	AllowedTags       string `json:"allowed_tags"`
	AllowUnrated      bool   `json:"allow_unrated"`
}

func loadParentalMap() map[string]parentalSettings {
	parentalMu.Lock()
	defer parentalMu.Unlock()
	raw, err := os.ReadFile(parentalPath)
	if err != nil {
		return map[string]parentalSettings{}
	}
	var m map[string]parentalSettings
	if json.Unmarshal(raw, &m) != nil || m == nil {
		return map[string]parentalSettings{}
	}
	return m
}

func saveParentalMap(m map[string]parentalSettings) error {
	parentalMu.Lock()
	defer parentalMu.Unlock()
	_ = os.MkdirAll(filepath.Dir(parentalPath), 0o700)
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp := parentalPath + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, parentalPath)
}

func (h *Handler) UsersParental(w http.ResponseWriter, r *http.Request) {
	userID := r.PathValue("id")
	m := loadParentalMap()
	p := m[userID]
	if r.Method == http.MethodPost {
		_ = r.ParseForm()
		p = parentalSettings{
			MaxParentalRating: strings.TrimSpace(r.FormValue("max_rating")),
			BlockedTags:       strings.TrimSpace(r.FormValue("blocked_tags")),
			AllowedTags:       strings.TrimSpace(r.FormValue("allowed_tags")),
			AllowUnrated:      r.FormValue("allow_unrated") == "1",
		}
		m[userID] = p
		if err := saveParentalMap(m); err != nil {
			_ = templates.UserParentalForm(templates.ParentalData{
				UserID: userID, MaxParentalRating: p.MaxParentalRating, BlockedTags: p.BlockedTags,
				AllowedTags: p.AllowedTags, AllowUnrated: p.AllowUnrated, Error: err.Error(),
			}).Render(r.Context(), w)
			return
		}
		_ = templates.UserParentalForm(templates.ParentalData{
			UserID: userID, MaxParentalRating: p.MaxParentalRating, BlockedTags: p.BlockedTags,
			AllowedTags: p.AllowedTags, AllowUnrated: p.AllowUnrated, Saved: true,
		}).Render(r.Context(), w)
		return
	}
	_ = templates.UserParentalForm(templates.ParentalData{
		UserID: userID, MaxParentalRating: p.MaxParentalRating, BlockedTags: p.BlockedTags,
		AllowedTags: p.AllowedTags, AllowUnrated: p.AllowUnrated,
	}).Render(r.Context(), w)
}

// --- Live TV admin ---

type liveTVGuideRow struct {
	ChannelID string `json:"channel_id"`
	Title     string `json:"title"`
	Start     string `json:"start"`
	End       string `json:"end"`
}

type liveTVFile struct {
	Channels   []templates.LiveTVChannel `json:"channels"`
	Tuners     []map[string]any          `json:"tuners,omitempty"`
	Recordings []map[string]any          `json:"recordings,omitempty"`
	Timers     []map[string]any          `json:"timers,omitempty"`
	Guide      []liveTVGuideRow          `json:"guide,omitempty"`
}

func defaultLiveTVChannels() []templates.LiveTVChannel {
	return []templates.LiveTVChannel{
		{ID: "ch1", Name: "MuxCore Demo 1", Number: "1", Category: "Demo", URL: ""},
		{ID: "ch2", Name: "MuxCore Demo 2", Number: "2", Category: "Demo", URL: ""},
	}
}

func defaultLiveTVFile() liveTVFile {
	now := time.Now().UTC()
	return liveTVFile{
		Channels: defaultLiveTVChannels(),
		Tuners: []map[string]any{
			{"id": "tuner1", "name": "Demo tuner", "type": "m3u", "url": "", "enabled": true},
		},
		Timers:     []map[string]any{},
		Recordings: []map[string]any{},
		Guide: []liveTVGuideRow{
			{
				ChannelID: "ch1", Title: "MuxCore Demo — Morning block",
				Start: now.Add(-30 * time.Minute).Format(time.RFC3339),
				End:   now.Add(30 * time.Minute).Format(time.RFC3339),
			},
			{
				ChannelID: "ch2", Title: "MuxCore Demo 2 — Afternoon",
				Start: now.Add(-10 * time.Minute).Format(time.RFC3339),
				End:   now.Add(50 * time.Minute).Format(time.RFC3339),
			},
		},
	}
}

func loadLiveTV() liveTVFile {
	livetvMu.Lock()
	defer livetvMu.Unlock()
	raw, err := os.ReadFile(livetvPath)
	if err != nil {
		return defaultLiveTVFile()
	}
	var f liveTVFile
	if json.Unmarshal(raw, &f) != nil || len(f.Channels) == 0 {
		return defaultLiveTVFile()
	}
	return f
}

func saveLiveTV(f liveTVFile) error {
	livetvMu.Lock()
	defer livetvMu.Unlock()
	_ = os.MkdirAll(filepath.Dir(livetvPath), 0o700)
	raw, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp := livetvPath + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, livetvPath)
}

func (h *Handler) LiveTVAdminPage(w http.ResponseWriter, r *http.Request) {
	f := loadLiveTV()
	raw, _ := json.MarshalIndent(f, "", "  ")
	data := templates.LiveTVAdminData{
		ChannelsJSON: string(raw),
		Channels:     f.Channels,
		Saved:        r.URL.Query().Get("saved") == "1",
	}
	content := templates.LiveTVAdminEditPage(data)
	nav := h.nav(r.URL.Path)
	h.render(w, r, templates.Layout("Live TV", nav, content))
}

func (h *Handler) LiveTVAdminSave(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	raw := r.FormValue("channels_json")
	var f liveTVFile
	if err := json.Unmarshal([]byte(raw), &f); err != nil {
		data := templates.LiveTVAdminData{ChannelsJSON: raw, Error: "invalid JSON: " + err.Error()}
		content := templates.LiveTVAdminEditPage(data)
		nav := h.nav(r.URL.Path)
		h.render(w, r, templates.Layout("Live TV", nav, content))
		return
	}
	if err := saveLiveTV(f); err != nil {
		data := templates.LiveTVAdminData{ChannelsJSON: raw, Error: err.Error(), Channels: f.Channels}
		content := templates.LiveTVAdminEditPage(data)
		nav := h.nav(r.URL.Path)
		h.render(w, r, templates.Layout("Live TV", nav, content))
		return
	}
	slog.Info("livetv channels saved", "count", len(f.Channels), "path", livetvPath)
	http.Redirect(w, r, "/livetv?saved=1", http.StatusSeeOther)
}

// --- Libraries / Playback / Plugins (live) ---

var (
	playbackMu   sync.Mutex
	playbackPath = envOr("ADMIN_UI_PLAYBACK_FILE", filepath.Join(os.TempDir(), "muxcore-admin-playback.json"))
)

type playbackFile struct {
	EnableResume     bool   `json:"enable_resume"`
	EnableTranscode  bool   `json:"enable_transcode"`
	PreferDirectPlay bool   `json:"prefer_direct_play"`
	TrickplayEnabled bool   `json:"trickplay_enabled"`
	MaxBitrateMbps   string `json:"max_bitrate_mbps"`
	FFmpegBin        string `json:"ffmpeg_bin,omitempty"`
}

func loadPlayback() playbackFile {
	playbackMu.Lock()
	defer playbackMu.Unlock()
	raw, err := os.ReadFile(playbackPath)
	if err != nil {
		return playbackFile{EnableResume: true, PreferDirectPlay: true, MaxBitrateMbps: "80", FFmpegBin: "ffmpeg"}
	}
	var p playbackFile
	if json.Unmarshal(raw, &p) != nil {
		return playbackFile{EnableResume: true, PreferDirectPlay: true, MaxBitrateMbps: "80", FFmpegBin: "ffmpeg"}
	}
	if p.FFmpegBin == "" {
		p.FFmpegBin = "ffmpeg"
	}
	return p
}

func savePlayback(p playbackFile) error {
	playbackMu.Lock()
	defer playbackMu.Unlock()
	_ = os.MkdirAll(filepath.Dir(playbackPath), 0o700)
	raw, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	tmp := playbackPath + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, playbackPath)
}

const (
	capTranscoder     = "transcoder"
	capMediaTranscode = "media.transcoder"
)

func (h *Handler) resolveTranscoder(ctx context.Context) (id, httpAddr string, ok bool) {
	if h.Core == nil {
		return "", "", false
	}
	for _, cap := range []string{capMediaTranscode, capTranscoder} {
		mods, err := h.Core.Discovery.FindByCapability(ctx, cap)
		if err != nil || len(mods) == 0 {
			continue
		}
		for _, m := range mods {
			if m.GetId() == "" {
				continue
			}
			return m.GetId(), normalizeDialAddr(m.GetId(), m.GetHttpAddr()), true
		}
	}
	return "", "", false
}

func (h *Handler) loadTranscoderFFmpeg(ctx context.Context) (moduleID, ffmpeg string, present bool) {
	id, addr, ok := h.resolveTranscoder(ctx)
	if !ok {
		return "", "", false
	}
	raw, err := h.settingsMeshCall(ctx, id, addr, methodGet, nil)
	if err != nil {
		return id, "", true
	}
	var defs []settingDefJSON
	if json.Unmarshal(raw, &defs) != nil {
		return id, "", true
	}
	for _, d := range defs {
		if strings.EqualFold(d.Key, "ffmpeg_bin") || strings.EqualFold(d.Key, "TRANSCODER_FFMPEG_BIN") {
			val := d.Value
			if val == "" {
				val = d.Default
			}
			return id, val, true
		}
	}
	return id, "", true
}

func (h *Handler) pushTranscoderFFmpeg(ctx context.Context, ffmpeg string) (moduleID string, err error) {
	id, addr, ok := h.resolveTranscoder(ctx)
	if !ok {
		return "", fmt.Errorf("transcoder absent")
	}
	ffmpeg = strings.TrimSpace(ffmpeg)
	if ffmpeg == "" {
		ffmpeg = "ffmpeg"
	}
	payload, err := json.Marshal(updateSettingReq{Key: "ffmpeg_bin", Value: ffmpeg})
	if err != nil {
		return id, err
	}
	_, err = h.settingsMeshCall(ctx, id, addr, methodUpdate, payload)
	return id, err
}

func (h *Handler) PlaybackAdminPage(w http.ResponseWriter, r *http.Request) {
	p := loadPlayback()
	tid, ffmpeg, present := h.loadTranscoderFFmpeg(r.Context())
	if present && ffmpeg != "" {
		p.FFmpegBin = ffmpeg
	}
	data := templates.PlaybackAdminData{
		EnableResume: p.EnableResume, EnableTranscode: p.EnableTranscode,
		PreferDirectPlay: p.PreferDirectPlay, TrickplayEnabled: p.TrickplayEnabled,
		MaxBitrateMbps: p.MaxBitrateMbps, FFmpegBin: p.FFmpegBin,
		TranscoderID: tid, TranscoderPresent: present, SoftEmpty: !present,
		Saved: r.URL.Query().Get("saved") == "1",
	}
	if n := r.URL.Query().Get("notice"); n != "" {
		data.Notice = n
	}
	content := templates.PlaybackAdminEditPage(data)
	nav := h.nav(r.URL.Path)
	h.render(w, r, templates.Layout("Playback", nav, content))
}

func (h *Handler) PlaybackAdminSave(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	p := playbackFile{
		EnableResume:     r.FormValue("enable_resume") == "1",
		EnableTranscode:  r.FormValue("enable_transcode") == "1",
		PreferDirectPlay: r.FormValue("prefer_direct") == "1",
		TrickplayEnabled: r.FormValue("trickplay") == "1",
		MaxBitrateMbps:   strings.TrimSpace(r.FormValue("max_bitrate")),
		FFmpegBin:        strings.TrimSpace(r.FormValue("ffmpeg_bin")),
	}
	if p.FFmpegBin == "" {
		p.FFmpegBin = "ffmpeg"
	}
	if err := savePlayback(p); err != nil {
		data := templates.PlaybackAdminData{
			EnableResume: p.EnableResume, EnableTranscode: p.EnableTranscode,
			PreferDirectPlay: p.PreferDirectPlay, TrickplayEnabled: p.TrickplayEnabled,
			MaxBitrateMbps: p.MaxBitrateMbps, FFmpegBin: p.FFmpegBin, Error: err.Error(),
			SoftEmpty: true,
		}
		content := templates.PlaybackAdminEditPage(data)
		nav := h.nav(r.URL.Path)
		h.render(w, r, templates.Layout("Playback", nav, content))
		return
	}
	notice := ""
	if _, err := h.pushTranscoderFFmpeg(r.Context(), p.FFmpegBin); err != nil {
		if strings.Contains(err.Error(), "absent") {
			notice = "Local policy saved; media-transcoder not registered (soft-empty)."
		} else {
			notice = "Local policy saved; transcoder UpdateSetting failed: " + err.Error()
		}
	}
	redir := "/playback?saved=1"
	if notice != "" {
		redir += "&notice=" + url.QueryEscape(notice)
	}
	http.Redirect(w, r, redir, http.StatusSeeOther)
}

func (h *Handler) LibrariesAdminPage(w http.ResponseWriter, r *http.Request) {
	h.refreshMediaNavLinks(r.Context())
	var rows []templates.LibraryModRow
	h.mediaMu.RLock()
	for _, mod := range h.mediaModules {
		rows = append(rows, templates.LibraryModRow{
			ID:   mod.GetId(),
			Name: firstNonEmpty(mod.GetName(), mod.GetId()),
			Path: "/media/" + mod.GetId(),
		})
	}
	h.mediaMu.RUnlock()
	content := templates.LibrariesLivePage(rows)
	nav := h.nav(r.URL.Path)
	h.render(w, r, templates.Layout("Libraries", nav, content))
}

func (h *Handler) PluginsPage(w http.ResponseWriter, r *http.Request) {
	mods := h.loadModuleList(r)
	content := templates.PluginsCatalogPage(mods)
	nav := h.nav(r.URL.Path)
	h.render(w, r, templates.Layout("Plugins", nav, content))
}

func (h *Handler) MetadataManagerPage(w http.ResponseWriter, r *http.Request) {
	h.refreshMediaNavLinks(r.Context())
	var rows []templates.MetadataLibRow
	h.mediaMu.RLock()
	for _, mod := range h.mediaModules {
		rows = append(rows, templates.MetadataLibRow{
			ID:   mod.GetId(),
			Name: firstNonEmpty(mod.GetName(), mod.GetId()),
			Path: "/media/" + mod.GetId(),
		})
	}
	h.mediaMu.RUnlock()
	content := templates.MetadataManagerPage(rows)
	nav := h.nav(r.URL.Path)
	h.render(w, r, templates.Layout("Metadata", nav, content))
}

func (h *Handler) AuthSSOPage(w http.ResponseWriter, r *http.Request) {
	all := h.loadModuleList(r)
	var authMods []templates.ModuleListItem
	for _, m := range all {
		id := strings.ToLower(m.ID)
		if strings.HasPrefix(id, "auth") || containsStr(m.Capabilities, "auth") || containsStr(m.Capabilities, "authorizer") {
			authMods = append(authMods, m)
		}
	}
	content := templates.AuthSSOPage(templates.AuthSSOData{
		AuthModules: authMods,
		Notes: []string{
			"auth-local: username/password, TOTP, WebAuthn, API tokens.",
			"auth-oidc: federated SSO when the module is registered; configure via Settings.",
			"Consumer Quick Connect codes are approved in media-ui-app; admin sessions revoke under Devices.",
		},
	})
	nav := h.nav(r.URL.Path)
	h.render(w, r, templates.Layout("Authentication", nav, content))
}

func containsStr(list []string, want string) bool {
	for _, s := range list {
		if strings.EqualFold(s, want) {
			return true
		}
	}
	return false
}
