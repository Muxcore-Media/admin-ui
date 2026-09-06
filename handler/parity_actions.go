package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	"unicode"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	templates "github.com/Muxcore-Media/admin-ui/templ"
	backupv1 "github.com/Muxcore-Media/backup-local/muxcore/backup/v1"
	authv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/auth/v1"
)

const (
	capBackup             = "backup"
	capScheduler          = "scheduler"
	backupDialTimeout     = 3 * time.Second
	backupReadTimeout     = 5 * time.Second
	backupListPageTimeout = backupDialTimeout + backupReadTimeout + time.Second
	backupActionTimeout   = backupDialTimeout + backupReadTimeout + time.Second
	schedulerDialTimeout  = 3 * time.Second
	schedulerReadTimeout  = 5 * time.Second
	schedulerPageTimeout  = schedulerDialTimeout + schedulerReadTimeout + time.Second
	metadataDialTimeout   = 3 * time.Second
	metadataPageTimeout   = metadataDialTimeout + time.Second
	moduleListDialTimeout = 3 * time.Second
	moduleListReadTimeout = 5 * time.Second
	moduleListPageTimeout = moduleListDialTimeout + moduleListReadTimeout + time.Second
)

func schedulerHTTPDo(_ context.Context, req *http.Request) (*http.Response, error) {
	return (&http.Client{Timeout: schedulerReadTimeout}).Do(req)
}

var (
	networkingMu sync.Mutex
	parentalMu   sync.Mutex
	livetvMu     sync.Mutex
)

// --- API Keys catalog ---

func (h *Handler) APIKeysPage(w http.ResponseWriter, r *http.Request) {
	pageCtx, pageCancel := context.WithTimeout(r.Context(), usersDialTimeout+usersReadTimeout+time.Second)
	defer pageCancel()
	rows, users, errMsg := h.collectAPIKeysAndUsers(pageCtx)
	content := templates.APIKeysLivePage(rows, users, errMsg)
	nav := h.nav(r.URL.Path)
	h.render(w, r, templates.Layout("API Keys", nav, content))
}

