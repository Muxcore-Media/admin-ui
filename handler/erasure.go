package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	authv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/auth/v1"
	"github.com/Muxcore-Media/core/sdk/go/module/erasure"

	"github.com/Muxcore-Media/admin-ui/session"
)

// ADR-0035 personal-data owner for admin-ui.
//
// admin-ui keeps three stores that carry a user id (§3 of the ADR):
//
//   - sessions.json         every session of the user is deleted;
//   - password-resets.json  entries stored with the user's id are deleted, and
//     legacy entries (no id, written by the BFF before it recorded one) are
//     purged once their username no longer resolves to a live account;
//   - parental.json         the legacy per-user entry is deleted (it is only
//     the ADR-0030 §7 migration source).
//
// Erasure is keyed by the tombstone's user id, never by username. The one
// username-keyed file is password-resets.json; the live user directory is
// consulted only to decide whether a legacy entry still belongs to somebody.
//
// # Atomicity
//
// The stores are three separate files, one of them shared with another
// process, so a single transaction is not available. Apply instead:
//
//  1. reads and parses every input first and fails closed (an unreadable or
//     malformed file is an error, never "empty"), so a bad input changes
//     nothing;
//  2. then writes each file with temp-file + rename, the erasure_applied
//     record LAST.
//
// Every mutation is an idempotent deletion, and an erasure id is recorded only
// after all of them persisted. A failure part-way therefore leaves the id
// unrecorded; the reconciler's next sweep repeats the deletions (a no-op for
// the files already written) and then records it. The record is never written
// ahead of the data it vouches for.
//
// erasure_applied is a small JSON file beside sessions.json (the module has no
// database engine). It holds opaque erasure ids and times only, never a user
// id or username, and is never pruned: replaying an old tombstone is a no-op.

// ErasureUsers lists the identity provider's live accounts. The reconciler's
// implementation reaches the provider through the verified `identity`
// capability (see ProviderUsers in the root package).
type ErasureUsers interface {
	ListUsers(ctx context.Context) ([]*authv1.UserInfo, error)
}

// ErasureOwner implements erasure.Owner and erasure.Verifier.
type ErasureOwner struct {
	id       string
	sessions *session.Store
	users    ErasureUsers
	now      func() time.Time
}

var (
	_ erasure.Owner    = (*ErasureOwner)(nil)
	_ erasure.Verifier = (*ErasureOwner)(nil)
)

// NewErasureOwner returns admin-ui's erasure owner. moduleID must equal the
// CN of admin-ui's mesh certificate: the provider attributes acknowledgements
// to the verified CN.
func NewErasureOwner(moduleID string, sessions *session.Store, users ErasureUsers) *ErasureOwner {
	return &ErasureOwner{id: moduleID, sessions: sessions, users: users, now: time.Now}
}

// Detail codes reported with a failed application. They are acknowledged to
// the provider and shown on /users, so they never carry personal data.
const (
	erasureDetailUsers          = "user_list_unavailable"
	erasureDetailResetsRead     = "password_resets_unreadable"
	erasureDetailParentalRead   = "parental_unreadable"
	erasureDetailAppliedRead    = "applied_record_unreadable"
	erasureDetailSessions       = "sessions_persist_failed"
	erasureDetailResetsWrite    = "password_resets_persist_failed"
	erasureDetailParentalWrite  = "parental_persist_failed"
	erasureDetailAppliedWrite   = "applied_record_persist_failed"
	erasureDetailInvalidInput   = "invalid_tombstone"
	erasureAppliedFormatVersion = 1
)

// erasureMu serialises Apply and the applied-record file in this process.
var erasureMu sync.Mutex

type erasureAppliedFile struct {
	Version int `json:"version"`
	// Applied maps erasure id -> RFC 3339 UTC time it was applied.
	Applied map[string]string `json:"applied"`
}

// readErasureApplied loads erasure-applied.json. A missing file is an empty
// record; an unreadable or malformed one is an error so a damaged record is
// never replaced by an empty one.
func readErasureApplied() (erasureAppliedFile, error) {
	f := erasureAppliedFile{Version: erasureAppliedFormatVersion, Applied: map[string]string{}}
	raw, err := os.ReadFile(erasureAppliedFilePath())
	if errors.Is(err, os.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return erasureAppliedFile{}, fmt.Errorf("read erasure-applied.json: %w", err)
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		return erasureAppliedFile{}, fmt.Errorf("erasure-applied.json is not valid JSON: %w", err)
	}
	if f.Applied == nil {
		f.Applied = map[string]string{}
	}
	return f, nil
}

// ModuleID implements erasure.Owner.
func (o *ErasureOwner) ModuleID() string { return o.id }

// Applied implements erasure.Owner.
func (o *ErasureOwner) Applied(_ context.Context, erasureID string) (bool, error) {
	erasureMu.Lock()
	defer erasureMu.Unlock()
	f, err := readErasureApplied()
	if err != nil {
		return false, err
	}
	_, ok := f.Applied[erasureID]
	return ok, nil
}

