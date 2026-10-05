package session

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
	"time"
)

type Session struct {
	UserID         string
	Username       string
	Roles          []string
	Permissions    []string
	TenantID       string
	AuthLocalToken string
	Label          string
	CreatedAt      time.Time
	ExpiresAt      time.Time
}

// Store keeps admin sessions keyed by the SHA-256 of the bearer token (the
// session ID). Raw tokens are only ever held by the client cookie; neither the
// in-memory map nor the persisted file contains them (NFR-SEC-005).
type Store struct {
	mu       sync.RWMutex
	sessions map[string]*Session // key: ID(token)
	ttl      time.Duration
	filePath string
	aead     cipherAEAD
}

func NewStore(ttl time.Duration) *Store {
	s := &Store{
		sessions: make(map[string]*Session),
		ttl:      ttl,
	}
	go s.cleanupLoop()
	return s
}

// ID returns the stable, non-reversible identifier for a session token. It is
// safe to render in UIs and to persist.
func ID(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func (s *Store) TTL() time.Duration {
	return s.ttl
}

func (s *Store) Create(userID, username string, roles, permissions []string) (string, error) {
	return s.CreateWithTenant(userID, username, "", roles, permissions)
}

// CreateWithTenant stores a session bound to tenantID (forwarded as X-Tenant-ID by BFFs).
func (s *Store) CreateWithTenant(userID, username, tenantID string, roles, permissions []string) (string, error) {
	now := time.Now()
	sess := &Session{
		UserID:      userID,
		Username:    username,
		Roles:       roles,
		Permissions: permissions,
		TenantID:    tenantID,
		CreatedAt:   now,
		ExpiresAt:   now.Add(s.ttl),
	}
	token, err := newToken()
	if err != nil {
		return "", err
	}

	s.mu.Lock()
	s.sessions[ID(token)] = sess
	s.mu.Unlock()
	_ = s.persist()

	return token, nil
}

func (s *Store) Get(token string) (*Session, bool) {
	if token == "" {
		return nil, false
	}
	id := ID(token)
	s.mu.RLock()
	sess, ok := s.sessions[id]
	s.mu.RUnlock()

	if !ok {
		return nil, false
	}

	if time.Now().After(sess.ExpiresAt) {
		s.mu.Lock()
		delete(s.sessions, id)
		s.mu.Unlock()
		return nil, false
	}

	return sess, true
}

func (s *Store) BindAuthLocalToken(adminSessionToken, authLocalToken string) {
	authLocalToken = strings.TrimSpace(authLocalToken)
	if authLocalToken == "" {
		return
	}
	s.mu.Lock()
	if sess, ok := s.sessions[ID(adminSessionToken)]; ok {
		sess.AuthLocalToken = authLocalToken
	}
	s.mu.Unlock()
	_ = s.persist()
}

// Revoke removes the session for a raw bearer token (logout).
func (s *Store) Revoke(token string) {
	s.RevokeByID(ID(token))
}

// RevokeByID removes the session with the given ID (see ID / SessionInfo.ID).
func (s *Store) RevokeByID(id string) {
	s.mu.Lock()
	delete(s.sessions, id)
	s.mu.Unlock()
	_ = s.persist()
}

// RenameSession sets a human-readable label on the session for a raw token.
func (s *Store) RenameSession(token, label string) bool {
	return s.RenameByID(ID(token), label)
}

// RenameByID sets a human-readable label on an active session so operators can
// identify devices without revoking them. A blank label clears the existing one.
func (s *Store) RenameByID(id, label string) bool {
	s.mu.Lock()
	sess, ok := s.sessions[id]
	if ok {
		sess.Label = strings.TrimSpace(label)
	}
	s.mu.Unlock()
	if ok {
		_ = s.persist()
	}
	return ok
}

func (s *Store) GetFromRequest(r *http.Request) (*Session, bool) {
	cookie, err := r.Cookie("session")
	if err != nil {
		return nil, false
	}
	return s.Get(cookie.Value)
}

func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.sessions)
}

// SessionInfo is a public view of an active admin session. ID is the session
// identifier (hash of the bearer), never the bearer itself.
type SessionInfo struct {
	ID        string
	UserID    string
	Username  string
	Label     string
	Roles     []string
	CreatedAt time.Time
	ExpiresAt time.Time
}

func (s *Store) List() []SessionInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]SessionInfo, 0, len(s.sessions))
	now := time.Now()
	for id, sess := range s.sessions {
		if now.After(sess.ExpiresAt) {
			continue
		}
		out = append(out, SessionInfo{
			ID:        id,
			UserID:    sess.UserID,
			Username:  sess.Username,
			Label:     sess.Label,
			Roles:     append([]string(nil), sess.Roles...),
			CreatedAt: sess.CreatedAt,
			ExpiresAt: sess.ExpiresAt,
		})
	}
	return out
}

func (s *Store) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		now := time.Now()
		s.mu.Lock()
		for id, sess := range s.sessions {
			if now.After(sess.ExpiresAt) {
				delete(s.sessions, id)
			}
		}
		s.mu.Unlock()
		_ = s.persist()
	}
}
