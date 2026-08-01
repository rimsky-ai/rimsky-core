// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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