// liveUsernames returns the set of usernames that still resolve to an
// account. An empty directory is refused: a household always has at least the
// administrator that performed the deletion, so an empty answer is a provider
// fault, and acting on it would purge every legacy request.
func (o *ErasureOwner) liveUsernames(ctx context.Context) (map[string]struct{}, error) {
	if o.users == nil {
		return nil, errors.New("no user directory configured")
	}
	users, err := o.users.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	live := make(map[string]struct{}, len(users))
	for _, u := range users {
		if name := u.GetUsername(); name != "" {
			live[name] = struct{}{}
		}
	}
	if len(live) == 0 {
		return nil, errors.New("identity provider returned no users")
	}
	return live, nil
}

// splitPasswordResets partitions requests into those to keep and the number
// to remove for the tombstone's user id. A legacy entry (no stored id) is
// removed when its username is absent from live.
func splitPasswordResets(f passwordResetFile, userID string, live map[string]struct{}) (kept passwordResetFile, removed int) {
	kept = passwordResetFile{Requests: make([]passwordResetEntry, 0, len(f.Requests))}
	for _, e := range f.Requests {
		switch {
		case e.UserID != "" && e.UserID == userID:
			removed++
		case e.UserID == "":
			if _, resolves := live[e.Username]; !resolves {
				removed++
				continue
			}
			kept.Requests = append(kept.Requests, e)
		default:
			kept.Requests = append(kept.Requests, e)
		}
	}
	return kept, removed
}

// Apply implements erasure.Owner. See the file comment for the atomicity
// model. Applying an erasure id that is already recorded changes nothing.
func (o *ErasureOwner) Apply(ctx context.Context, t erasure.Tombstone) (erasure.Counts, error) {
	if t.ErasureID == "" || t.UserID == "" {
		return nil, erasure.WithDetail(erasureDetailInvalidInput, errors.New("tombstone without erasure id or user id"))
	}
	erasureMu.Lock()
	defer erasureMu.Unlock()

	rec, err := readErasureApplied()
	if err != nil {
		return nil, erasure.WithDetail(erasureDetailAppliedRead, err)
	}
	if _, done := rec.Applied[t.ErasureID]; done {
		return erasure.Counts{}, nil
	}

	// Phase 1: read everything, fail closed. Nothing is written yet.
	live, err := o.liveUsernames(ctx)
	if err != nil {
		return nil, erasure.WithDetail(erasureDetailUsers, err)
	}
	passwordResetMu.Lock()
	defer passwordResetMu.Unlock()
	resets, err := readPasswordResetFile()
	if err != nil {
		return nil, erasure.WithDetail(erasureDetailResetsRead, fmt.Errorf("read password-resets.json: %w", err))
	}
	keptResets, removedResets := splitPasswordResets(resets, t.UserID, live)

	parentalMu.Lock()
	defer parentalMu.Unlock()
	legacy, err := readParentalLegacyLocked()
	if err != nil {
		return nil, erasure.WithDetail(erasureDetailParentalRead, err)
	}
	_, hadParental := legacy.Entries[t.UserID]

	// Phase 2: write, the applied record last.
	removedSessions, err := o.sessions.RevokeUser(t.UserID, t.TenantID)
	if err != nil {
		return nil, erasure.WithDetail(erasureDetailSessions, fmt.Errorf("persist sessions.json: %w", err))
	}
	if removedResets > 0 {
		if err := writePasswordResetFileLocked(keptResets); err != nil {
			return nil, erasure.WithDetail(erasureDetailResetsWrite, fmt.Errorf("persist password-resets.json: %w", err))
		}
	}
	removedParental := 0
	if hadParental {
		delete(legacy.Entries, t.UserID)
		if err := writeFile0600(parentalFilePath(), legacy.Entries); err != nil {
			return nil, erasure.WithDetail(erasureDetailParentalWrite, fmt.Errorf("persist parental.json: %w", err))
		}
		removedParental = 1
	}
	rec.Applied[t.ErasureID] = o.now().UTC().Format(time.RFC3339)
	rec.Version = erasureAppliedFormatVersion
	if err := writeFile0600(erasureAppliedFilePath(), rec); err != nil {
		return nil, erasure.WithDetail(erasureDetailAppliedWrite, fmt.Errorf("persist erasure-applied.json: %w", err))
	}
	return erasure.Counts{
		"sessions":         int64(removedSessions),
		"password_resets":  int64(removedResets),
		"parental_entries": int64(removedParental),
	}, nil
}

// Verify implements erasure.Verifier: rows that still carry the user id. It
// reads the three stores again from disk or memory; legacy username-keyed
// requests carry no id and are not counted.
func (o *ErasureOwner) Verify(_ context.Context, t erasure.Tombstone) (int, error) {
	remaining := o.sessions.CountUser(t.UserID, t.TenantID)

	resets, err := readPasswordResetFile()
	if err != nil {
		return 0, fmt.Errorf("read password-resets.json: %w", err)
	}
	for _, e := range resets.Requests {
		if e.UserID != "" && e.UserID == t.UserID {
			remaining++
		}
	}

	legacy, err := readParentalLegacy()
	if err != nil {
		return 0, err
	}
	if _, ok := legacy.Entries[t.UserID]; ok {
		remaining++
	}
	return remaining, nil
}
