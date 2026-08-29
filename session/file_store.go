package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

type persistedSessions struct {
	Sessions map[string]*Session `json:"sessions"`
}

func loadSessionsFromFile(path string) map[string]*Session {
	raw, err := os.ReadFile(path)
	if err != nil {
		return make(map[string]*Session)
	}
	var f persistedSessions
	if json.Unmarshal(raw, &f) != nil || f.Sessions == nil {
		return make(map[string]*Session)
	}
	now := time.Now()
	out := make(map[string]*Session, len(f.Sessions))
	for tok, sess := range f.Sessions {
		if sess != nil && now.Before(sess.ExpiresAt) {
			out[tok] = sess
		}
	}
	return out
}

func (s *Store) persist() error {
	if s.filePath == "" {
		return nil
	}
	s.mu.RLock()
	snapshot := make(map[string]*Session, len(s.sessions))
	for k, v := range s.sessions {
		snapshot[k] = v
	}
	s.mu.RUnlock()

	f := persistedSessions{Sessions: snapshot}
	raw, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.filePath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp := s.filePath + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.filePath)
}

// NewFileStore returns a session store persisted to path (atomic JSON writes).
func NewFileStore(path string, ttl time.Duration) *Store {
	s := &Store{
		sessions: loadSessionsFromFile(path),
		ttl:      ttl,
		filePath: path,
	}
	go s.cleanupLoop()
	return s
}
