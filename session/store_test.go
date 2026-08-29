package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

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
