package handler

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	authv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/auth/v1"
	"github.com/Muxcore-Media/core/sdk/go/module/erasure"

	"github.com/Muxcore-Media/admin-ui/session"
)

type fakeErasureUsers struct {
	users []*authv1.UserInfo
	err   error
	calls int
}

func (f *fakeErasureUsers) ListUsers(context.Context) ([]*authv1.UserInfo, error) {
	f.calls++
	return f.users, f.err
}

type erasureFixture struct {
	t        *testing.T
	dir      string
	sessions *session.Store
	users    *fakeErasureUsers
	owner    *ErasureOwner
}

// newErasureFixture points every admin-ui store at a temp dir and builds an
// owner over a file-backed session store.
func newErasureFixture(t *testing.T) *erasureFixture {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("ADMIN_UI_DATA_DIR", dir)
	for _, k := range []string{"ADMIN_UI_SESSION_FILE", "ADMIN_UI_PARENTAL_FILE", "ADMIN_UI_PASSWORD_RESET_FILE", "ADMIN_UI_ERASURE_APPLIED_FILE"} {
		t.Setenv(k, "")
	}
	store, err := session.NewFileStoreWithKey(SessionFilePath(), time.Hour, make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	users := &fakeErasureUsers{users: []*authv1.UserInfo{
		{Id: "u-admin", Username: "admin"},
		{Id: "u-bob", Username: "bob"},
	}}
	return &erasureFixture{t: t, dir: dir, sessions: store, users: users, owner: NewErasureOwner("admin-ui", store, users)}
}

func (f *erasureFixture) session(userID, tenant, bearer string) {
	f.t.Helper()
	tok, err := f.sessions.CreateWithTenant(userID, "name-of-"+userID, tenant, []string{"user"}, nil)
	if err != nil {
		f.t.Fatal(err)
	}
	if bearer != "" {
		f.sessions.BindAuthLocalToken(tok, bearer)
	}
}

func (f *erasureFixture) writeJSON(path string, v any) {
	f.t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		f.t.Fatal(err)
	}
}

func (f *erasureFixture) resets() []passwordResetEntry {
	f.t.Helper()
	got, err := readPasswordResetFile()
	if err != nil {
		f.t.Fatal(err)
	}
	return got.Requests
}

func (f *erasureFixture) resetIDs() []string {
	var ids []string
	for _, e := range f.resets() {
		ids = append(ids, e.ID)
	}
	return ids
}

func (f *erasureFixture) parentalKeys() []string {
	f.t.Helper()
	src, err := readParentalLegacy()
	if err != nil {
		f.t.Fatal(err)
	}
	var keys []string
	for k := range src.Entries {
		keys = append(keys, k)
	}
	return keys
}

// seed writes a victim (u-victim) and a bystander (u-bob) into every store.
func (f *erasureFixture) seed() {
	f.session("u-victim", "", "victim-bearer-1")
	f.session("u-victim", "", "") // Quick Connect: no bearer
	f.session("u-bob", "", "bob-bearer")
	f.writeJSON(passwordResetFilePath(), passwordResetFile{Requests: []passwordResetEntry{
		{ID: "r-id", Username: "victim-old-name", UserID: "u-victim", Status: "pending"},
		{ID: "r-legacy-gone", Username: "victim", Status: "pending"},
		{ID: "r-legacy-bob", Username: "bob", Status: "pending"},
		{ID: "r-bob", Username: "bob", UserID: "u-bob", Status: "pending"},
	}})
	f.writeJSON(parentalFilePath(), map[string]parentalSettings{
		"u-victim": {MaxParentalRating: "PG", KidsMode: true, PINHash: "x"},
		"u-bob":    {MaxParentalRating: "R"},
	})
}

func tombstone(id, user string) erasure.Tombstone {
	return erasure.Tombstone{ErasureID: id, UserID: user, DeletedAt: time.Now()}
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]int{}
	for _, s := range a {
		seen[s]++
	}
	for _, s := range b {
		seen[s]--
	}
	for _, n := range seen {
		if n != 0 {
			return false
		}
	}
	return true
}

