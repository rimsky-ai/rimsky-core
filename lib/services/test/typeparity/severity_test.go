// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package typeparity

import (
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/services/executors/verifier-shape-checks/checks"
)

func TestVerifierChecksSeverityMatchesFoundationSpecSeverity(t *testing.T) {
	if string(checks.SeverityError) != string(spec.SeverityError) {
		t.Fatalf("checks.SeverityError %q diverged from spec.SeverityError %q — the verifier's Severity duplicate must track the foundation enum by value",
			checks.SeverityError, spec.SeverityError)
	}
	if string(checks.SeverityWarning) != string(spec.SeverityWarning) {
		t.Fatalf("checks.SeverityWarning %q diverged from spec.SeverityWarning %q — the verifier's Severity duplicate must track the foundation enum by value",
			checks.SeverityWarning, spec.SeverityWarning)
	}
}
