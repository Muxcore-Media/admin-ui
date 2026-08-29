package handler

import (
	"path/filepath"
	"testing"
)

func TestAdminDataFileDefaults(t *testing.T) {
	t.Setenv("ADMIN_UI_DATA_DIR", "/data/admin-ui")
	t.Setenv("ADMIN_UI_BRANDING_FILE", "")
	t.Setenv("ADMIN_UI_NETWORKING_FILE", "")
	t.Setenv("ADMIN_UI_PASSWORD_RESET_FILE", "")

	if got := brandingFilePath(); got != filepath.Join("/data/admin-ui", "branding.json") {
		t.Fatalf("brandingFilePath() = %q", got)
	}
	if got := networkingFilePath(); got != filepath.Join("/data/admin-ui", "networking.json") {
		t.Fatalf("networkingFilePath() = %q", got)
	}
	if got := passwordResetFilePath(); got != filepath.Join("/data/admin-ui", "password-resets.json") {
		t.Fatalf("passwordResetFilePath() = %q", got)
	}
}

func TestAdminDataDirFallsBackToMediaUIUserdata(t *testing.T) {
	t.Setenv("ADMIN_UI_DATA_DIR", "")
	t.Setenv("MEDIA_UI_USERDATA_DIR", "/data/media-ui")
	if got := AdminDataDir(); got != "/data/media-ui" {
		t.Fatalf("AdminDataDir() = %q, want /data/media-ui", got)
	}
}