func TestErasureApplyDeletesUsersRowsAndRecords(t *testing.T) {
	f := newErasureFixture(t)
	f.seed()
	ctx := context.Background()

	if n, err := f.owner.Verify(ctx, tombstone("er-1", "u-victim")); err != nil || n != 4 {
		t.Fatalf("before Apply Verify = %d, %v; want 4 rows (2 sessions, 1 reset, 1 parental)", n, err)
	}

	counts, err := f.owner.Apply(ctx, tombstone("er-1", "u-victim"))
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	// "r-legacy-gone" (username "victim" no longer resolves) is purged with the id-keyed row.
	want := erasure.Counts{"sessions": 2, "password_resets": 2, "parental_entries": 1}
	for k, v := range want {
		if counts[k] != v {
			t.Errorf("counts[%s] = %d, want %d (all: %v)", k, counts[k], v, counts)
		}
	}

	if got := f.sessions.CountUser("u-victim", ""); got != 0 {
		t.Errorf("victim sessions remain: %d", got)
	}
	if got := f.sessions.CountUser("u-bob", ""); got != 1 {
		t.Errorf("bystander sessions = %d, want 1", got)
	}
	if got := f.resetIDs(); !sameStrings(got, []string{"r-legacy-bob", "r-bob"}) {
		t.Errorf("password resets after erasure = %v", got)
	}
	if got := f.parentalKeys(); !sameStrings(got, []string{"u-bob"}) {
		t.Errorf("parental entries after erasure = %v", got)
	}
	if ok, err := f.owner.Applied(ctx, "er-1"); err != nil || !ok {
		t.Errorf("Applied(er-1) = %v, %v; want true", ok, err)
	}
	if n, err := f.owner.Verify(ctx, tombstone("er-1", "u-victim")); err != nil || n != 0 {
		t.Errorf("Verify after Apply = %d, %v; want 0", n, err)
	}

	// The persisted session file really lost the victim: reload it.
	reloaded, err := session.NewFileStoreWithKey(SessionFilePath(), time.Hour, make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.CountUser("u-victim", "") != 0 || reloaded.CountUser("u-bob", "") != 1 {
		t.Errorf("persisted sessions: victim=%d bob=%d", reloaded.CountUser("u-victim", ""), reloaded.CountUser("u-bob", ""))
	}

	// The record holds no user id or username.
	raw, err := os.ReadFile(erasureAppliedFilePath())
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"u-victim", "victim", "bob"} {
		if strings.Contains(string(raw), leak) {
			t.Errorf("erasure-applied.json contains %q: %s", leak, raw)
		}
	}
}

func TestErasureAppliedIDIsNotAppliedTwice(t *testing.T) {
	f := newErasureFixture(t)
	f.seed()
	ctx := context.Background()
	if _, err := f.owner.Apply(ctx, tombstone("er-1", "u-victim")); err != nil {
		t.Fatal(err)
	}

	// A new row for the same id appears after the record was written, and the
	// directory is now unreachable: a no-op must neither read nor touch it.
	f.writeJSON(parentalFilePath(), map[string]parentalSettings{"u-victim": {KidsMode: true}, "u-bob": {}})
	f.users.err = errors.New("directory down")
	callsBefore := f.users.calls

	counts, err := f.owner.Apply(ctx, tombstone("er-1", "u-victim"))
	if err != nil {
		t.Fatalf("re-Apply of an applied erasure must be a no-op, got %v", err)
	}
	for k, v := range counts {
		if v != 0 {
			t.Errorf("re-Apply reported work: %s=%d", k, v)
		}
	}
	if f.users.calls != callsBefore {
		t.Error("re-Apply consulted the user directory")
	}
	if got := f.parentalKeys(); !sameStrings(got, []string{"u-victim", "u-bob"}) {
		t.Errorf("re-Apply modified parental.json: %v", got)
	}
}

