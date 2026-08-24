// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package plumbline

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/rimsky-ai/rimsky-core/tools/logkind-lint/scan"
)

// @decision: structured-log-kind-format
func TestStructuredLogKindFormat(t *testing.T) {
	repoRoot := findRepoRoot(t)

	violations, err := scan.ProcessLogViolations(repoRoot)
	if err != nil {
		t.Fatalf("scan process-log emit sites for kind format: %v", err)
	}
	baselinePath := filepath.Join(repoRoot, "tools", "logkind-lint", "baseline.json")
	raw, err := os.ReadFile(baselinePath)
	if err != nil {
		t.Fatalf("read %s: %v (generate it with `go run ./tools/logkind-lint`)", baselinePath, err)
	}
	baseline := map[string]int{}
	if err := json.Unmarshal(raw, &baseline); err != nil {
		t.Fatalf("parse %s: %v", baselinePath, err)
	}

	if len(baseline) > 0 {
		t.Errorf("%s records %d file(s) of malformed log kinds; decision:structured-log-kind-format "+
			"claims an empty baseline, so the gate is absolute: rename the kind rather than recording it",
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
			t.Errorf("%s has %d malformed log kind(s), baseline allows %d — every process-log emit site names its "+
				"kind as a raw string literal in the form SUBSYSTEM.NOUN.VERB, upper-case dotted segments of "+
				"letters and digits. Prose belongs in a field, never in the kind.", file, n, allowed)
		}
	}
	for file, allowed := range baseline {
		if current[file] < allowed {
			t.Errorf("%s now has %d malformed log kind(s) but the baseline still records %d — the backlog "+
				"drained; lock the gain in with `go run ./tools/logkind-lint`", file, current[file], allowed)
		}
	}
}

// @decision: structured-log-kind-format
func TestEveryProcessLogKindIsReadableAtItsEmitSite(t *testing.T) {
	violations, err := scan.ProcessLogViolations(findRepoRoot(t))
	if err != nil {
		t.Fatalf("scan process-log emit sites for kind format: %v", err)
	}
	for _, v := range scan.NonBaselineable(violations) {
		t.Errorf("%s:%d [%s] %s — %s", v.File, v.Line, v.Kind, v.Source, v.Detail)
	}
	if t.Failed() {
		t.Log("a message the scan cannot read statically carries no kind, so the baseline cannot record it. " +
			"Name the kind literally at the emit site and put the varying part in a field.")
	}
}
