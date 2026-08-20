// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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

	if len(baseline) > 0 {
		t.Errorf("%s records %d file(s) of unclassified wall-clock waits; decision:test-wallclock-lint-ratchet "+
			"claims an empty baseline, so the gate is absolute: convert the wait rather than recording it",
			baselinePath, len(baseline))
	}

	current := scan.CountsByFile(violations)
	for file, n := range current {
		allowed := baseline[file]
		if n > allowed {
			for _, v := range violations {
				if v.File == file {
					t.Logf("  %s:%d [%s] %s", v.File, v.Line, v.Kind, v.Source)
				}
			}
			t.Errorf("%s has %d unclassified wall-clock wait(s), baseline allows %d — the ratchet is one-way: "+
				"a deadline that fails a test is a load-dependent verdict, not a verdict. Block on the awaited "+
				"signal, block on the event-log tail, or poll until success and declare the wait's class with "+
				"%s%s plus a justification (the test guard's no-progress watchdog is the only backstop).",
				file, n, allowed, scan.MarkerPrefix, scan.ClassOutcome)
		}
	}
	for file, allowed := range baseline {
		if current[file] < allowed {
			t.Errorf("%s now has %d unclassified wall-clock wait(s) but the baseline still records %d — the backlog "+
				"drained; lock the gain in with `go run ./tools/wallclock-lint`", file, current[file], allowed)
		}
	}
}

// @decision: polling-audit
func TestEveryDeclaredWaitClassTheLintRefusesFailsOutsideTheBaseline(t *testing.T) {
	violations, err := scan.TestCodeViolations(findRepoRoot(t))
	if err != nil {
		t.Fatalf("scan test code for wall-clock verdict idioms: %v", err)
	}
	for _, v := range scan.NonBaselineable(violations) {
		t.Errorf("%s:%d [%s] %s — %s", v.File, v.Line, v.Kind, v.Source, v.Detail)
	}
	if t.Failed() {
		t.Log("the recorded baseline carries the unclassified backlog alone; a wait whose marker declares an " +
			"ordering-dependent class, names no known class, carries no justification, claims an outcome on a " +
			"construct that fails on expiry, or uses the retired unclassified marker fails the gate at once")
	}
}
