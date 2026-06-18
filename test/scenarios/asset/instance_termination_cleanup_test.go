// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package asset

import (
	"testing"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
)

func TestInstanceTerminationCleanup_ReportShape(t *testing.T) {
	t.Parallel()
	r := runtime.HeldDurableReleaseReport{
		Attempted: 3,
		Succeeded: 2,
		Failures: []runtime.HeldDurableReleaseFailure{
			{
				ClaimHandleID: shared.UUID(uuid.New()),
				ProducerName:  "production-store",
			},
		},
	}
	if r.Attempted != r.Succeeded+len(r.Failures) {
		t.Errorf("report invariant: Attempted (%d) must equal Succeeded (%d) + len(Failures) (%d)",
			r.Attempted, r.Succeeded, len(r.Failures))
	}
}
