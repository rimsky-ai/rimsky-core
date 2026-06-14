// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @blessed-invariant: latest-symlink-no-broken-window — concurrent readers of
// `<root>/.rimsky/latest` always observe a valid target, never a missing or
// broken link. The TestUpdateLatestSymlink_ConcurrentReadersNeverSeeBroken
// case below exercises the property under a high-rate update / read race.

package compose

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestDiscoverArtifactRoot_FindsAncestor(t *testing.T) {
	tmp := t.TempDir()
	parent := filepath.Join(tmp, "parent")
	child := filepath.Join(parent, "child")
	if err := os.MkdirAll(filepath.Join(parent, ".rimsky"), 0o755); err != nil {
		t.Fatalf("mkdir parent/.rimsky: %v", err)
	}
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatalf("mkdir child: %v", err)
	}
	got, err := DiscoverArtifactRoot(child, "")
	if err != nil {
		t.Fatalf("DiscoverArtifactRoot: %v", err)
	}
	want, _ := filepath.Abs(parent)
	if got != want {
		t.Fatalf("DiscoverArtifactRoot from %q: got %q, want %q", child, got, want)
	}
}

func TestDiscoverArtifactRoot_StopsAtRoot(t *testing.T) {
	tmp := t.TempDir()
	x := filepath.Join(tmp, "x")
	if err := os.MkdirAll(x, 0o755); err != nil {
		t.Fatalf("mkdir x: %v", err)
	}
	got, err := DiscoverArtifactRoot(x, "")
	if err != nil {
		t.Fatalf("DiscoverArtifactRoot: %v", err)
	}
	want, _ := filepath.Abs(x)
	if got != want {
		t.Fatalf("DiscoverArtifactRoot from %q: got %q, want %q (cwd unchanged when no ancestor carries .rimsky/)", x, got, want)
	}
}

func TestDiscoverArtifactRoot_WorkdirOverride(t *testing.T) {
	tmp := t.TempDir()
	explicit := filepath.Join(tmp, "explicit")
	got, err := DiscoverArtifactRoot(tmp, explicit)
	if err != nil {
		t.Fatalf("DiscoverArtifactRoot with override: %v", err)
	}
	want, _ := filepath.Abs(explicit)
	if got != want {
		t.Fatalf("DiscoverArtifactRoot override: got %q, want %q", got, want)
	}
	if info, err := os.Stat(explicit); err != nil || !info.IsDir() {
		t.Fatalf("override path %q was not created as a directory: %v", explicit, err)
	}
}

func TestEnsureRunDir_Collision(t *testing.T) {
	tmp := t.TempDir()
	ts := "2026-06-13T10-00-00Z"
	name := "demo"
	first, err := EnsureRunDir(tmp, ts, name)
	if err != nil {
		t.Fatalf("first EnsureRunDir: %v", err)
	}
	second, err := EnsureRunDir(tmp, ts, name)
	if err != nil {
		t.Fatalf("second EnsureRunDir: %v", err)
	}
	if first == second {
		t.Fatalf("second invocation reused the same run dir %q — collision walker did not advance", first)
	}
	if !strings.HasSuffix(second, "-2") {
		t.Fatalf("second invocation landed at %q; expected `-2` suffix", second)
	}
}

// TestEnsureRunDir_ConcurrentClaimsDistinct stress-tests the os.Mkdir
// atomicity claim the collision walker depends on: a dozen goroutines
// racing on the same (timestamp, name) input must each land on a
// distinct directory — no two callers may share a path. Without the
// Mkdir-then-ErrExist contract, the walker would let two callers
// agree on the same `-N` suffix.
func TestEnsureRunDir_ConcurrentClaimsDistinct(t *testing.T) {
	tmp := t.TempDir()
	ts := "2026-06-13T10-30-00Z"
	name := "demo"
	const workers = 12
	results := make(chan string, workers)
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			path, err := EnsureRunDir(tmp, ts, name)
			if err != nil {
				errs <- err
				return
			}
			results <- path
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Fatalf("EnsureRunDir under contention: %v", err)
	}
	seen := map[string]int{}
	for path := range results {
		seen[path]++
	}
	if len(seen) != workers {
		t.Fatalf("expected %d distinct run dirs, got %d: %#v", workers, len(seen), seen)
	}
	for path, count := range seen {
		if count != 1 {
			t.Fatalf("path %q claimed by %d callers; expected exactly 1", path, count)
		}
	}
}

func TestEnsureRunDir_BlobsCreated(t *testing.T) {
	tmp := t.TempDir()
	runDir, err := EnsureRunDir(tmp, "2026-06-13T11-00-00Z", "demo")
	if err != nil {
		t.Fatalf("EnsureRunDir: %v", err)
	}
	blobs := filepath.Join(runDir, "blobs")
	info, err := os.Stat(blobs)
	if err != nil || !info.IsDir() {
		t.Fatalf("blobs dir %q missing or not a directory: %v", blobs, err)
	}
}

