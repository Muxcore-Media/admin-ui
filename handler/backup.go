package handler

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"google.golang.org/grpc"

	backupv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/backup/v1"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

const capBackup = "backup"

func (h *Handler) dialBackup(ctx *http.Request) (*grpc.ClientConn, backupv1.BackupServiceClient, error) {
	mod, err := h.Core.Discovery.Resolve(ctx.Context(), "backup-local")
	if err != nil {
		mods, err2 := h.Core.Discovery.FindByCapability(ctx.Context(), capBackup)
		if err2 != nil || len(mods) == 0 {
			return nil, nil, err
		}
		conn, err3 := h.cachedConn(ctx.Context(), mods[0].GetHttpAddr())
		if err3 != nil {
			return nil, nil, err3
		}
		return conn, backupv1.NewBackupServiceClient(conn), nil
	}
	conn, err := h.cachedConn(ctx.Context(), mod.GetHttpAddr())
	if err != nil {
		return nil, nil, err
	}
	return conn, backupv1.NewBackupServiceClient(conn), nil
}

func formatBackupTime(unix int64) string {
	t := time.Unix(unix, 0)
	return t.Format("Jan 2 15:04")
}

func formatBackupSize(bytes int64) string {
	switch {
	case bytes >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(bytes)/float64(1<<30))
	case bytes >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(bytes)/float64(1<<20))
	case bytes >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(bytes)/float64(1<<10))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

func toBackupItems(proto []*backupv1.BackupInfo) []templates.BackupItem {
	if proto == nil {
		return nil
	}
	items := make([]templates.BackupItem, 0, len(proto))
	for _, b := range proto {
		items = append(items, templates.BackupItem{
			ID:          b.GetId(),
			Timestamp:   b.GetTimestampUnix(),
			SizeBytes:   b.GetSizeBytes(),
			ModuleIDs:   b.GetModuleIds(),
			DisplayTime: formatBackupTime(b.GetTimestampUnix()),
			DisplaySize: formatBackupSize(b.GetSizeBytes()),
		})
	}
	return items
}

func (h *Handler) BackupsPage(w http.ResponseWriter, r *http.Request) {
	_, client, err := h.dialBackup(r)
	if err != nil {
		slog.Warn("backup: dial failed", "error", err)
		content := templates.BackupsPage(nil)
		nav := h.nav(r.URL.Path)
		component := templates.Layout("Backups", nav, content)
		h.render(w, r, component)
		return
	}

	resp, err := client.ListBackups(r.Context(), &backupv1.ListBackupsRequest{})
	if err != nil {
		slog.Warn("backup: ListBackups failed", "error", err)
		content := templates.BackupsPage(nil)
		nav := h.nav(r.URL.Path)
		component := templates.Layout("Backups", nav, content)
		h.render(w, r, component)
		return
	}

	items := toBackupItems(resp.GetBackups())
	content := templates.BackupsPage(items)
	nav := h.nav(r.URL.Path)
	component := templates.Layout("Backups", nav, content)
	h.render(w, r, component)
}

func (h *Handler) BackupCreate(w http.ResponseWriter, r *http.Request) {
	_, client, err := h.dialBackup(r)
	if err != nil {
		toast(w, "error", "Backup module unavailable")
		h.renderBackupTable(w, r)
		return
	}

	_, err = client.CreateBackup(r.Context(), &backupv1.CreateBackupRequest{})
	if err != nil {
		slog.Warn("backup: CreateBackup failed", "error", err)
		toast(w, "error", "Backup failed: "+err.Error())
		h.renderBackupTable(w, r)
		return
	}

	h.auditLog(r.Context(), SessionFromContext(r.Context()).Username, "backup.create", "backup", "", nil)
	toast(w, "success", "Backup created")
	h.renderBackupTable(w, r)
}

func (h *Handler) BackupRestore(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	_, client, err := h.dialBackup(r)
	if err != nil {
		toast(w, "error", "Backup module unavailable")
		return
	}

	_, err = client.RestoreBackup(r.Context(), &backupv1.RestoreBackupRequest{BackupId: id})
	if err != nil {
		slog.Warn("backup: RestoreBackup failed", "id", id, "error", err)
		toast(w, "error", "Restore failed: "+err.Error())
		return
	}

	h.auditLog(r.Context(), SessionFromContext(r.Context()).Username, "backup.restore", "backup", id, nil)
	toast(w, "success", "Backup restored")
	h.renderBackupTable(w, r)
}

func (h *Handler) BackupDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	_, client, err := h.dialBackup(r)
	if err != nil {
		toast(w, "error", "Backup module unavailable")
		return
	}

	_, err = client.DeleteBackup(r.Context(), &backupv1.DeleteBackupRequest{BackupId: id})
	if err != nil {
		slog.Warn("backup: DeleteBackup failed", "id", id, "error", err)
		toast(w, "error", "Delete failed: "+err.Error())
		return
	}

	h.auditLog(r.Context(), SessionFromContext(r.Context()).Username, "backup.delete", "backup", id, nil)
	toast(w, "success", "Backup deleted")
	w.Write([]byte(``))
}

func (h *Handler) renderBackupTable(w http.ResponseWriter, r *http.Request) {
	_, client, err := h.dialBackup(r)
	if err != nil {
		w.Write([]byte(`<div class="text-sm text-red-400">Backup module unavailable</div>`))
		return
	}

	resp, err := client.ListBackups(r.Context(), &backupv1.ListBackupsRequest{})
	if err != nil {
		w.Write([]byte(`<div class="text-sm text-red-400">Failed to list backups</div>`))
		return
	}

	items := toBackupItems(resp.GetBackups())
	component := templates.BackupTable(items)
	h.render(w, r, component)
}
