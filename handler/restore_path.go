package handler

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// defaultRestoreRoot matches backup-local's restore area in the reference
// compose stacks (BACKUP_RESTORE_DIR=/data/restore, volume backup-restore-data).
const defaultRestoreRoot = "/data/restore"

// restoreRoot is the allow-listed directory restores may target
// (FR-BAK-003 / NFR-SEC-008, RULE-VAL-1). ADMIN_UI_RESTORE_ROOT wins, then
// BACKUP_RESTORE_DIR (shared with the BFF / backup-local), then /data/restore.
func restoreRoot() string {
	for _, k := range []string{"ADMIN_UI_RESTORE_ROOT", "BACKUP_RESTORE_DIR"} {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return filepath.Clean(v)
		}
	}
	return defaultRestoreRoot
}

// errPathOutsideRoot is returned when a user path escapes its allow-listed root.
var errPathOutsideRoot = errors.New("path is outside the allowed root")

// confinePath resolves a user-supplied path inside root (RULE-VAL-1).
//
//   - empty input resolves to root itself;
//   - relative input is joined under root;
//   - absolute input is accepted only when it already lies inside root;
//   - ".." escapes, NUL bytes and non-absolute roots are rejected;
//   - when the target (or its nearest existing ancestor) exists locally,
//     symlinks are evaluated and the real path must still be inside the real root.
func confinePath(root, userPath string) (string, error) {
	if root == "" || !filepath.IsAbs(root) {
		return "", fmt.Errorf("allowed root %q must be an absolute path", root)
	}
	if strings.ContainsRune(userPath, 0) {
		return "", fmt.Errorf("invalid path")
	}
	root = filepath.Clean(root)
	userPath = strings.TrimSpace(userPath)

	var candidate string
	switch {
	case userPath == "":
		candidate = root
	case filepath.IsAbs(userPath):
		candidate = filepath.Clean(userPath)
	default:
		candidate = filepath.Join(root, userPath)
	}
	if !within(root, candidate) {
		return "", errPathOutsideRoot
	}

	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		// Root not present on this host (e.g. backup-local runs in another
		// container): the lexical check above is the confinement.
		return candidate, nil
	}
	realCandidate, err := evalExistingPrefix(candidate)
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}
	if !within(realRoot, realCandidate) {
		return "", errPathOutsideRoot
	}
	return candidate, nil
}

// within reports whether p equals root or lies beneath it (both cleaned).
func within(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel))
}

// evalExistingPrefix resolves symlinks for the longest existing prefix of p and
// re-appends the non-existent remainder.
func evalExistingPrefix(p string) (string, error) {
	var rest []string
	cur := p
	for {
		resolved, err := filepath.EvalSymlinks(cur)
		if err == nil {
			parts := append([]string{resolved}, rest...)
			return filepath.Join(parts...), nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return p, nil
		}
		rest = append([]string{filepath.Base(cur)}, rest...)
		cur = parent
	}
}
