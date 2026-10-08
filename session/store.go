package session

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"slices"
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

// Get returns a deep copy of the live session, so callers may read its fields
// without racing claim updates from CommitValidatedClaims. An expired session
// is removed.
func (s *Store) Get(token string) (*Session, bool) {
	if token == "" {
		return nil, false
	}
	id := ID(token)
	s.mu.RLock()
	sess, ok := s.sessions[id]
	var cp Session
	expired := ok && time.Now().After(sess.ExpiresAt)
	if ok && !expired {
		cp = sess.clone()
	}
	s.mu.RUnlock()

	if !ok {
		return nil, false
	}

	if expired {
		s.mu.Lock()
		delete(s.sessions, id)
		s.mu.Unlock()
		return nil, false
	}

	return &cp, true
}

// CommitResult reports whether current provider claims were stored on the
// local session that was validated.
type CommitResult int

const (
	// CommitApplied means the stored session still matched the validated bearer
	// and user, and its public claims were replaced.
	CommitApplied CommitResult = iota
	// CommitGone means the local session disappeared or expired during validation.
	CommitGone
	// CommitRebound means the stored provider bearer changed during validation.
	CommitRebound
	// CommitIdentityMismatch means the stored user is not the validated user.
	// Claims are left unchanged.
	CommitIdentityMismatch
)

func (sess *Session) clone() Session {
	if sess == nil {
		return Session{}
	}
	out := *sess
	out.Roles = append([]string(nil), sess.Roles...)
	out.Permissions = append([]string(nil), sess.Permissions...)
	return out
}

// Snapshot returns a deep copy of the live session. Callers can retain it for
// the rest of a request without observing later store mutations, and mutating
// the copy does not change the store. An expired session is removed.
func (s *Store) Snapshot(token string) (Session, bool) {
	if s == nil || token == "" {
		return Session{}, false
	}
	id := ID(token)
	s.mu.Lock()
	sess, ok := s.sessions[id]
	expired := ok && time.Now().After(sess.ExpiresAt)
	if expired {
		delete(s.sessions, id)
	}
	var snap Session
	if ok && !expired {
		snap = sess.clone()
	}
	s.mu.Unlock()
	if expired {
		_ = s.persist()
	}
	if !ok || expired {
		return Session{}, false
	}
	return snap, true
}

// CommitValidatedClaims records current public claims for a provider bearer.
// It does not recreate a session revoked while validation was in flight, does
// not apply claims when the stored bearer was replaced, and does not rebind a
// session to a different user. Permissions and the provider bearer are kept.
// On CommitApplied the returned session is a deep copy taken under the same
// lock as the write, so concurrent requests never observe each other's claims.
// The file is rewritten only when username, tenant or roles actually changed.
//
// TenantID is replaced with the tenant passed in (the provider's Validate
// tenant_id). Login falls back to the token's "tenant_id" claim when the
// response carries none, so a provider that reports the tenant only as a claim
// blanks the stored tenant on the first revalidation.
func (s *Store) CommitValidatedClaims(adminToken, bearer, userID, username, tenantID string, roles []string) (Session, CommitResult) {
	if s == nil || adminToken == "" {
		return Session{}, CommitGone
	}
	id := ID(adminToken)
	s.mu.Lock()
	sess, ok := s.sessions[id]
	if !ok || time.Now().After(sess.ExpiresAt) {
		if ok {
			delete(s.sessions, id)
		}
		s.mu.Unlock()
		if ok {
			_ = s.persist()
		}
		return Session{}, CommitGone
	}
	if sess.AuthLocalToken != bearer {
		s.mu.Unlock()
		return Session{}, CommitRebound
	}
	if userID == "" || sess.UserID != userID {
		s.mu.Unlock()
		return Session{}, CommitIdentityMismatch
	}
	changed := sess.Username != username || sess.TenantID != tenantID || !slices.Equal(sess.Roles, roles)
	if changed {
		sess.Username = username
		sess.TenantID = tenantID
		sess.Roles = append([]string(nil), roles...)
	}
	cp := sess.clone()
	s.mu.Unlock()
	if changed {
		_ = s.persist()
	}
	return cp, CommitApplied
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

// RevokeIfBound removes only the identity and provider bearer that a request
// checked. A late rejection for an old bearer cannot revoke its replacement.
func (s *Store) RevokeIfBound(token, userID, bearer string) bool {
	if s == nil || token == "" {
		return false
	}
	id := ID(token)
	s.mu.Lock()
	sess, ok := s.sessions[id]
	matched := ok && sess.UserID == userID && sess.AuthLocalToken == bearer
	if matched {
		delete(s.sessions, id)
	}
	s.mu.Unlock()
	if matched {
		_ = s.persist()
	}
	return matched
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
