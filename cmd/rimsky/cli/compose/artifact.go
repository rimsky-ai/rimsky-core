// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// artifact.go — per-run forensic-artifact filesystem layer for
// `rimsky compose run`. Implements the walk-up artifact-root discovery,
// the collision-safe run-directory claim, the run-timestamp format, and
// the latest-symlink updater. Pure filesystem operations; no runtime
// dependencies. See @decision: artifact-root-discovery, artifact-layout,
// timestamp-format.
package compose

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"
)

// stagingCounter is appended to every staging file name so two
// concurrent goroutines in the same process — which may observe the
// same time.Now().UnixNano() on coarse-resolution clocks — never
// collide on a staged path.
var stagingCounter atomic.Uint64

// DiscoverArtifactRoot returns the directory that contains (or will
// contain) the `.rimsky/` artifact tree for this run.
//
// When workdirOverride is non-empty, it is created via MkdirAll and
// returned as an absolute path; walk-up discovery is suppressed
// entirely (the override is the explicit operator intent).
//
// Otherwise the function starts at cwd and walks parent directories,
// returning the first ancestor that holds a `.rimsky/` subdirectory.
// When no ancestor carries one, cwd is returned (the caller creates
// `.rimsky/` on first run). The walk stops at the filesystem root —
// `filepath.Dir` is idempotent at "/" on Unix and at the volume root
// on Windows, so a single `parent == dir` check covers both.
func DiscoverArtifactRoot(cwd string, workdirOverride string) (string, error) {
	if workdirOverride != "" {
		if err := os.MkdirAll(workdirOverride, 0o700); err != nil {
			return "", fmt.Errorf("create workdir %q: %w", workdirOverride, err)
		}
		abs, err := filepath.Abs(workdirOverride)
		if err != nil {
			return "", fmt.Errorf("absolute workdir %q: %w", workdirOverride, err)
		}
		return abs, nil
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return "", fmt.Errorf("absolute cwd %q: %w", cwd, err)
	}
	dir := abs
	for {
		candidate := filepath.Join(dir, ".rimsky")
		if info, statErr := os.Stat(candidate); statErr == nil && info.IsDir() {
			return dir, nil
		}
		// @deliberate: parent == dir is the single stop-condition that
		// covers both filesystem roots — `filepath.Dir` is idempotent at
		// "/" on Unix and at `C:\` on Windows, so reaching a root makes
		// parent equal dir on every supported platform.
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return abs, nil
}

// FormatRunTimestamp formats t as a filesystem-safe ISO 8601 UTC
// timestamp (`YYYY-MM-DDTHH-MM-SSZ`). Colons are replaced by hyphens
// so the value is legal in directory names on every supported platform.
// See @decision: timestamp-format.
func FormatRunTimestamp(t time.Time) string {
	return t.UTC().Format("2006-01-02T15-04-05Z")
}

// maxRunDirCollisionSuffix bounds the collision-walker on EnsureRunDir.
// @deliberate: 999 is a finite ceiling so a buggy filesystem (e.g.
// EACCES that masquerades as ErrExist on a misbehaving overlay) cannot
// produce an unbounded loop, while still being a wide enough budget
// that a real run never bumps into it — even coarse-clock test loops
// and CI matrix concurrent runs land far below this.
const maxRunDirCollisionSuffix = 999

// EnsureRunDir computes the per-run directory under root and atomically
// claims it via os.Mkdir. On collision (a sibling run with the same
// timestamp+name landed first), it appends `-2`, `-3`, ... up to
// `maxRunDirCollisionSuffix` until a fresh path lands. The Mkdir call
// is the load-bearing atomicity primitive: two concurrent callers
// probing the same path race in the kernel — one wins with nil error,
// the loser gets ErrExist and retries the next suffix.
//
// On success the function also creates `<runDir>/blobs/` so the blob
// backend has its spill root before any role runner opens persistence.
// If the blobs subdirectory cannot be created (e.g., quota / EACCES /
// EROFS), the just-claimed run-dir slot is removed so the next
// sequential attempt — from this caller or another — can reuse the
// same path rather than walking up the suffix budget. A concurrent
// caller that has already advanced past this slot does not retry it;
// that is benign (one suffix skipped) and the property holds for the
// next sequential attempt.
//
// Mode is 0o700 on the run-dir and the blobs subdirectory: the state.db
// and spilled blob bodies are part of the forensic artifact and may
// contain executor stdout, payloads, claim contents — only the invoking
// UID should read them by default.
func EnsureRunDir(root, timestamp, name string) (string, error) {
	runsRoot := filepath.Join(root, ".rimsky", "runs")
	if err := os.MkdirAll(runsRoot, 0o700); err != nil {
		return "", fmt.Errorf("create runs root %q: %w", runsRoot, err)
	}
	base := filepath.Join(runsRoot, timestamp+"-"+name)
	if err := os.Mkdir(base, 0o700); err == nil {
		if mkErr := os.MkdirAll(filepath.Join(base, "blobs"), 0o700); mkErr != nil {
			// Partial success: release the atomic-claim slot so the
			// caller's next sequential attempt sees a clean path.
			_ = os.RemoveAll(base)
			return "", fmt.Errorf("create blobs dir under %q: %w", base, mkErr)
		}
		return base, nil
	} else if !errors.Is(err, os.ErrExist) {
		return "", fmt.Errorf("claim run dir %q: %w", base, err)
	}
	for suffix := 2; suffix <= maxRunDirCollisionSuffix; suffix++ {
		candidate := fmt.Sprintf("%s-%d", base, suffix)
		err := os.Mkdir(candidate, 0o700)
		if err == nil {
			if mkErr := os.MkdirAll(filepath.Join(candidate, "blobs"), 0o700); mkErr != nil {
				_ = os.RemoveAll(candidate)
				return "", fmt.Errorf("create blobs dir under %q: %w", candidate, mkErr)
			}
			return candidate, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return "", fmt.Errorf("claim run dir %q: %w", candidate, err)
		}
	}
	return "", fmt.Errorf("exhausted run-dir collision suffixes (-2..-%d) under %q; the most likely fix is to remove stale run dirs under %q", maxRunDirCollisionSuffix, base, filepath.Dir(base))
}

// UpdateLatestSymlink atomically points `<root>/.rimsky/latest` at
// runDir.
//
// Update is always the swap path — never plain rename — so concurrent
// readers can never observe a missing dent.
//
// To make the swap path universal even when no live `latest` exists
// yet, the function first attempts a sentinel installation: a symlink
// pointing at `.` (the link directory itself). Two concurrent bootstrap
// callers race on this sentinel via the kernel's symlink-with-EEXIST
// semantics — exactly one wins, the other observes EEXIST and proceeds
// straight to the swap. After the sentinel exists, the actual update
// uses the platform's atomic-dent-swap primitive (swapAtomicInodes),
// which exchanges the temp and live directory entries in one syscall.
// Plain os.Rename over an existing symlink on APFS briefly unlinks the
// target dent — a concurrent readlink can return ENOENT. The swap
// primitive (renamex_np(RENAME_SWAP) on darwin, renameat2(RENAME_EXCHANGE)
// on linux) never unlinks the dent; a concurrent reader always observes
// one of the two valid targets (the sentinel `.` or the previous /
// staged run-dir target).
//
// The symlink stores a path relative to its parent directory so the
// artifact tree remains coherent if the root is moved or copied.
//
// @blessed-invariant: latest-symlink-no-broken-window — on platforms
// that expose the atomic-dent-swap primitive (darwin via
// renamex_np(RENAME_SWAP), linux via renameat2(RENAME_EXCHANGE)), the
// latest directory entry is never unlinked during an update;
// concurrent readlink against the live link path never returns
// os.ErrNotExist and, when it succeeds, always resolves to one of the
// previous or new run-dir targets (or, briefly during first bootstrap,
// the sentinel `.` target). On other platforms the fallback in
// artifact_swap_other.go uses plain os.Rename — atomic at the inode
// level per POSIX, but without the strong no-broken-window guarantee
// the swap primitive delivers; see that file's package comment.
func UpdateLatestSymlink(root, runDir string) error {
	linkDir := filepath.Join(root, ".rimsky")
	if err := os.MkdirAll(linkDir, 0o700); err != nil {
		return fmt.Errorf("create symlink dir %q: %w", linkDir, err)
	}
	linkPath := filepath.Join(linkDir, "latest")
	relTarget, err := filepath.Rel(linkDir, runDir)
	if err != nil {
		return fmt.Errorf("compute relative symlink target from %q to %q: %w", linkDir, runDir, err)
	}

	// Ensure the live link exists before staging the new target. The
	// sentinel target is `.` — a valid symlink that resolves to the
	// link directory itself. Two concurrent bootstrappers race on
	// os.Symlink; the kernel guarantees exactly one wins (EEXIST for
	// the loser), so after this block the live dent is present in
	// every caller's view.
	if sentinelErr := os.Symlink(".", linkPath); sentinelErr != nil && !errors.Is(sentinelErr, os.ErrExist) {
		return fmt.Errorf("install sentinel symlink %q: %w", linkPath, sentinelErr)
	}
	// @constraint: cap the failure mode when a stray non-symlink (a
	// regular file or a directory an unrelated tool created in
	// `.rimsky/`) sits at linkPath. os.Symlink returns ErrExist for
	// that case too, so without this guard control would proceed to
	// the swap call and the kernel would surface a confusing
	// renameat2/renamex_np error. Lstat with no symlink-deref
	// distinguishes "valid symlink already present" (success path)
	// from "operator-foreign object" (precise error).
	if fi, lerr := os.Lstat(linkPath); lerr == nil && fi.Mode()&os.ModeSymlink == 0 {
		return fmt.Errorf("latest at %q is not a symlink (mode %s); refusing to swap", linkPath, fi.Mode())
	}

	// Temp name embeds PID + nanoseconds + a per-process atomic counter
	// so two concurrent callers — across processes (PID) or within one
	// process (counter, also robust to coarse-resolution UnixNano) —
	// never collide on the staging file.
	tmpName := fmt.Sprintf("latest.tmp.%d.%d.%d", os.Getpid(), time.Now().UnixNano(), stagingCounter.Add(1))
	tmpPath := filepath.Join(linkDir, tmpName)
	if symErr := os.Symlink(relTarget, tmpPath); symErr != nil {
		return fmt.Errorf("stage symlink %q -> %q: %w", tmpPath, relTarget, symErr)
	}
	if swapErr := swapAtomicInodes(tmpPath, linkPath); swapErr != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("atomic swap %q <-> %q: %w", tmpPath, linkPath, swapErr)
	}
	// After the swap, tmpPath now holds the previous target's symlink;
	// remove it so the staging slot stays clean.
	_ = os.Remove(tmpPath)
	return nil
}
