// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @decision: env-var-registry

package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/rimsky-ai/rimsky-core/tools/env-registry/scan"
)

func main() {
	repoRoot, err := findRepoRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, "env-registry:", err)
		os.Exit(1)
	}
	reads, err := scan.LiveReads(repoRoot)
	if err != nil {
		fmt.Fprintln(os.Stderr, "env-registry:", err)
		os.Exit(1)
	}
	out := filepath.Join(repoRoot, "tools", "env-registry", "registry.md")
	if err := os.WriteFile(out, []byte(scan.RenderTable(reads)), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "env-registry:", err)
		os.Exit(1)
	}
	fmt.Printf("env-registry: %d variables -> %s\n", len(reads), out)
}

func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.work")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.work not found above %s", dir)
		}
		dir = parent
	}
}
