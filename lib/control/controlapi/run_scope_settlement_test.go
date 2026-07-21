// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: run-scope
// @concept: frame

package controlapi

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks/storetest"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/frame"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
	"github.com/rimsky-ai/rimsky-core/test/support/pgdbtest"
)

func TestFrameSettlement_ClosesRootScopeAndFansOutExactlyOnce(t *testing.T) {
	t.Parallel()
	f := newFanOutFixture(t)
	ctx := context.Background()

	instanceID := seedInstanceForRunScopeFanout(t, f, uuid.NewString())

	rootScope := uuid.New()
	pgdbtest.ExecForTest(ctx, t, f.driver, `
        INSERT INTO rimsky_run_scopes(id, graph_name, instance_id, partition_key, created_at)
        VALUES ($1, 'main', $2, '', now())
    `, rootScope, instanceID)
	msgID := uuid.New()
	pgdbtest.ExecForTest(ctx, t, f.driver, `
        INSERT INTO rimsky_messages
            (id, instance_id, type, sender_kind, sender, payload, received_at, delivered_at)
        VALUES ($1, $2, '', 'operator', 'test', E'{}'::bytea, now(), now())
    `, msgID, instanceID)
	var frameID shared.UUID
	require.NoError(t, f.deps.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		fid, err := f.deps.Persist.Frames().InsertRunningFrame(ctx,
			shared.UUID(instanceID), shared.UUID(msgID), shared.UUID(rootScope), tx)
		frameID = fid
		return err
	}))

	scopeFanout := runtime.FrameRunScopeTerminalFanout(f.deps.Persist, f.driver.AdvisoryLocker(), f.lifecycle,
		func(tplSpec node.TemplateSpec) []string { return LifecyclePeersForSpec(f.deps, tplSpec) })
	require.NotNil(t, scopeFanout)

	require.NoError(t, frame.RunTick(ctx, f.deps.Persist, f.driver.Queue(), silentFrameLogger{}, scopeFanout, nil))

	require.NoError(t, f.deps.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		row, err := f.deps.Persist.Frames().GetForObservability(ctx, frameID, tx)
		if err != nil {
			return err
		}
		require.NotNil(t, row)
		require.NotNil(t, row.EndedAt, "settled frame must be ended by frame-end detection")
		scope, err := f.deps.Persist.RunScopes().GetByID(ctx, shared.UUID(rootScope), tx)
		if err != nil {
			return err
		}
		require.NotNil(t, scope)
		require.NotNil(t, scope.ClosedAt,
			"graceful frame settlement must close the root run scope AT settlement, not defer it to instance teardown")
		return nil
	}))

	countScopeTerminal := func(calls []storetest.FakeCall) int {
		n := 0
		for _, c := range calls {
			if c.Verb == "on_run_scope_terminal" && c.RunScopeID == rootScope.String() {
				require.Equal(t, "frame_settled", c.TerminalReason,
					"settlement fan-out must carry the frame_settled terminal reason")
				n++
			}
		}
		return n
	}
	require.Equal(t, 1, countScopeTerminal(f.alpha.Calls()),
		"alpha must hear exactly one OnRunScopeTerminal for the settled root scope")
	require.Equal(t, 1, countScopeTerminal(f.beta.Calls()),
		"beta must hear exactly one OnRunScopeTerminal for the settled root scope")

	require.NoError(t, frame.RunTick(ctx, f.deps.Persist, f.driver.Queue(), silentFrameLogger{}, scopeFanout, nil))
	require.Equal(t, 1, countScopeTerminal(f.alpha.Calls()),
		"a second tick must not re-fire the settled scope (exactly-once per scope)")
	require.Equal(t, 1, countScopeTerminal(f.beta.Calls()),
		"a second tick must not re-fire the settled scope (exactly-once per scope)")
}
