package handler

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

type passwordResetFile struct {
	Requests []struct {
		ID        string    `json:"id"`
		Username  string    `json:"username"`
		Note      string    `json:"note"`
		CreatedAt time.Time `json:"created_at"`
		Status    string    `json:"status"`
	} `json:"requests"`
}

func passwordResetPath() string {
	return envOr("ADMIN_UI_PASSWORD_RESET_FILE", filepath.Join(os.TempDir(), "muxcore-password-resets.json"))
}

func loadPasswordResetRequests() []templates.PasswordResetRow {
	raw, err := os.ReadFile(passwordResetPath())
	if err != nil {
		return nil
	}
	var f passwordResetFile
	if json.Unmarshal(raw, &f) != nil {
		return nil
	}
	var out []templates.PasswordResetRow
	for _, e := range f.Requests {
		if e.Status != "" && e.Status != "pending" {
			continue
		}
		if e.Username == "" {
			continue
		}
		out = append(out, templates.PasswordResetRow{
			ID:        e.ID,
			Username:  e.Username,
			Note:      e.Note,
			CreatedAt: e.CreatedAt.UTC().Format(time.RFC822),
		})
	}
	return out
}
