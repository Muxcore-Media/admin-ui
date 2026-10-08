package session

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

func TestFileStoreNoPlaintextAndPermissions(t *testing.T) {
	t.Setenv(EnvSessionKey, "")
	dir := filepath.Join(t.TempDir(), "data")
	path := filepath.Join(dir, "sessions.json")

	s1 := NewFileStore(path, time.Hour)
	tok, err := s1.Create("u1", "alice", []string{"admin"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	const bearer = "auth-local-bearer-SECRET-123"
	s1.BindAuthLocalToken(tok, bearer)

	for p, want := range map[string]os.FileMode{dir: 0o700, path: 0o600, filepath.Join(dir, KeyFileName): 0o600} {
		st, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if got := st.Mode().Perm(); got != want {
			t.Fatalf("%s mode = %o, want %o", p, got, want)
		}
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), tok) {
		t.Fatal("session bearer token found in plaintext on disk")
	}
	if strings.Contains(string(raw), bearer) {
		t.Fatal("auth-local token found in plaintext on disk")
	}
	if !strings.Contains(string(raw), ID(tok)) {
		t.Fatal("expected session keyed by token hash")
	}

	// Reload after "restart": same key file, session + decrypted bearer restored.
	s2 := NewFileStore(path, time.Hour)
	sess, ok := s2.Get(tok)
	if !ok {
		t.Fatal("expected session after reload")
	}
	if sess.AuthLocalToken != bearer {
		t.Fatalf("AuthLocalToken = %q after reload", sess.AuthLocalToken)
	}
}

func TestFileStoreEnvKeyAndRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	t.Setenv(EnvSessionKey, "first-key")
	s1 := NewFileStore(path, time.Hour)
	tok, _ := s1.Create("u1", "alice", nil, nil)
	s1.BindAuthLocalToken(tok, "bearer")
	if _, err := os.Stat(filepath.Join(filepath.Dir(path), KeyFileName)); !os.IsNotExist(err) {
		t.Fatal("key file must not be written when ADMIN_UI_SESSION_KEY is set")
	}
	if _, ok := NewFileStore(path, time.Hour).Get(tok); !ok {
		t.Fatal("expected session with same env key")
	}
	t.Setenv(EnvSessionKey, "second-key")
	if _, ok := NewFileStore(path, time.Hour).Get(tok); ok {
		t.Fatal("session sealed with a rotated key must be dropped")
	}
}

func TestFileStoreDiscardsLegacyPlaintextFile(t *testing.T) {
	t.Setenv(EnvSessionKey, "")
	path := filepath.Join(t.TempDir(), "sessions.json")
	legacy := `{"sessions":{"11111111-2222-3333-4444-555555555555":{"UserID":"u1","AuthLocalToken":"LEGACY-SECRET","ExpiresAt":"2999-01-01T00:00:00Z"}}}`
	if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	s := NewFileStore(path, time.Hour)
	if s.Count() != 0 {
		t.Fatalf("expected legacy sessions discarded, got %d", s.Count())
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "LEGACY-SECRET") {
		t.Fatal("legacy plaintext should be overwritten")
	}
	st, _ := os.Stat(path)
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("rewritten file mode = %o", st.Mode().Perm())
	}
}

func TestListExposesIDNotToken(t *testing.T) {
	s := NewStore(time.Hour)
	tok, _ := s.Create("u1", "alice", nil, nil)
	infos := s.List()
	if len(infos) != 1 || infos[0].ID != ID(tok) || infos[0].ID == tok {
		t.Fatalf("unexpected list: %+v", infos)
	}
	if !s.RenameByID(infos[0].ID, "TV") {
		t.Fatal("RenameByID failed")
	}
	s.RevokeByID(infos[0].ID)
	if _, ok := s.Get(tok); ok {
		t.Fatal("expected session revoked by ID")
	}
}

