package session

import (
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
)

type Session struct {
	UserID      string
	Username    string
	Roles       []string
	Permissions []string
	CreatedAt   time.Time
	ExpiresAt   time.Time
}

type Store struct {
	mu       sync.RWMutex
	sessions map[string]*Session
	ttl      time.Duration
}

func NewStore(ttl time.Duration) *Store {
	s := &Store{
		sessions: make(map[string]*Session),
		ttl:      ttl,
	}
	go s.cleanupLoop()
	return s
}

func (s *Store) TTL() time.Duration {
	return s.ttl
}

func (s *Store) Create(userID, username string, roles, permissions []string) (string, error) {
	now := time.Now()
	sess := &Session{
		UserID:      userID,
		Username:    username,
		Roles:       roles,
		Permissions: permissions,
		CreatedAt:   now,
		ExpiresAt:   now.Add(s.ttl),
	}
	token := uuid.New().String()

	s.mu.Lock()
	s.sessions[token] = sess
	s.mu.Unlock()

	return token, nil
}

func (s *Store) Get(token string) (*Session, bool) {
	s.mu.RLock()
	sess, ok := s.sessions[token]
	s.mu.RUnlock()

	if !ok {
		return nil, false
	}

	if time.Now().After(sess.ExpiresAt) {
		s.mu.Lock()
		delete(s.sessions, token)
		s.mu.Unlock()
		return nil, false
	}

	return sess, true
}

func (s *Store) Revoke(token string) {
	s.mu.Lock()
	delete(s.sessions, token)
	s.mu.Unlock()
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

func (s *Store) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		now := time.Now()
		s.mu.Lock()
		for token, sess := range s.sessions {
			if now.After(sess.ExpiresAt) {
				delete(s.sessions, token)
			}
		}
		s.mu.Unlock()
	}
}
