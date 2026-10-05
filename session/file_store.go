package session

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type cipherAEAD = cipher.AEAD

// EnvSessionKey optionally supplies the session encryption key (32 bytes as
// base64 or hex; any other non-empty value is stretched with SHA-256). When
// unset, a random key is generated once and persisted (0600) next to the
// session file as KeyFileName.
const (
	EnvSessionKey = "ADMIN_UI_SESSION_KEY"
	KeyFileName   = "session.key"
)

// fileFormatVersion 2: map keyed by session ID (SHA-256 of the bearer); the
// auth-local bearer is AES-256-GCM encrypted with the ID as associated data.
// Version 1 (no "version" field, keyed by raw bearer, plaintext) is discarded.
const fileFormatVersion = 2

type persistedSession struct {
	UserID            string    `json:"user_id"`
	Username          string    `json:"username"`
	Roles             []string  `json:"roles,omitempty"`
	Permissions       []string  `json:"permissions,omitempty"`
	TenantID          string    `json:"tenant_id,omitempty"`
	AuthLocalTokenEnc string    `json:"auth_local_token_enc,omitempty"`
	Label             string    `json:"label,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	ExpiresAt         time.Time `json:"expires_at"`
}

type persistedSessions struct {
	Version  int                          `json:"version"`
	Sessions map[string]*persistedSession `json:"sessions"`
}

// ParseKey converts an ADMIN_UI_SESSION_KEY value into a 32-byte AES key.
func ParseKey(v string) ([]byte, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil, errors.New("session: empty key")
	}
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(v); err == nil && len(b) == 32 {
			return b, nil
		}
	}
	if b, err := hex.DecodeString(v); err == nil && len(b) == 32 {
		return b, nil
	}
	sum := sha256.Sum256([]byte(v))
	return sum[:], nil
}

// LoadOrCreateKey returns the key from envValue when set, otherwise reads (or
// generates and writes, mode 0600) the key file at keyPath.
func LoadOrCreateKey(envValue, keyPath string) ([]byte, error) {
	if strings.TrimSpace(envValue) != "" {
		return ParseKey(envValue)
	}
	raw, err := os.ReadFile(keyPath) //nolint:gosec // path under operator data dir
	if err == nil {
		key, perr := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
		if perr != nil || len(key) != 32 {
			return nil, fmt.Errorf("session: invalid key file %s", keyPath)
		}
		_ = os.Chmod(keyPath, 0o600)
		return key, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("session: read key file: %w", err)
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
		return nil, fmt.Errorf("session: create key dir: %w", err)
	}
	f, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // path under operator data dir
	if err != nil {
		if errors.Is(err, fs.ErrExist) { // lost a race with another process
			return LoadOrCreateKey("", keyPath)
		}
		return nil, fmt.Errorf("session: write key file: %w", err)
	}
	_, werr := f.WriteString(base64.StdEncoding.EncodeToString(key) + "\n")
	cerr := f.Close()
	if werr != nil || cerr != nil {
		_ = os.Remove(keyPath)
		return nil, fmt.Errorf("session: write key file: %w", errors.Join(werr, cerr))
	}
	return key, nil
}

func newAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func (s *Store) seal(id, plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	ct := s.aead.Seal(nonce, nonce, []byte(plaintext), []byte(id))
	return base64.StdEncoding.EncodeToString(ct), nil
}

func (s *Store) open(id, enc string) (string, error) {
	if enc == "" {
		return "", nil
	}
	raw, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		return "", err
	}
	ns := s.aead.NonceSize()
	if len(raw) < ns {
		return "", errors.New("session: ciphertext too short")
	}
	pt, err := s.aead.Open(nil, raw[:ns], raw[ns:], []byte(id))
	if err != nil {
		return "", err
	}
	return string(pt), nil
}

// load reads persisted sessions. Legacy (v1, plaintext) files and sessions
// whose bearer cannot be decrypted (key rotated) are dropped: users log in
// again. Returns true when the file had to be discarded/rewritten.
func (s *Store) load() bool {
	raw, err := os.ReadFile(s.filePath)
	if err != nil {
		return false
	}
	var f persistedSessions
	if json.Unmarshal(raw, &f) != nil || f.Version != fileFormatVersion {
		slog.Info("discarding legacy/unreadable admin session file; users must sign in again", "path", s.filePath)
		return true
	}
	now := time.Now()
	dropped := false
	for id, p := range f.Sessions {
		if p == nil || !now.Before(p.ExpiresAt) {
			dropped = true
			continue
		}
		tok, err := s.open(id, p.AuthLocalTokenEnc)
		if err != nil {
			dropped = true
			continue
		}
		s.sessions[id] = &Session{
			UserID:         p.UserID,
			Username:       p.Username,
			Roles:          p.Roles,
			Permissions:    p.Permissions,
			TenantID:       p.TenantID,
			AuthLocalToken: tok,
			Label:          p.Label,
			CreatedAt:      p.CreatedAt,
			ExpiresAt:      p.ExpiresAt,
		}
	}
	return dropped
}

func (s *Store) persist() error {
	if s.filePath == "" {
		return nil
	}
	f := persistedSessions{Version: fileFormatVersion, Sessions: make(map[string]*persistedSession)}
	s.mu.RLock()
	for id, v := range s.sessions {
		enc, err := s.seal(id, v.AuthLocalToken)
		if err != nil {
			s.mu.RUnlock()
			return err
		}
		f.Sessions[id] = &persistedSession{
			UserID:            v.UserID,
			Username:          v.Username,
			Roles:             v.Roles,
			Permissions:       v.Permissions,
			TenantID:          v.TenantID,
			AuthLocalTokenEnc: enc,
			Label:             v.Label,
			CreatedAt:         v.CreatedAt,
			ExpiresAt:         v.ExpiresAt,
		}
	}
	raw, err := json.MarshalIndent(f, "", "  ")
	s.mu.RUnlock()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.filePath), 0o700); err != nil {
		return err
	}
	tmp := s.filePath + ".tmp"
	_ = os.Remove(tmp) // ensure the 0600 mode below applies to a fresh file
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.filePath)
}

// NewFileStoreWithKey returns a session store persisted to path (atomic JSON
// writes, file 0600, dir 0700) with bearer material protected by key.
func NewFileStoreWithKey(path string, ttl time.Duration, key []byte) (*Store, error) {
	aead, err := newAEAD(key)
	if err != nil {
		return nil, fmt.Errorf("session: key: %w", err)
	}
	s := &Store{
		sessions: make(map[string]*Session),
		ttl:      ttl,
		filePath: path,
		aead:     aead,
	}
	if s.load() {
		if err := s.persist(); err != nil {
			slog.Warn("rewrite admin session file", "path", path, "error", err)
		}
	}
	go s.cleanupLoop()
	return s, nil
}

// NewFileStore returns a persisted session store whose key comes from
// ADMIN_UI_SESSION_KEY or KeyFileName next to path. If the key cannot be
// obtained, it falls back to an in-memory store (never plaintext on disk).
func NewFileStore(path string, ttl time.Duration) *Store {
	key, err := LoadOrCreateKey(os.Getenv(EnvSessionKey), filepath.Join(filepath.Dir(path), KeyFileName))
	if err == nil {
		var s *Store
		if s, err = NewFileStoreWithKey(path, ttl, key); err == nil {
			return s
		}
	}
	slog.Error("admin session persistence disabled; sessions are memory-only", "path", path, "error", err)
	return NewStore(ttl)
}
