// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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

func TestFormatRunTimestamp_FilesystemSafe(t *testing.T) {
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

func TestUpdateLatestSymlink_SweepsStaleTmpEntries(t *testing.T) {
	tmp := t.TempDir()
	linkDir := filepath.Join(tmp, ".rimsky")
	if err := os.MkdirAll(linkDir, 0o700); err != nil {
		t.Fatalf("mkdir %q: %v", linkDir, err)
	}

	stalePath := filepath.Join(linkDir, "latest.tmp.999.111.1")
	if err := os.WriteFile(stalePath, []byte("leaked"), 0o600); err != nil {
		t.Fatalf("write stale entry: %v", err)
	}
	old := time.Now().Add(-staleLatestTmpAge - time.Minute)
	if err := os.Chtimes(stalePath, old, old); err != nil {
		t.Fatalf("backdate stale entry: %v", err)
	}

	freshPath := filepath.Join(linkDir, "latest.tmp.999.222.2")
	if err := os.WriteFile(freshPath, []byte("in-flight"), 0o600); err != nil {
		t.Fatalf("write fresh entry: %v", err)
	}

	runDir, err := EnsureRunDir(tmp, "2026-06-13T14-00-00Z", "sweep")
	if err != nil {
		t.Fatalf("EnsureRunDir: %v", err)
	}
	if err := UpdateLatestSymlink(tmp, runDir); err != nil {
		t.Fatalf("UpdateLatestSymlink: %v", err)
	}

	if _, err := os.Lstat(stalePath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("stale latest.tmp.* entry should have been swept, stat err = %v", err)
	}
	if _, err := os.Lstat(freshPath); err != nil {
		t.Errorf("a recently-modified latest.tmp.* entry should not be swept (could belong to an in-flight concurrent writer), stat err = %v", err)
	}
}

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
					switch {
					case errors.Is(err, os.ErrNotExist):
						missingLinkErrors.Add(1)
						if firstErr.Load() == nil {
							firstErr.Store(err.Error())
						}
					case errors.Is(err, syscall.EINVAL):
					default:
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
