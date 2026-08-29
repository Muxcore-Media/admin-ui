package handler

import (
	"os"
	"path/filepath"
)

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// AdminDataDir is the root for durable admin-ui JSON state (branding, networking, sessions, …).
func AdminDataDir() string {
	if v := os.Getenv("ADMIN_UI_DATA_DIR"); v != "" {
		return v
	}
	if v := os.Getenv("MEDIA_UI_USERDATA_DIR"); v != "" {
		return v
	}
	return filepath.Join(os.TempDir(), "muxcore-admin-ui")
}

func adminDataFile(envKey, basename string) string {
	return envOr(envKey, filepath.Join(AdminDataDir(), basename))
}

func brandingFilePath() string {
	return adminDataFile("ADMIN_UI_BRANDING_FILE", "branding.json")
}

func networkingFilePath() string {
	return adminDataFile("ADMIN_UI_NETWORKING_FILE", "networking.json")
}

func parentalFilePath() string {
	return adminDataFile("ADMIN_UI_PARENTAL_FILE", "parental.json")
}

func livetvFilePath() string {
	return adminDataFile("ADMIN_UI_LIVETV_FILE", "livetv.json")
}

func playbackFilePath() string {
	return adminDataFile("ADMIN_UI_PLAYBACK_FILE", "playback.json")
}

func passwordResetFilePath() string {
	return adminDataFile("ADMIN_UI_PASSWORD_RESET_FILE", "password-resets.json")
}

func spoolTrustDir() string {
	return envOr("ADMIN_UI_SPOOL_TRUST_DIR", filepath.Join(AdminDataDir(), "spool-trust"))
}

func SessionFilePath() string {
	return adminDataFile("ADMIN_UI_SESSION_FILE", "sessions.json")
}

func logDirPath() string {
	return envOr("ADMIN_UI_LOG_DIR", envOr("MVP_RUN", filepath.Join(AdminDataDir(), "logs")))
}
