// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: breakpoint

package runtime

import (
	"context"
	"errors"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

func TestEvaluateBeforeDispatchBreakpoints_InfraErrorFailsClosed(t *testing.T) {
	ctx := context.Background()
	fx := seedCarryForwardFixture(t, ctx)
	acq := makeStatefulCounterAcq(fx, fx.mainScopeID)

	args := RunArgs{Persist: fx.tables, Logger: shared.SilentLogger{}}

	cancelledCtx, cancel := context.WithCancel(ctx)
	cancel()

	merged, err := evaluateBeforeDispatchBreakpoints(cancelledCtx, args, acq, "", map[string]any{"k": "v"}, nil)
	if err == nil {
		t.Fatalf("evaluateBeforeDispatchBreakpoints: a pause-phase infra error must fail closed (block dispatch), got nil error and bag %#v", merged)
	}
	var infraErr *BreakpointInfraError
	if !errors.As(err, &infraErr) {
		t.Fatalf("evaluateBeforeDispatchBreakpoints: expected the caller to surface a *BreakpointInfraError, got %T: %v", err, err)
	}
	if merged != nil {
		t.Fatalf("evaluateBeforeDispatchBreakpoints: fail-closed must not hand back a bag to dispatch with, got %#v", merged)
	}
}