func TestSnapshotIsIndependentOfStore(t *testing.T) {
	s := NewStore(time.Hour)
	tok, err := s.CreateWithTenant("u1", "alice", "tenant-a", []string{"viewer"}, []string{"keep"})
	if err != nil {
		t.Fatal(err)
	}
	s.BindAuthLocalToken(tok, "bearer-a")
	snap, ok := s.Snapshot(tok)
	if !ok {
		t.Fatal("expected snapshot")
	}
	snap.Roles[0] = "mutated"
	snap.Permissions[0] = "mutated"
	snap.Username = "mutated"
	snap.AuthLocalToken = "mutated"
	stored, ok := s.Get(tok)
	if !ok || stored.Username != "alice" || stored.Roles[0] != "viewer" || stored.Permissions[0] != "keep" || stored.AuthLocalToken != "bearer-a" {
		t.Fatalf("store changed with snapshot: %+v", stored)
	}
	stored.Roles[0] = "store-mutated"
	if snap.Roles[0] != "mutated" {
		t.Fatal("snapshot shares the store role slice")
	}
}

func TestCommitValidatedClaims(t *testing.T) {
	s := NewStore(time.Hour)
	tok, _ := s.CreateWithTenant("u1", "alice", "old-tenant", []string{"viewer"}, []string{"keep"})
	s.BindAuthLocalToken(tok, "bearer-a")
	roles := []string{"admin"}
	if _, got := s.CommitValidatedClaims(tok, "bearer-a", "u1", "alice-new", "tenant-b", roles); got != CommitApplied {
		t.Fatalf("commit = %v", got)
	}
	roles[0] = "mutated"
	stored, ok := s.Get(tok)
	if !ok || stored.Username != "alice-new" || stored.TenantID != "tenant-b" || stored.UserID != "u1" || stored.Roles[0] != "admin" || stored.Permissions[0] != "keep" || stored.AuthLocalToken != "bearer-a" {
		t.Fatalf("claims = %+v", stored)
	}

	if _, got := s.CommitValidatedClaims(tok, "other-bearer", "u1", "nope", "nope", []string{"nope"}); got != CommitRebound {
		t.Fatalf("rebind commit = %v", got)
	}
	if stored, _ = s.Get(tok); stored.Username != "alice-new" {
		t.Fatal("rebound commit wrote claims")
	}
	if _, got := s.CommitValidatedClaims(tok, "bearer-a", "u2", "other", "t", nil); got != CommitIdentityMismatch {
		t.Fatalf("mismatch commit = %v", got)
	}
	if _, got := s.CommitValidatedClaims(tok, "bearer-a", "", "other", "t", nil); got != CommitIdentityMismatch {
		t.Fatalf("empty user commit = %v", got)
	}
	s.Revoke(tok)
	if _, got := s.CommitValidatedClaims(tok, "bearer-a", "u1", "alice", "t", nil); got != CommitGone {
		t.Fatalf("revoked commit = %v", got)
	}
	if s.Count() != 0 {
		t.Fatal("commit recreated a revoked session")
	}
}

func TestCommitValidatedClaimsPersists(t *testing.T) {
	t.Setenv(EnvSessionKey, "claims-key")
	path := filepath.Join(t.TempDir(), "sessions.json")
	s1 := NewFileStore(path, time.Hour)
	tok, err := s1.CreateWithTenant("u1", "alice", "old", []string{"viewer"}, []string{"keep"})
	if err != nil {
		t.Fatal(err)
	}
	s1.BindAuthLocalToken(tok, "bearer-a")
	if _, got := s1.CommitValidatedClaims(tok, "bearer-a", "u1", "alice-new", "tenant-b", []string{"admin"}); got != CommitApplied {
		t.Fatalf("commit = %v", got)
	}
	s2 := NewFileStore(path, time.Hour)
	stored, ok := s2.Get(tok)
	if !ok || stored.Username != "alice-new" || stored.TenantID != "tenant-b" || len(stored.Roles) != 1 || stored.Roles[0] != "admin" || stored.Permissions[0] != "keep" || stored.AuthLocalToken != "bearer-a" {
		t.Fatalf("reloaded claims = %+v ok=%v", stored, ok)
	}
}

