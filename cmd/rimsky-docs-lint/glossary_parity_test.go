// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// findRepoRoot walks up from the test's cwd looking for go.work. Tests run
// with cwd = the package directory (cmd/rimsky-docs-lint/), so two levels up
// is the repo root. We walk to be defensive against future relayouts.
func findRepoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir := wd
	for i := 0; i < 8; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.work")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Skipf("repo root not found above %s", wd)
	return ""
}

func runGoCmd(t *testing.T, dir string, args []string) error {
	t.Helper()
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stderr
	return cmd.Run()
}

func TestGlossaryParity_DetectsDrift(t *testing.T) {
	repoRoot := findRepoRoot(t)
	tmp := t.TempDir()
	outAbs := filepath.Join(tmp, "glossary.md")
	if err := os.WriteFile(outAbs, []byte("stale content"), 0644); err != nil {
		t.Fatal(err)
	}
	// Both -concepts-dir and -output are passed to the inner binary as-is.
	// We want them to resolve from cmd.Dir = repoRoot, so we pass an absolute
	// path for the temp output and a repo-root-relative path for the fixtures.
	err := runGlossaryParity([]string{
		"-repo-root=" + repoRoot,
		"-concepts-dir=cmd/rimsky-docs-glossary/testdata/concepts",
		"-output=" + outAbs,
	})
	if err == nil {
		t.Fatal("expected drift error")
	}
	if !strings.Contains(err.Error(), "glossary parity failed") {
		t.Errorf("unexpected error shape: %v", err)
	}
}

func TestGlossaryParity_FixtureRoundTrip(t *testing.T) {
	repoRoot := findRepoRoot(t)
	tmp := t.TempDir()
	outAbs := filepath.Join(tmp, "glossary.md")
	// First, generate the canonical output by running with -check=false.
	genCmd := []string{
		"run", "./cmd/rimsky-docs-glossary",
		"-concepts-dir=cmd/rimsky-docs-glossary/testdata/concepts",
		"-output=" + outAbs,
	}
	if err := runGoCmd(t, repoRoot, genCmd); err != nil {
		t.Fatalf("generate: %v", err)
	}
	// Then, parity-check should pass.
	err := runGlossaryParity([]string{
		"-repo-root=" + repoRoot,
		"-concepts-dir=cmd/rimsky-docs-glossary/testdata/concepts",
		"-output=" + outAbs,
	})
	if err != nil {
		t.Errorf("expected parity pass after generate, got %v", err)
	}
}
