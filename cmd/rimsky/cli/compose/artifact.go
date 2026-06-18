// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package compose

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"
)

var stagingCounter atomic.Uint64

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
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return abs, nil
}

func FormatRunTimestamp(t time.Time) string {
	return t.UTC().Format("2006-01-02T15-04-05Z")
}

const maxRunDirCollisionSuffix = 999

func EnsureRunDir(root, timestamp, name string) (string, error) {
	runsRoot := filepath.Join(root, ".rimsky", "runs")
	if err := os.MkdirAll(runsRoot, 0o700); err != nil {
		return "", fmt.Errorf("create runs root %q: %w", runsRoot, err)
	}
	base := filepath.Join(runsRoot, timestamp+"-"+name)
	if err := os.Mkdir(base, 0o700); err == nil {
		if mkErr := os.MkdirAll(filepath.Join(base, "blobs"), 0o700); mkErr != nil {
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

// @blessed-invariant: latest-symlink-no-broken-window — on platforms
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

	if sentinelErr := os.Symlink(".", linkPath); sentinelErr != nil && !errors.Is(sentinelErr, os.ErrExist) {
		return fmt.Errorf("install sentinel symlink %q: %w", linkPath, sentinelErr)
	}
	if fi, lerr := os.Lstat(linkPath); lerr == nil && fi.Mode()&os.ModeSymlink == 0 {
		return fmt.Errorf("latest at %q is not a symlink (mode %s); refusing to swap", linkPath, fi.Mode())
	}

	tmpName := fmt.Sprintf("latest.tmp.%d.%d.%d", os.Getpid(), time.Now().UnixNano(), stagingCounter.Add(1))
	tmpPath := filepath.Join(linkDir, tmpName)
	if symErr := os.Symlink(relTarget, tmpPath); symErr != nil {
		return fmt.Errorf("stage symlink %q -> %q: %w", tmpPath, relTarget, symErr)
	}
	if swapErr := swapAtomicInodes(tmpPath, linkPath); swapErr != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("atomic swap %q <-> %q: %w", tmpPath, linkPath, swapErr)
	}
	_ = os.Remove(tmpPath)
	return nil
}