func TestParseKey(t *testing.T) {
	k32 := strings.Repeat("ab", 32)
	if b, err := ParseKey(k32); err != nil || len(b) != 32 || b[0] != 0xab {
		t.Fatalf("hex key: %v %x", err, b)
	}
	if b, err := ParseKey("passphrase"); err != nil || len(b) != 32 {
		t.Fatalf("passphrase key: %v", err)
	}
	if _, err := ParseKey("  "); err == nil {
		t.Fatal("expected error for empty key")
	}
}

func TestCommitValidatedClaimsReturnsOwnClaimsConcurrently(t *testing.T) {
	s := NewStore(time.Hour)
	tok, _ := s.CreateWithTenant("u1", "alice", "t", []string{"viewer"}, []string{"keep"})
	s.BindAuthLocalToken(tok, "bearer-a")
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := fmt.Sprintf("alice-%d", i)
			role := fmt.Sprintf("role-%d", i)
			for j := 0; j < 50; j++ {
				got, res := s.CommitValidatedClaims(tok, "bearer-a", "u1", name, "tenant-"+name, []string{role})
				if res != CommitApplied {
					t.Errorf("commit = %v", res)
					return
				}
				if got.Username != name || got.TenantID != "tenant-"+name || len(got.Roles) != 1 || got.Roles[0] != role || got.Permissions[0] != "keep" {
					t.Errorf("request %d saw another request's claims: %+v", i, got)
					return
				}
				got.Roles[0] = "mutated" // the copy must not alias the store
			}
		}(i)
	}
	wg.Wait()
}

// Run under -race: Get and Snapshot readers must not race claim rewrites.
func TestSessionReadersDoNotRaceClaimCommits(t *testing.T) {
	s := NewStore(time.Hour)
	tok, _ := s.CreateWithTenant("u1", "alice", "t", []string{"viewer"}, nil)
	s.BindAuthLocalToken(tok, "bearer-a")
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			name := fmt.Sprintf("alice-%d", i)
			s.CommitValidatedClaims(tok, "bearer-a", "u1", name, name, []string{name})
		}
	}()
	for i := 0; i < 2000; i++ {
		if sess, ok := s.Get(tok); !ok || sess.Username == "" || sess.TenantID == "" || len(sess.Roles) == 0 {
			t.Fatalf("Get = %+v ok=%v", sess, ok)
		}
		if snap, ok := s.Snapshot(tok); !ok || snap.Username == "" || len(snap.Roles) == 0 {
			t.Fatalf("Snapshot = %+v ok=%v", snap, ok)
		}
	}
	close(stop)
	wg.Wait()
}

func TestCommitValidatedClaimsSkipsRewriteWhenUnchanged(t *testing.T) {
	t.Setenv(EnvSessionKey, "claims-key")
	path := filepath.Join(t.TempDir(), "sessions.json")
	s := NewFileStore(path, time.Hour)
	tok, err := s.CreateWithTenant("u1", "alice", "t", []string{"admin"}, []string{"keep"})
	if err != nil {
		t.Fatal(err)
	}
	s.BindAuthLocalToken(tok, "bearer-a")
	old := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	mtime := func() time.Time {
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		return fi.ModTime()
	}
	got, res := s.CommitValidatedClaims(tok, "bearer-a", "u1", "alice", "t", []string{"admin"})
	if res != CommitApplied || got.Username != "alice" || got.Roles[0] != "admin" {
		t.Fatalf("commit = %v %+v", res, got)
	}
	if !mtime().Equal(old) {
		t.Fatal("unchanged claims rewrote the sessions file")
	}
	if _, res = s.CommitValidatedClaims(tok, "bearer-a", "u1", "alice", "t", []string{"admin", "extra"}); res != CommitApplied {
		t.Fatalf("commit = %v", res)
	}
	if mtime().Equal(old) {
		t.Fatal("changed roles did not rewrite the sessions file")
	}
}