func (h *Handler) APIKeysCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	userID := strings.TrimSpace(r.FormValue("user_id"))
	name := strings.TrimSpace(r.FormValue("name"))
	if userID == "" || name == "" {
		http.Error(w, "user_id and name are required", http.StatusBadRequest)
		return
	}
	pageCtx, pageCancel := context.WithTimeout(r.Context(), usersDialTimeout+usersReadTimeout+time.Second)
	defer pageCancel()
	dialCtx, dialCancel := context.WithTimeout(pageCtx, usersDialTimeout)
	client, conn, err := h.authClient(dialCtx)
	dialCancel()
	if err != nil {
		http.Error(w, "auth unavailable: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer func() { _ = conn.Close() }()
	readCtx, readCancel := context.WithTimeout(pageCtx, usersReadTimeout)
	resp, err := client.CreateAPIToken(readCtx, &authv1.CreateAPITokenRequest{UserId: userID, Name: name})
	readCancel()
	if err != nil {
		http.Error(w, "create token: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if resp.GetError() != "" {
		http.Error(w, resp.GetError(), http.StatusBadRequest)
		return
	}
	if sess := SessionFromContext(r.Context()); sess != nil {
		h.auditLog(r.Context(), sess.UserID, "admin.apikey.create", "token_name", name, map[string]string{"user_id": userID})
	}
	username := h.resolveUsername(pageCtx, client, userID)
	nav := h.nav("/keys")
	content := templates.APIKeyCopyOnce(username, name, resp.GetToken())
	h.render(w, r, templates.Layout("API Key Created", nav, content))
}

func (h *Handler) APIKeysRotate(w http.ResponseWriter, r *http.Request) {
	oldTokenID := r.PathValue("id")
	userID := r.URL.Query().Get("user")
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if userID == "" || name == "" {
		http.Error(w, "user and name are required", http.StatusBadRequest)
		return
	}
	pageCtx, pageCancel := context.WithTimeout(r.Context(), usersDialTimeout+2*usersReadTimeout+time.Second)
	defer pageCancel()
	dialCtx, dialCancel := context.WithTimeout(pageCtx, usersDialTimeout)
	client, conn, err := h.authClient(dialCtx)
	dialCancel()
	if err != nil {
		http.Error(w, "auth unavailable: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer func() { _ = conn.Close() }()
	createCtx, createCancel := context.WithTimeout(pageCtx, usersReadTimeout)
	resp, err := client.CreateAPIToken(createCtx, &authv1.CreateAPITokenRequest{UserId: userID, Name: name})
	createCancel()
	if err != nil {
		http.Error(w, "create token: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if resp.GetError() != "" {
		http.Error(w, resp.GetError(), http.StatusBadRequest)
		return
	}
	deleteCtx, deleteCancel := context.WithTimeout(pageCtx, usersReadTimeout)
	_, _ = client.DeleteAPIToken(deleteCtx, &authv1.DeleteAPITokenRequest{TokenId: oldTokenID})
	deleteCancel()
	if sess := SessionFromContext(r.Context()); sess != nil {
		h.auditLog(r.Context(), sess.UserID, "admin.apikey.rotate", "old_token", oldTokenID, map[string]string{
			"user_id": userID, "token_name": name,
		})
	}
	username := h.resolveUsername(pageCtx, client, userID)
	nav := h.nav("/keys")
	content := templates.APIKeyCopyOnce(username, name, resp.GetToken())
	h.render(w, r, templates.Layout("API Key Rotated", nav, content))
}

func (h *Handler) APIKeysRevoke(w http.ResponseWriter, r *http.Request) {
	tokenID := r.PathValue("id")
	userID := r.URL.Query().Get("user")
	pageCtx, pageCancel := context.WithTimeout(r.Context(), usersDialTimeout+usersReadTimeout)
	defer pageCancel()
	client, conn, err := h.authClient(pageCtx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer func() { _ = conn.Close() }()
	_, _ = client.DeleteAPIToken(pageCtx, &authv1.DeleteAPITokenRequest{TokenId: tokenID})
	if sess := SessionFromContext(r.Context()); sess != nil {
		h.auditLog(r.Context(), sess.UserID, "admin.apikey.revoke", "token", tokenID, map[string]string{"user_id": userID})
	}
	http.Redirect(w, r, "/keys", http.StatusSeeOther)
}

func (h *Handler) collectAPIKeysAndUsers(ctx context.Context) ([]templates.APIKeyRow, []templates.APIKeyUserOption, string) {
	dialCtx, dialCancel := context.WithTimeout(ctx, usersDialTimeout)
	client, conn, err := h.authClient(dialCtx)
	dialCancel()
	if err != nil {
		return nil, nil, "auth unavailable: " + err.Error()
	}
	defer func() { _ = conn.Close() }()
	readCtx, readCancel := context.WithTimeout(ctx, usersReadTimeout)
	listResp, err := client.ListUsers(readCtx, &authv1.ListUsersRequest{})
	readCancel()
	if err != nil {
		return nil, nil, "list users: " + err.Error()
	}
	var rows []templates.APIKeyRow
	var users []templates.APIKeyUserOption
	for _, u := range listResp.GetUsers() {
		users = append(users, templates.APIKeyUserOption{ID: u.GetId(), Username: u.GetUsername()})
		readCtx, readCancel := context.WithTimeout(ctx, usersReadTimeout)
		tok, err := client.ListAPITokens(readCtx, &authv1.ListAPITokensRequest{UserId: u.GetId()})
		readCancel()
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
	return rows, users, ""
}

// resolveUsername looks up the username for userID from the auth service.
// On failure it returns userID as a fallback so callers always get a displayable string.
func (h *Handler) resolveUsername(ctx context.Context, client authv1.AuthServiceClient, userID string) string {
	readCtx, readCancel := context.WithTimeout(ctx, usersReadTimeout)
	defer readCancel()
	resp, err := client.ListUsers(readCtx, &authv1.ListUsersRequest{})
	if err != nil {
		return userID
	}
	for _, u := range resp.GetUsers() {
		if u.GetId() == userID {
			return u.GetUsername()
		}
	}
	return userID
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
	pageCtx, cancel := context.WithTimeout(r.Context(), backupListPageTimeout)
	defer cancel()
	rows, errMsg := h.listBackupRows(pageCtx)
	sched := h.loadBackupSchedule(pageCtx)
	flash := r.URL.Query().Get("ok")
	content := templates.BackupsLivePage(rows, errMsg, flash, sched)
	nav := h.nav(r.URL.Path)
	h.render(w, r, templates.Layout("Backups", nav, content))
}

func (h *Handler) loadBackupSchedule(ctx context.Context) templates.BackupScheduleData {
	dialCtx, dialCancel := context.WithTimeout(ctx, backupDialTimeout)
	client, conn, err := h.backupClient(dialCtx)
	dialCancel()
	if err != nil {
		return templates.BackupScheduleData{SoftEmpty: true}
	}
	defer func() { _ = conn.Close() }()
	readCtx, readCancel := context.WithTimeout(ctx, backupReadTimeout)
	cfg, err := client.GetScheduleConfig(readCtx, &backupv1.GetScheduleConfigRequest{})
	readCancel()
	if err != nil {
		return templates.BackupScheduleData{SoftEmpty: true, Error: err.Error()}
	}
	d := templates.BackupScheduleData{
		CronExpr:       cfg.GetCronExpr(),
		RetentionCount: int(cfg.GetRetentionCount()),
		RetentionDays:  int(cfg.GetRetentionDays()),
		Enabled:        cfg.GetEnabled(),
		LastRunStatus:  cfg.GetLastRunStatus(),
	}
	if t := cfg.GetNextRunUnix(); t > 0 {
		d.NextRun = time.Unix(t, 0).UTC().Format(time.RFC822)
	}
	if t := cfg.GetLastRunUnix(); t > 0 {
		d.LastRun = time.Unix(t, 0).UTC().Format(time.RFC822)
	}
	return d
}

func (h *Handler) BackupsScheduleSave(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	cronExpr := strings.TrimSpace(r.FormValue("cron_expr"))
	enabled := r.FormValue("enabled") == "1"
	retCount := int32(parseIntOr(r.FormValue("retention_count"), 0))
	retDays := int32(parseIntOr(r.FormValue("retention_days"), 0))

	pageCtx, cancel := context.WithTimeout(r.Context(), backupActionTimeout)
	defer cancel()
	dialCtx, dialCancel := context.WithTimeout(pageCtx, backupDialTimeout)
	client, conn, err := h.backupClient(dialCtx)
	dialCancel()
	if err != nil {
		http.Redirect(w, r, "/backups?ok="+urlQuery("backup-local unavailable: "+err.Error()), http.StatusSeeOther)
		return
	}
	defer func() { _ = conn.Close() }()
	readCtx, readCancel := context.WithTimeout(pageCtx, backupReadTimeout)
	_, err = client.SetScheduleConfig(readCtx, &backupv1.SetScheduleConfigRequest{
		CronExpr:       cronExpr,
		RetentionCount: retCount,
		RetentionDays:  retDays,
		Enabled:        enabled,
	})
	readCancel()
	if err != nil {
		http.Redirect(w, r, "/backups?ok="+urlQuery("schedule save failed: "+err.Error()), http.StatusSeeOther)
		return
	}
	if sess := SessionFromContext(r.Context()); sess != nil {
		h.auditLog(r.Context(), sess.UserID, "admin.backup.schedule.save", "backup-local", "schedule", map[string]string{
			"cron_expr":       cronExpr,
			"enabled":         fmt.Sprintf("%v", enabled),
			"retention_count": fmt.Sprintf("%d", retCount),
			"retention_days":  fmt.Sprintf("%d", retDays),
		})
	}
	http.Redirect(w, r, "/backups?ok=schedule+saved", http.StatusSeeOther)
}

func parseIntOr(s string, def int) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return def
	}
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil {
		return def
	}
	return n
}

func (h *Handler) listBackupRows(ctx context.Context) ([]templates.BackupRow, string) {
	dialCtx, dialCancel := context.WithTimeout(ctx, backupDialTimeout)
	client, conn, err := h.backupClient(dialCtx)
	dialCancel()
	if err != nil {
		return nil, err.Error()
	}
	defer func() { _ = conn.Close() }()
	readCtx, readCancel := context.WithTimeout(ctx, backupReadTimeout)
	resp, err := client.ListBackups(readCtx, &backupv1.ListBackupsRequest{})
	readCancel()
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
	pageCtx, cancel := context.WithTimeout(r.Context(), backupActionTimeout)
	defer cancel()
	dialCtx, dialCancel := context.WithTimeout(pageCtx, backupDialTimeout)
	client, conn, err := h.backupClient(dialCtx)
	dialCancel()
	if err != nil {
		http.Redirect(w, r, "/backups?ok="+urlQuery(err.Error()), http.StatusSeeOther)
		return
	}
	defer func() { _ = conn.Close() }()
	sourcePaths := h.libraryBackupSourcePaths(pageCtx)
	readCtx, readCancel := context.WithTimeout(pageCtx, backupReadTimeout)
	resp, err := client.CreateBackup(readCtx, &backupv1.CreateBackupRequest{SourcePaths: sourcePaths})
	readCancel()
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
	pageCtx, cancel := context.WithTimeout(r.Context(), backupActionTimeout)
	defer cancel()
	dialCtx, dialCancel := context.WithTimeout(pageCtx, backupDialTimeout)
	client, conn, err := h.backupClient(dialCtx)
	dialCancel()
	if err != nil {
		http.Redirect(w, r, "/backups?ok="+urlQuery(err.Error()), http.StatusSeeOther)
		return
	}
	defer func() { _ = conn.Close() }()
	readCtx, readCancel := context.WithTimeout(pageCtx, backupReadTimeout)
	_, err = client.DeleteBackup(readCtx, &backupv1.DeleteBackupRequest{BackupId: id})
	readCancel()
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
	pageCtx, cancel := context.WithTimeout(r.Context(), backupActionTimeout)
	defer cancel()
	dialCtx, dialCancel := context.WithTimeout(pageCtx, backupDialTimeout)
	client, conn, err := h.backupClient(dialCtx)
	dialCancel()
	if err != nil {
		http.Redirect(w, r, "/backups?ok="+urlQuery(err.Error()), http.StatusSeeOther)
		return
	}
	defer func() { _ = conn.Close() }()
	readCtx, readCancel := context.WithTimeout(pageCtx, backupReadTimeout)
	resp, err := client.RestoreBackup(readCtx, &backupv1.RestoreBackupRequest{BackupId: id, TargetPath: target})
	readCancel()
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
	ctx, cancel := context.WithTimeout(r.Context(), schedulerPageTimeout)
	defer cancel()
	rows, errMsg := h.listTaskRows(ctx)
	flash := r.URL.Query().Get("ok")
	content := templates.TasksLivePage(rows, errMsg, flash)
	nav := h.nav(r.URL.Path)
	h.render(w, r, templates.Layout("Scheduled Tasks", nav, content))
}

func (h *Handler) listTaskRows(ctx context.Context) ([]templates.TaskRow, string) {
	dialCtx, dialCancel := context.WithTimeout(ctx, schedulerDialTimeout)
	base, err := h.schedulerHTTPBase(dialCtx)
	dialCancel()
	if err != nil {
		return nil, err.Error()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/list", nil)
	if err != nil {
		return nil, err.Error()
	}
	resp, err := schedulerHTTPDo(ctx, req)
	if err != nil {
		return nil, err.Error()
	}
	defer func() { _ = resp.Body.Close() }()
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
	ctx, cancel := context.WithTimeout(r.Context(), schedulerDialTimeout+schedulerReadTimeout)
	defer cancel()
	id := r.PathValue("id")
	dialCtx, dialCancel := context.WithTimeout(ctx, schedulerDialTimeout)
	base, err := h.schedulerHTTPBase(dialCtx)
	dialCancel()
	if err != nil {
		http.Redirect(w, r, "/tasks?ok="+urlQuery(err.Error()), http.StatusSeeOther)
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, base+"/cancel/"+id, nil)
	if err != nil {
		http.Redirect(w, r, "/tasks?ok="+urlQuery(err.Error()), http.StatusSeeOther)
		return
	}
	resp, err := schedulerHTTPDo(ctx, req)
	msg := "cancelled " + id
	if err != nil {
		msg = err.Error()
	} else {
		defer func() { _ = resp.Body.Close() }()
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
	raw, err := os.ReadFile(networkingFilePath())
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
	path := networkingFilePath()
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	raw, err := json.MarshalIndent(n, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
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
	h.ApplyNetworkingRuntime(n.PublicURL, n.TrustedProxies)
	http.Redirect(w, r, "/networking?saved=1", http.StatusSeeOther)
}

// --- Parental controls (admin-local store until auth UserInfo grows fields) ---

type parentalSettings struct {
	MaxParentalRating string `json:"max_parental_rating"`
	BlockedTags       string `json:"blocked_tags"`
	AllowedTags       string `json:"allowed_tags"`
	AllowUnrated      bool   `json:"allow_unrated"`
	KidsMode          bool   `json:"kids_mode"`
	// PINHash is a salted SHA-256 hex digest; empty means no PIN is set.
	PINHash string `json:"pin_hash,omitempty"`
}

// hashParentalPIN returns a salted SHA-256 hex digest of a PIN.
// userID is the per-user salt so hashes are not reusable across accounts.
func hashParentalPIN(userID, pin string) string {
	h := sha256.Sum256([]byte(userID + ":" + pin))
	return hex.EncodeToString(h[:])
}

// validateParentalPIN returns an error if pin is not exactly 4–6 ASCII digits.
func validateParentalPIN(pin string) error {
	if len(pin) < 4 || len(pin) > 6 {
		return fmt.Errorf("PIN must be 4–6 digits")
	}
	for _, c := range pin {
		if c < '0' || c > '9' {
			return fmt.Errorf("PIN must contain digits only")
		}
	}
	return nil
}

// validateParentalRating returns an error for a malformed rating string.
// An empty string (no restriction) is always accepted.
func validateParentalRating(rating string) error {
	if rating == "" {
		return nil
	}
	if len(rating) > 20 {
		return fmt.Errorf("rating too long (max 20 characters)")
	}
	for _, c := range rating {
		if !unicode.IsLetter(c) && !unicode.IsDigit(c) && c != '-' && c != '+' && c != ' ' {
			return fmt.Errorf("invalid character %q in rating", c)
		}
	}
	return nil
}

func loadParentalMap() map[string]parentalSettings {
	parentalMu.Lock()
	defer parentalMu.Unlock()
	raw, err := os.ReadFile(parentalFilePath())
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
	path := parentalFilePath()
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (h *Handler) UsersParental(w http.ResponseWriter, r *http.Request) {
	userID := r.PathValue("id")
	m := loadParentalMap()
	p := m[userID]

	renderForm := func(saved bool, errMsg string) {
		_ = templates.UserParentalForm(templates.ParentalData{
			UserID:            userID,
			MaxParentalRating: p.MaxParentalRating,
			BlockedTags:       p.BlockedTags,
			AllowedTags:       p.AllowedTags,
			AllowUnrated:      p.AllowUnrated,
			KidsMode:          p.KidsMode,
			PINSet:            p.PINHash != "",
			Saved:             saved,
			Error:             errMsg,
		}).Render(r.Context(), w)
	}

	if r.Method != http.MethodPost {
		renderForm(false, "")
		return
	}

	_ = r.ParseForm()

	rating := strings.TrimSpace(r.FormValue("max_rating"))
	if err := validateParentalRating(rating); err != nil {
		renderForm(false, err.Error())
		return
	}

	updated := parentalSettings{
		MaxParentalRating: rating,
		BlockedTags:       strings.TrimSpace(r.FormValue("blocked_tags")),
		AllowedTags:       strings.TrimSpace(r.FormValue("allowed_tags")),
		AllowUnrated:      r.FormValue("allow_unrated") == "1",
		KidsMode:          r.FormValue("kids_mode") == "1",
		PINHash:           p.PINHash, // preserve existing PIN by default
	}

	if r.FormValue("clear_pin") == "1" {
		updated.PINHash = ""
	} else if newPIN := strings.TrimSpace(r.FormValue("pin")); newPIN != "" {
		if err := validateParentalPIN(newPIN); err != nil {
			renderForm(false, err.Error())
			return
		}
		updated.PINHash = hashParentalPIN(userID, newPIN)
	}

	p = updated
	m[userID] = p
	if err := saveParentalMap(m); err != nil {
		renderForm(false, err.Error())
		return
	}
	if err := h.syncParentalToUserdata(r.Context(), userID, p); err != nil {
		renderForm(false, "userdata sync failed: "+err.Error())
		return
	}

	if sess := SessionFromContext(r.Context()); sess != nil {
		h.auditLog(r.Context(), sess.UserID, "admin.parental.save", "user", userID, map[string]string{
			"max_rating": p.MaxParentalRating,
			"kids_mode":  fmt.Sprintf("%v", p.KidsMode),
			"pin_set":    fmt.Sprintf("%v", p.PINHash != ""),
		})
	}

	renderForm(true, "")
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
	raw, err := os.ReadFile(livetvFilePath())
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
	path := livetvFilePath()
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	raw, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
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
	slog.Info("livetv channels saved", "count", len(f.Channels), "path", livetvFilePath())
	http.Redirect(w, r, "/livetv?saved=1", http.StatusSeeOther)
}

// --- Libraries / Playback / Plugins (live) ---

var playbackMu sync.Mutex

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
	raw, err := os.ReadFile(playbackFilePath())
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
	path := playbackFilePath()
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	raw, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

const (
	capTranscoder         = "transcoder"
	capMediaTranscode     = "media.transcoder"
	playbackDialTimeout   = 3 * time.Second
	playbackReadTimeout   = 5 * time.Second
	playbackPageTimeout   = playbackDialTimeout + playbackReadTimeout + time.Second
	playbackActionTimeout = playbackDialTimeout + playbackReadTimeout + time.Second
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
	dialCtx, dialCancel := context.WithTimeout(ctx, playbackDialTimeout)
	id, addr, ok := h.resolveTranscoder(dialCtx)
	dialCancel()
	if !ok {
		return "", "", false
	}
	readCtx, readCancel := context.WithTimeout(ctx, playbackReadTimeout)
	raw, err := h.settingsMeshCall(readCtx, id, addr, methodGet, nil)
	readCancel()
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
	dialCtx, dialCancel := context.WithTimeout(ctx, playbackDialTimeout)
	id, addr, ok := h.resolveTranscoder(dialCtx)
	dialCancel()
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
	readCtx, readCancel := context.WithTimeout(ctx, playbackReadTimeout)
	_, err = h.settingsMeshCall(readCtx, id, addr, methodUpdate, payload)
	readCancel()
	return id, err
}

func (h *Handler) PlaybackAdminPage(w http.ResponseWriter, r *http.Request) {
	pageCtx, pageCancel := context.WithTimeout(r.Context(), playbackPageTimeout)
	defer pageCancel()

	p := loadPlayback()
	tid, ffmpeg, present := h.loadTranscoderFFmpeg(pageCtx)
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
	pageCtx, pageCancel := context.WithTimeout(r.Context(), playbackActionTimeout)
	defer pageCancel()
	if _, err := h.pushTranscoderFFmpeg(pageCtx, p.FFmpegBin); err != nil {
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
	pageCtx, pageCancel := context.WithTimeout(r.Context(), metadataPageTimeout)
	defer pageCancel()

	dialCtx, dialCancel := context.WithTimeout(pageCtx, metadataDialTimeout)
	h.refreshMediaNavLinks(dialCtx)
	dialCancel()

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
	pageCtx, pageCancel := context.WithTimeout(r.Context(), moduleListPageTimeout)
	defer pageCancel()
	mods := h.loadModuleList(pageCtx)
	content := templates.PluginsCatalogPage(mods)
	nav := h.nav(r.URL.Path)
	h.render(w, r, templates.Layout("Plugins", nav, content))
}

func (h *Handler) MetadataManagerPage(w http.ResponseWriter, r *http.Request) {
	pageCtx, pageCancel := context.WithTimeout(r.Context(), metadataPageTimeout)
	defer pageCancel()

	dialCtx, dialCancel := context.WithTimeout(pageCtx, metadataDialTimeout)
	h.refreshMediaNavLinks(dialCtx)
	dialCancel()

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
	pageCtx, pageCancel := context.WithTimeout(r.Context(), moduleListPageTimeout)
	defer pageCancel()
	all := h.loadModuleList(pageCtx)
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
