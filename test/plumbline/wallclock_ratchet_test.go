// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package plumbline

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/rimsky-ai/rimsky-core/tools/wallclock-lint/scan"
)

// @decision: test-wallclock-lint-ratchet
func TestWallclockVerdictRatchet(t *testing.T) {
	repoRoot := findRepoRoot(t)

	violations, err := scan.TestCodeViolations(repoRoot)
	if err != nil {
		t.Fatalf("scan test code for wall-clock verdict idioms: %v", err)
	}
	baselinePath := filepath.Join(repoRoot, "tools", "wallclock-lint", "baseline.json")
	raw, err := os.ReadFile(baselinePath)
	if err != nil {
		t.Fatalf("read %s: %v (generate it with `go run ./tools/wallclock-lint`)", baselinePath, err)
	}
	baseline := map[string]int{}
	if err := json.Unmarshal(raw, &baseline); err != nil {
		t.Fatalf("parse %s: %v", baselinePath, err)
	}

	current := scan.CountsByFile(violations)
	for file, n := range current {
		allowed := baseline[file]
		if n > allowed {
			for _, v := range violations {
				if v.File == file {
					t.Logf("  %s:%d [%s] %s", v.File, v.Line, v.Detector, v.Source)
				}
			}
			t.Errorf("%s has %d wall-clock verdict idiom(s), baseline allows %d — the ratchet is one-way: "+
				"a deadline that fails a test is a load-dependent verdict, not a verdict. Block on the awaited "+
				"signal or poll until success (the suite-level timeout is the only backstop). For a sleep that "+
				"is genuinely not a verdict input (fixture pacing), suppress the site with %q plus a justification.",
				file, n, allowed, scan.SuppressionMarker)
		}
	}
	for file, allowed := range baseline {
		if current[file] < allowed {
			t.Errorf("%s now has %d wall-clock verdict idiom(s) but the baseline still records %d — the backlog "+
				"drained; lock the gain in with `go run ./tools/wallclock-lint`", file, current[file], allowed)
		}
	}
}