func TestErasureApplyLeavesOtherUsersRows(t *testing.T) {
	f := newErasureFixture(t)
	f.seed()
	ctx := context.Background()
	if _, err := f.owner.Apply(ctx, tombstone("er-wrong", "u-nobody")); err != nil {
		t.Fatal(err)
	}
	if f.sessions.CountUser("u-victim", "") != 2 || f.sessions.CountUser("u-bob", "") != 1 {
		t.Error("erasing an unrelated id removed sessions")
	}
	if got := f.parentalKeys(); !sameStrings(got, []string{"u-victim", "u-bob"}) {
		t.Errorf("parental entries = %v", got)
	}
	// Only the legacy entry whose username does not resolve ("victim" is not
	// in the directory) may go; id-keyed rows of live users are untouched.
	if got := f.resetIDs(); !sameStrings(got, []string{"r-id", "r-legacy-bob", "r-bob"}) {
		t.Errorf("password resets = %v", got)
	}
}

func TestErasureLegacyPasswordResetsPurgeOnlyUnresolvableUsernames(t *testing.T) {
	f := newErasureFixture(t)
	f.writeJSON(passwordResetFilePath(), passwordResetFile{Requests: []passwordResetEntry{
		{ID: "legacy-erased", Username: "erased-user", Status: "pending"},
		{ID: "legacy-resolves", Username: "bob", Status: "resolved"},
		{ID: "legacy-admin", Username: "admin", Status: "dismissed"},
		{ID: "id-other-user", Username: "gone-name", UserID: "u-bob", Status: "pending"},
	}})
	counts, err := f.owner.Apply(context.Background(), tombstone("er-legacy", "u-erased"))
	if err != nil {
		t.Fatal(err)
	}
	if counts["password_resets"] != 1 {
		t.Errorf("password_resets = %d, want 1", counts["password_resets"])
	}
	if got := f.resetIDs(); !sameStrings(got, []string{"legacy-resolves", "legacy-admin", "id-other-user"}) {
		t.Errorf("remaining = %v", got)
	}
}

