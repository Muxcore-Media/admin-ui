package handler

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	templates "github.com/Muxcore-Media/admin-ui/templ"
	authv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/auth/v1"
)

type passwordResetFile struct {
	Requests []passwordResetEntry `json:"requests"`
}

type passwordResetEntry struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	// UserID is the account id the username resolved to when the request was
	// made (ADR-0035 §3). Erasure removes entries by this id, never by
	// username. Entries written before this field existed, or by a writer that
	// could not resolve the username, leave it empty ("legacy"): erasure purges
	// a legacy entry only once its username no longer resolves.
	UserID    string    `json:"user_id,omitempty"`
	Note      string    `json:"note"`
	CreatedAt time.Time `json:"created_at"`
	Status    string    `json:"status"`
}

type passwordResetLoadResult struct {
	Rows      []templates.PasswordResetRow
	Error     string
	SoftEmpty bool
}

var passwordResetMu sync.Mutex

func passwordResetPath() string {
	return passwordResetFilePath()
}

func readPasswordResetFile() (passwordResetFile, error) {
	raw, err := os.ReadFile(passwordResetPath())
	if err != nil {
		if os.IsNotExist(err) {
			return passwordResetFile{}, nil
		}
		return passwordResetFile{}, err
	}
	var f passwordResetFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return passwordResetFile{}, err
	}
	return f, nil
}

func writePasswordResetFile(f passwordResetFile) error {
	passwordResetMu.Lock()
	defer passwordResetMu.Unlock()
	return writePasswordResetFileLocked(f)
}

// writePasswordResetFileLocked atomically replaces the file (temp + rename).
// The caller holds passwordResetMu.
func writePasswordResetFileLocked(f passwordResetFile) error {
	path := passwordResetPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
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

func mapUsersByUsername(users []*authv1.UserInfo) map[string]string {
	m := make(map[string]string)
	if users == nil {
		return m
	}
	for _, u := range users {
		name := u.GetUsername()
		if name == "" {
			continue
		}
		if _, exists := m[name]; !exists {
			m[name] = u.GetId()
		}
	}
	return m
}

func loadPasswordResetQueue(users []*authv1.UserInfo) passwordResetLoadResult {
	f, err := readPasswordResetFile()
	if err != nil {
		return passwordResetLoadResult{Error: "Could not read password reset requests."}
	}
	userByName := mapUsersByUsername(users)
	var out []templates.PasswordResetRow
	for _, e := range f.Requests {
		if e.Status != "" && e.Status != "pending" {
			continue
		}
		if e.Username == "" {
			continue
		}
		created := ""
		if !e.CreatedAt.IsZero() {
			created = e.CreatedAt.UTC().Format(time.RFC822)
		}
		out = append(out, templates.PasswordResetRow{
			ID:        e.ID,
			Username:  e.Username,
			Note:      e.Note,
			CreatedAt: created,
			UserID:    userByName[e.Username],
		})
	}
	if len(out) == 0 {
		return passwordResetLoadResult{SoftEmpty: true}
	}
	return passwordResetLoadResult{Rows: out}
}

func pendingPasswordResetCount() int {
	return len(loadPasswordResetQueue(nil).Rows)
}

func dismissPasswordResetRequest(id string) error {
	if id == "" {
		return errors.New("request id required")
	}
	f, err := readPasswordResetFile()
	if err != nil {
		return err
	}
	found := false
	for i := range f.Requests {
		if f.Requests[i].ID != id {
			continue
		}
		if f.Requests[i].Status == "" || f.Requests[i].Status == "pending" {
			f.Requests[i].Status = "dismissed"
			found = true
		}
		break
	}
	if !found {
		return errors.New("pending request not found")
	}
	return writePasswordResetFile(f)
}

func resolvePasswordResetByUsername(username string) error {
	if username == "" {
		return nil
	}
	f, err := readPasswordResetFile()
	if err != nil {
		return err
	}
	changed := false
	for i := range f.Requests {
		if f.Requests[i].Username != username {
			continue
		}
		if f.Requests[i].Status == "" || f.Requests[i].Status == "pending" {
			f.Requests[i].Status = "resolved"
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return writePasswordResetFile(f)
}

func passwordResetEntryByID(id string) (passwordResetEntry, bool, error) {
	f, err := readPasswordResetFile()
	if err != nil {
		return passwordResetEntry{}, false, err
	}
	for _, e := range f.Requests {
		if e.ID != id {
			continue
		}
		if e.Status != "" && e.Status != "pending" {
			return passwordResetEntry{}, false, nil
		}
		return e, true, nil
	}
	return passwordResetEntry{}, false, nil
}
