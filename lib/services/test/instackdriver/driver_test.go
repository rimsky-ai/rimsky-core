// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package instackdriver

import (
	"context"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

func TestRunnerExecutesSuitesInsideStackNetwork(t *testing.T) {
	ctx := context.Background()
	ep := harness.BootInStackProfile(ctx, t)
	harness.RunInStackSuites(ctx, t, ep, "lib/services/test/instack")
}