func TestErasureApplyPersistFailureRecordsNothing(t *testing.T) {
	f := newErasureFixture(t)
	f.seed()
	ctx := context.Background()

	// sessions.json cannot be rewritten: the temp path is a non-empty dir.
	obstacle := SessionFilePath() + ".tmp"
	if err := os.MkdirAll(filepath.Join(obstacle, "keep"), 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := f.owner.Apply(ctx, tombstone("er-1", "u-victim"))
	if err == nil {
		t.Fatal("Apply succeeded although sessions.json could not be persisted")
	}
	if !strings.Contains(err.Error(), "persist sessions.json") {
		t.Errorf("unexpected error: %v", err)
	}
	if ok, aerr := f.owner.Applied(ctx, "er-1"); aerr != nil || ok {
		t.Fatalf("a failed Apply recorded the erasure: applied=%v err=%v", ok, aerr)
	}
	if _, serr := os.Stat(erasureAppliedFilePath()); !errors.Is(serr, os.ErrNotExist) {
		t.Errorf("erasure-applied.json exists after failed Apply: %v", serr)
	}
	// Nothing else was rewritten behind the failed step.
	if got := f.resetIDs(); len(got) != 4 {
		t.Errorf("password-resets.json changed after failed session persist: %v", got)
	}
	if got := f.parentalKeys(); len(got) != 2 {
		t.Errorf("parental.json changed after failed session persist: %v", got)
	}
	if n, _ := f.owner.Verify(ctx, tombstone("er-1", "u-victim")); n == 0 {
		t.Error("Verify reported a clean post-condition for rows that were not erased")
	}

	// The obstruction clears: the next sweep completes and records it.
	if err := os.RemoveAll(obstacle); err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.Apply(ctx, tombstone("er-1", "u-victim")); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if ok, _ := f.owner.Applied(ctx, "er-1"); !ok {
		t.Error("retry did not record the erasure")
	}
	if n, _ := f.owner.Verify(ctx, tombstone("er-1", "u-victim")); n != 0 {
		t.Errorf("Verify after retry = %d", n)
	}
}

func TestErasureApplyLaterWriteFailureNeverRecordsApplied(t *testing.T) {
	obstacles := map[string]func() string{
		"password-resets": func() string { return passwordResetFilePath() + ".tmp" },
		"parental":        func() string { return parentalFilePath() + ".tmp" },
		"applied-record":  func() string { return erasureAppliedFilePath() + ".tmp" },
	}
	for name, obstacleFor := range obstacles {
		t.Run(name, func(t *testing.T) {
			f := newErasureFixture(t)
			f.seed()
			ctx := context.Background()
			obstacle := obstacleFor()
			if err := os.MkdirAll(filepath.Join(obstacle, "keep"), 0o700); err != nil {
				t.Fatal(err)
			}
			if _, err := f.owner.Apply(ctx, tombstone("er-w", "u-victim")); err == nil {
				t.Fatal("Apply succeeded although a store could not be persisted")
			}
			if ok, err := f.owner.Applied(ctx, "er-w"); err != nil || ok {
				t.Fatalf("applied=%v err=%v after a failed write", ok, err)
			}
			// The id was never vouched for, so the next sweep must redo it and
			// only then record it.
			if err := os.RemoveAll(obstacle); err != nil {
				t.Fatal(err)
			}
			if _, err := f.owner.Apply(ctx, tombstone("er-w", "u-victim")); err != nil {
				t.Fatalf("retry: %v", err)
			}
			if ok, _ := f.owner.Applied(ctx, "er-w"); !ok {
				t.Error("erasure not recorded after a successful retry")
			}
			if n, _ := f.owner.Verify(ctx, tombstone("er-w", "u-victim")); n != 0 {
				t.Errorf("Verify = %d after completed retry", n)
			}
			if got := f.resetIDs(); !sameStrings(got, []string{"r-legacy-bob", "r-bob"}) {
				t.Errorf("password resets after retry = %v", got)
			}
		})
	}
}

func TestErasureApplyFailsClosedOnUnreadableInputs(t *testing.T) {
	cases := map[string]func(f *erasureFixture){
		"password-resets": func(f *erasureFixture) {
			if err := os.WriteFile(passwordResetFilePath(), []byte("{not json"), 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"parental": func(f *erasureFixture) {
			if err := os.WriteFile(parentalFilePath(), []byte("[]"), 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"user-directory": func(f *erasureFixture) { f.users.err = errors.New("provider unreachable") },
		"empty-directory": func(f *erasureFixture) {
			f.users.users = nil
		},
		"applied-record": func(f *erasureFixture) {
			if err := os.WriteFile(erasureAppliedFilePath(), []byte("garbage"), 0o600); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, breakIt := range cases {
		t.Run(name, func(t *testing.T) {
			f := newErasureFixture(t)
			f.seed()
			breakIt(f)
			if _, err := f.owner.Apply(context.Background(), tombstone("er-1", "u-victim")); err == nil {
				t.Fatal("Apply succeeded with an unreadable input")
			}
			// Phase 1 failed: no store was touched.
			if f.sessions.CountUser("u-victim", "") != 2 {
				t.Error("sessions were erased although an input could not be read")
			}
			if name != "applied-record" {
				if ok, _ := f.owner.Applied(context.Background(), "er-1"); ok {
					t.Error("erasure recorded as applied after a failed Apply")
				}
			}
		})
	}
}

func TestErasureSessionsHonourTombstoneTenant(t *testing.T) {
	f := newErasureFixture(t)
	f.session("u-victim", "tenant-a", "b1")
	f.session("u-victim", "tenant-b", "b2") // same id, other tenant: not this tombstone's
	f.session("u-victim", "", "b3")         // tenant cleared by claim revalidation: still theirs
	ctx := context.Background()
	tomb := tombstone("er-t", "u-victim")
	tomb.TenantID = "tenant-a"
	if _, err := f.owner.Apply(ctx, tomb); err != nil {
		t.Fatal(err)
	}
	if got := f.sessions.CountUser("u-victim", "tenant-b"); got != 1 {
		t.Errorf("tenant-b session count = %d, want the single other-tenant session kept", got)
	}
}

func TestErasureOwnerRejectsEmptyTombstone(t *testing.T) {
	f := newErasureFixture(t)
	if _, err := f.owner.Apply(context.Background(), erasure.Tombstone{ErasureID: "er-x"}); err == nil {
		t.Fatal("Apply accepted a tombstone without a user id")
	}
	if _, err := f.owner.Apply(context.Background(), erasure.Tombstone{UserID: "u"}); err == nil {
		t.Fatal("Apply accepted a tombstone without an erasure id")
	}
}
