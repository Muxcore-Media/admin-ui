package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRenameSessionSetsLabel(t *testing.T) {
	s := NewStore(time.Hour)
	tok, err := s.Create("u1", "alice", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	ok := s.RenameSession(tok, "  Living Room TV  ")
	if !ok {
		t.Fatal("expected RenameSession to return true for existing session")
	}
	sess, found := s.Get(tok)
	if !found {
		t.Fatal("expected session to still exist")
	}
	if sess.Label != "Living Room TV" {
		t.Fatalf("expected trimmed label, got %q", sess.Label)
	}
}

func TestRenameSessionClearsLabel(t *testing.T) {
	s := NewStore(time.Hour)
	tok, _ := s.Create("u1", "alice", nil, nil)
	s.RenameSession(tok, "Old Label")
	s.RenameSession(tok, "")
	sess, _ := s.Get(tok)
	if sess.Label != "" {
		t.Fatalf("expected blank label, got %q", sess.Label)
	}
}

func TestRenameSessionUnknownTokenReturnsFalse(t *testing.T) {
	s := NewStore(time.Hour)
	if s.RenameSession("nonexistent", "label") {
		t.Fatal("expected false for unknown token")
	}
}

func TestListIncludesLabel(t *testing.T) {
	s := NewStore(time.Hour)
	tok, _ := s.Create("u1", "alice", nil, nil)
	s.RenameSession(tok, "Kitchen Hub")
	infos := s.List()
	if len(infos) != 1 {
		t.Fatalf("expected 1 session, got %d", len(infos))
	}
	if infos[0].Label != "Kitchen Hub" {
		t.Fatalf("expected label in List(), got %q", infos[0].Label)
	}
}

func TestFileStorePersistsAcrossReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.json")
	ttl := time.Hour

	s1 := NewFileStore(path, ttl)
	tok, err := s1.Create("u1", "alice", []string{"admin"}, nil)
	if err != nil {
		t.Fatal(err)
	}

	s2 := NewFileStore(path, ttl)
	sess, ok := s2.Get(tok)
	if !ok {
		t.Fatal("expected session after reload")
	}
	if sess.UserID != "u1" || sess.Username != "alice" {
		t.Fatalf("unexpected session: %+v", sess)
	}

	s2.Revoke(tok)
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	s3 := NewFileStore(path, ttl)
	if _, ok := s3.Get(tok); ok {
		t.Fatal("expected revoked session gone after reload")
	}
}

func TestFileStorePersistsLabel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.json")
	ttl := time.Hour

	s1 := NewFileStore(path, ttl)
	tok, _ := s1.Create("u1", "alice", nil, nil)
	s1.RenameSession(tok, "Bedroom TV")

	s2 := NewFileStore(path, ttl)
	sess, ok := s2.Get(tok)
	if !ok {
		t.Fatal("expected session after reload")
	}
	if sess.Label != "Bedroom TV" {
		t.Fatalf("expected label persisted, got %q", sess.Label)
	}
}
