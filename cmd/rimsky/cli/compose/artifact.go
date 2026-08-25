// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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

// @decision: timestamp-format
func FormatRunTimestamp(t time.Time) string {
	return t.UTC().Format("2006-01-02T15-04-05Z")
}

const maxRunDirCollisionSuffix = 999

// @decision: artifact-layout
func EnsureRunDir(root, timestamp, name string) (string, error) {
	runsRoot := filepath.Join(root, ".rimsky", "runs")
	if err := os.MkdirAll(runsRoot, 0o700); err != nil {
		return "", fmt.Errorf("create runs root %q: %w", runsRoot, err)
	}
	base := filepath.Join(runsRoot, timestamp+"-"+name)
	if err := os.Mkdir(base, 0o700); err == nil {
		return base, nil
	} else if !errors.Is(err, os.ErrExist) {
		return "", fmt.Errorf("claim run dir %q: %w", base, err)
	}
	for suffix := 2; suffix <= maxRunDirCollisionSuffix; suffix++ {
		candidate := fmt.Sprintf("%s-%d", base, suffix)
		err := os.Mkdir(candidate, 0o700)
		if err == nil {
			return candidate, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return "", fmt.Errorf("claim run dir %q: %w", candidate, err)
		}
	}
	return "", fmt.Errorf("exhausted run-dir collision suffixes (-2..-%d) under %q; the most likely fix is to remove stale run dirs under %q", maxRunDirCollisionSuffix, base, filepath.Dir(base))
}

const staleLatestTmpAge = 5 * time.Minute

func sweepStaleLatestTmp(linkDir string) {
	stale, err := filepath.Glob(filepath.Join(linkDir, "latest.tmp.*"))
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-staleLatestTmpAge)
	for _, p := range stale {
		fi, statErr := os.Lstat(p)
		if statErr != nil {
			continue
		}
		if fi.ModTime().Before(cutoff) {
			_ = os.Remove(p)
		}
	}
}

func UpdateLatestSymlink(root, runDir string) error {
	linkDir := filepath.Join(root, ".rimsky")
	if err := os.MkdirAll(linkDir, 0o700); err != nil {
		return fmt.Errorf("create symlink dir %q: %w", linkDir, err)
	}
	sweepStaleLatestTmp(linkDir)
	linkPath := filepath.Join(linkDir, "latest")
	relTarget, err := filepath.Rel(linkDir, runDir)
	if err != nil {
		return fmt.Errorf("compute relative symlink target from %q to %q: %w", linkDir, runDir, err)
	}

	if fi, lerr := os.Lstat(linkPath); lerr == nil && fi.Mode()&os.ModeSymlink == 0 {
		return fmt.Errorf("latest at %q is not a symlink (mode %s); refusing to replace it", linkPath, fi.Mode())
	}

	tmpName := fmt.Sprintf("latest.tmp.%d.%d.%d", os.Getpid(), time.Now().UnixNano(), stagingCounter.Add(1))
	tmpPath := filepath.Join(linkDir, tmpName)
	if symErr := os.Symlink(relTarget, tmpPath); symErr != nil {
		return fmt.Errorf("stage symlink %q -> %q: %w", tmpPath, relTarget, symErr)
	}
	if renameErr := os.Rename(tmpPath, linkPath); renameErr != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("atomic rename %q -> %q: %w", tmpPath, linkPath, renameErr)
	}
	return nil
}
