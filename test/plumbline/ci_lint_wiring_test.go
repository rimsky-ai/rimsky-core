// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package plumbline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// @story: clean-lint
// @decision: coding-style
func TestCIPointsThePlumblineLintAtTheVendoredBinary(t *testing.T) {
	repoRoot := findRepoRoot(t)

	vendored := filepath.Join(repoRoot, ".ok-plumbline", "bin", "plumbline")
	if _, err := os.Stat(vendored); err != nil {
		t.Fatalf("the vendored lint binary must exist for CI to run it: %v", err)
	}

	workflow := filepath.Join(repoRoot, ".github", "workflows", "ci.yml")
	raw, err := os.ReadFile(workflow)
	if err != nil {
		t.Fatalf("read %s: %v", workflow, err)
	}
	if !strings.Contains(string(raw), "PLUMBLINE_BIN:") {
		t.Fatal("ci.yml no longer sets PLUMBLINE_BIN; without it the plumbline lint test self-skips and comment-hygiene or citation-resolution violations reach main unchecked")
	}
	if !strings.Contains(string(raw), ".ok-plumbline/bin/plumbline") {
		t.Fatal("ci.yml sets PLUMBLINE_BIN but not to the vendored .ok-plumbline/bin/plumbline; a path CI cannot resolve fails the test rather than running the lint")
	}
}