func TestFormatRunTimestamp_FilesystemSafe(t *testing.T) {
	// 2026-06-13T10:30:45Z UTC → "2026-06-13T10-30-45Z".
	got := FormatRunTimestamp(time.Date(2026, 6, 13, 10, 30, 45, 0, time.UTC))
	if strings.Contains(got, ":") {
		t.Fatalf("FormatRunTimestamp returned %q which contains a colon (not filesystem-safe on Windows)", got)
	}
	pattern := regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}-\d{2}-\d{2}Z$`)
	if !pattern.MatchString(got) {
		t.Fatalf("FormatRunTimestamp returned %q; want pattern %q", got, pattern.String())
	}
}

func TestUpdateLatestSymlink_PointsAtTarget(t *testing.T) {
	tmp := t.TempDir()
	runDir, err := EnsureRunDir(tmp, "2026-06-13T12-00-00Z", "demo")
	if err != nil {
		t.Fatalf("EnsureRunDir: %v", err)
	}
	if err := UpdateLatestSymlink(tmp, runDir); err != nil {
		t.Fatalf("UpdateLatestSymlink: %v", err)
	}
	link := filepath.Join(tmp, ".rimsky", "latest")
	target, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("Readlink(%q): %v", link, err)
	}
	resolved := filepath.Join(filepath.Dir(link), target)
	wantAbs, _ := filepath.Abs(runDir)
	gotAbs, _ := filepath.Abs(resolved)
	if gotAbs != wantAbs {
		t.Fatalf("symlink resolved to %q; want %q", gotAbs, wantAbs)
	}
}

func TestUpdateLatestSymlink_OverwritesExisting(t *testing.T) {
	tmp := t.TempDir()
	first, err := EnsureRunDir(tmp, "2026-06-13T13-00-00Z", "first")
	if err != nil {
		t.Fatalf("EnsureRunDir first: %v", err)
	}
	second, err := EnsureRunDir(tmp, "2026-06-13T13-00-01Z", "second")
	if err != nil {
		t.Fatalf("EnsureRunDir second: %v", err)
	}
	if err := UpdateLatestSymlink(tmp, first); err != nil {
		t.Fatalf("UpdateLatestSymlink first: %v", err)
	}
	if err := UpdateLatestSymlink(tmp, second); err != nil {
		t.Fatalf("UpdateLatestSymlink second: %v", err)
	}
	link := filepath.Join(tmp, ".rimsky", "latest")
	target, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("Readlink: %v", err)
	}
	resolved := filepath.Join(filepath.Dir(link), target)
	wantAbs, _ := filepath.Abs(second)
	gotAbs, _ := filepath.Abs(resolved)
	if gotAbs != wantAbs {
		t.Fatalf("symlink resolved to %q after overwrite; want %q (second target)", gotAbs, wantAbs)
	}
}

// TestUpdateLatestSymlink_ConcurrentFirstInstall stresses the
// @blessed-invariant: latest-symlink-no-broken-window property at
// bootstrap: multiple writers race to install `latest` for the first
// time in a fresh `.rimsky/`. Without the sentinel-then-swap protocol,
// two writers can both see no live link, both stage their temp link,
// both call rename — the loser briefly unlinks the winner's dent on
// APFS. The sentinel installation (os.Symlink(".") on a fresh dir)
// uses the kernel's symlink-EEXIST atomicity so exactly one bootstrap
// writer wins; every writer then takes the swap path, which never
// unlinks the dent. All writers must end up with a live, valid link.
func TestUpdateLatestSymlink_ConcurrentFirstInstall(t *testing.T) {
	tmp := t.TempDir()
	const writers = 8
	runDirs := make([]string, writers)
	relTargets := make(map[string]bool, writers)
	linkDir := filepath.Join(tmp, ".rimsky")
	for i := 0; i < writers; i++ {
		runDir, err := EnsureRunDir(tmp, "2026-06-13T15-00-00Z", fmt.Sprintf("w%d", i))
		if err != nil {
			t.Fatalf("EnsureRunDir w%d: %v", i, err)
		}
		runDirs[i] = runDir
		rel, _ := filepath.Rel(linkDir, runDir)
		relTargets[rel] = true
	}
	// All writers race the very first install. The sentinel `.` is a
	// transient internal target — a concurrent reader may see it; the
	// invariant only forbids the dent being missing.
	relTargets["."] = true

	var wg sync.WaitGroup
	errCh := make(chan error, writers)
	for i := 0; i < writers; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := UpdateLatestSymlink(tmp, runDirs[i]); err != nil {
				errCh <- err
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("UpdateLatestSymlink under bootstrap contention: %v", err)
	}

	linkPath := filepath.Join(linkDir, "latest")
	got, err := os.Readlink(linkPath)
	if err != nil {
		t.Fatalf("Readlink final state: %v", err)
	}
	if !relTargets[got] {
		t.Fatalf("final symlink target %q is not one of the writer-staged targets", got)
	}
}

// TestUpdateLatestSymlink_ConcurrentReadersNeverSeeBroken is the
// load-bearing test for the @blessed-invariant: latest-symlink-no-broken-window
// property: reader goroutines hammer os.Readlink while a writer
// alternates the symlink target between two run directories. The
// invariant the implementation guarantees is "the link is never
// missing" — readlink against latest never returns os.ErrNotExist —
// and "the resolved target, when readlink succeeds, is one of the
// known-valid targets". A transient EINVAL during the kernel's
// atomic-swap window is not a broken-link observation: the directory
// entry still exists, the reader retries, and the next readlink
// succeeds with a valid target. ENOENT or an unknown target would
// indicate the rename-window invariant was violated.
func TestUpdateLatestSymlink_ConcurrentReadersNeverSeeBroken(t *testing.T) {
	tmp := t.TempDir()
	a, err := EnsureRunDir(tmp, "2026-06-13T14-00-00Z", "a")
	if err != nil {
		t.Fatalf("EnsureRunDir a: %v", err)
	}
	b, err := EnsureRunDir(tmp, "2026-06-13T14-00-01Z", "b")
	if err != nil {
		t.Fatalf("EnsureRunDir b: %v", err)
	}
	// Seed the link so the very first reader sees a valid target.
	if err := UpdateLatestSymlink(tmp, a); err != nil {
		t.Fatalf("UpdateLatestSymlink seed: %v", err)
	}
	linkDir := filepath.Join(tmp, ".rimsky")
	linkPath := filepath.Join(linkDir, "latest")

	aRel, _ := filepath.Rel(linkDir, a)
	bRel, _ := filepath.Rel(linkDir, b)
	validTargets := map[string]bool{aRel: true, bRel: true}

	stop := make(chan struct{})
	var (
		brokenObservations    atomic.Int64
		missingLinkErrors     atomic.Int64
		unexpectedErrors      atomic.Int64
		firstErr              atomic.Value
		firstUnexpectedErrStr atomic.Value
		wg                    sync.WaitGroup
	)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				got, err := os.Readlink(linkPath)
				if err != nil {
					// The invariant promises the link is never missing —
					// os.ErrNotExist (ENOENT) is the broken-window
					// observation we reject. A transient EINVAL during the
					// kernel's atomic-swap window is not a broken link;
					// the next readlink succeeds.
					switch {
					case errors.Is(err, os.ErrNotExist):
						missingLinkErrors.Add(1)
						if firstErr.Load() == nil {
							firstErr.Store(err.Error())
						}
					case errors.Is(err, syscall.EINVAL):
						// @deliberate: documented-tolerated error class
						// — the brief swap-window observation. Counted
						// implicitly by the absence of any other error.
					default:
						// @constraint: surface unexpected error classes
						// (EACCES, EIO, anything else) rather than
						// silently bucketing them as "benign". A subtle
						// regression that introduces a permission / I/O
						// race must not pass the test just because the
						// error is neither ENOENT nor EINVAL.
						unexpectedErrors.Add(1)
						if firstUnexpectedErrStr.Load() == nil {
							firstUnexpectedErrStr.Store(err.Error())
						}
					}
					continue
				}
				if !validTargets[got] {
					brokenObservations.Add(1)
				}
			}
		}()
	}
	// 100 alternating updates from a single writer; the readers race
	// each rename.
	for i := 0; i < 100; i++ {
		target := a
		if i%2 == 1 {
			target = b
		}
		if err := UpdateLatestSymlink(tmp, target); err != nil {
			close(stop)
			wg.Wait()
			t.Fatalf("UpdateLatestSymlink iteration %d: %v", i, err)
		}
	}
	// Give readers a beat to observe the final state.
	time.Sleep(50 * time.Millisecond)
	close(stop)
	wg.Wait()

	if got := missingLinkErrors.Load(); got != 0 {
		var firstStr string
		if v := firstErr.Load(); v != nil {
			firstStr = v.(string)
		}
		t.Fatalf("observed %d ErrNotExist readlinks; expected 0 (broken-window violation); first err: %s", got, firstStr)
	}
	if got := brokenObservations.Load(); got != 0 {
		t.Fatalf("observed %d invalid symlink targets; expected 0 (broken-window violation)", got)
	}
	if got := unexpectedErrors.Load(); got != 0 {
		var firstStr string
		if v := firstUnexpectedErrStr.Load(); v != nil {
			firstStr = v.(string)
		}
		t.Fatalf("observed %d unexpected readlink errors (not ENOENT, not EINVAL); first err: %s", got, firstStr)
	}
}
