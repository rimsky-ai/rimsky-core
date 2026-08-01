// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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
