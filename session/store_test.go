package session

import (
	"net/http"
	"testing"
	"time"
)

func TestCreateAndGet(t *testing.T) {
	s := NewStore(5 * time.Minute)
	token, err := s.Create("user1", "testuser", []string{"admin"}, []string{"admin.access"})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if token == "" {
		t.Fatal("expected non-empty token")
	}

	sess, ok := s.Get(token)
	if !ok {
		t.Fatal("expected session to exist")
	}
	if sess.UserID != "user1" {
		t.Fatalf("expected user1, got %s", sess.UserID)
	}
	if sess.Username != "testuser" {
		t.Fatalf("expected testuser, got %s", sess.Username)
	}
	if len(sess.Roles) != 1 || sess.Roles[0] != "admin" {
		t.Fatalf("expected [admin], got %v", sess.Roles)
	}
	if len(sess.Permissions) != 1 || sess.Permissions[0] != "admin.access" {
		t.Fatalf("expected [admin.access], got %v", sess.Permissions)
	}
}

func TestGetExpired(t *testing.T) {
	s := NewStore(0) // zero TTL means already expired
	token, err := s.Create("user1", "testuser", nil, nil)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Small sleep to ensure expiry
	time.Sleep(time.Millisecond)

	_, ok := s.Get(token)
	if ok {
		t.Fatal("expected session to be expired")
	}
}

func TestRevoke(t *testing.T) {
	s := NewStore(5 * time.Minute)
	token, _ := s.Create("user1", "testuser", nil, nil)

	s.Revoke(token)

	_, ok := s.Get(token)
	if ok {
		t.Fatal("expected session to be revoked")
	}
}

func TestGetFromRequest(t *testing.T) {
	s := NewStore(5 * time.Minute)
	token, _ := s.Create("user1", "testuser", nil, nil)

	req, _ := http.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: token})

	sess, ok := s.GetFromRequest(req)
	if !ok {
		t.Fatal("expected session from request")
	}
	if sess.UserID != "user1" {
		t.Fatalf("expected user1, got %s", sess.UserID)
	}
}

func TestGetFromRequestMissing(t *testing.T) {
	s := NewStore(5 * time.Minute)
	req, _ := http.NewRequest("GET", "/", nil)

	_, ok := s.GetFromRequest(req)
	if ok {
		t.Fatal("expected no session for missing cookie")
	}
}

func TestCount(t *testing.T) {
	s := NewStore(5 * time.Minute)
	if s.Count() != 0 {
		t.Fatalf("expected 0, got %d", s.Count())
	}
	s.Create("user1", "testuser", nil, nil)
	if s.Count() != 1 {
		t.Fatalf("expected 1, got %d", s.Count())
	}
	s.Create("user2", "testuser2", nil, nil)
	if s.Count() != 2 {
		t.Fatalf("expected 2, got %d", s.Count())
	}
}

func TestTTL(t *testing.T) {
	expected := 10 * time.Minute
	s := NewStore(expected)
	if s.TTL() != expected {
		t.Fatalf("expected %v, got %v", expected, s.TTL())
	}
}
