// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
)

func runGlossaryParity(args []string) error {
	fs := flag.NewFlagSet("glossary-parity", flag.ContinueOnError)
	outputPath := fs.String("output", "docs/glossary.md", "path to existing glossary file (relative to repo-root)")
	conceptsDir := fs.String("concepts-dir", "docs/concepts", "path to concept files (relative to repo-root)")
	repoRoot := fs.String("repo-root", ".", "repo root used as exec cwd so `go run ./cmd/rimsky-docs-glossary` resolves")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cmd := exec.Command("go", "run", "./cmd/rimsky-docs-glossary",
		"-concepts-dir="+*conceptsDir, "-output="+*outputPath, "-check=true")
	cmd.Dir = *repoRoot
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("glossary parity failed: %w", err)
	}
	return nil
}
