// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: run-scope
// @concept: frame

package controlapi

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks/storetest"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/frame"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
	"github.com/rimsky-ai/rimsky-core/test/support/pgdbtest"
)

func TestFrameSettlement_ClosesRootScopeAndStagesItsTerminalExactlyOnce(t *testing.T) {
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

	// @decision: lifecycle-fanout-after-commit
	delivery := frame.LifecycleDelivery{LateBindServiceProxies: f.deps.LateBindServiceProxies}
	drain := runtime.NewLifecycleReconciler(runtime.LifecycleReconcilerConfig{
		Persist:        f.deps.Persist,
		AdvisoryLocker: f.deps.AdvisoryLocker,
		Subscribers:    f.lifecycle,
		Logger:         shared.SilentLogger{},
	})

	require.NoError(t, frame.RunTick(ctx, f.deps.Persist, f.driver.Queue(), silentFrameLogger{}, delivery, nil))
	drain.DrainOnce(ctx)

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

	require.NoError(t, frame.RunTick(ctx, f.deps.Persist, f.driver.Queue(), silentFrameLogger{}, delivery, nil))
	drain.DrainOnce(ctx)
	require.Equal(t, 1, countScopeTerminal(f.alpha.Calls()),
		"a second tick must not re-fire the settled scope (exactly-once per scope)")
	require.Equal(t, 1, countScopeTerminal(f.beta.Calls()),
		"a second tick must not re-fire the settled scope (exactly-once per scope)")
}

// @concept: run-scope
// @decision: lifecycle-fanout-after-commit
func TestFrameSettlement_StagesNoTerminalForAChildScopeAlreadyClosedAtRendezvous(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	instUUID := mustParseUUID(t, newInstanceFromBody(t, h,
		templateWithClaimProducersAndLocks("settle-closed-child-"+uuid.NewString())))
	frameID, msgID := seedFrameForTest(t, ctx, h, instUUID, "test/settle-closed-child")
	pgdbtest.ExecForTest(ctx, t, h.driver,
		`UPDATE rimsky_messages SET delivered_at = now() WHERE id = $1`, uuid.UUID(msgID))

	rootScope := rootRunScopeOfFrame(t, ctx, h, frameID)
	claimNode := findNodeIDByType(t, h, instUUID, "claim-topic")
	parentRunID := seedNodeRunInState(ctx, t, h, claimNode.ID, frameID, rootScope, string(cascade.NodeStateFresh))

	childScope := shared.UUID(uuid.New())
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		root := rootScope
		if err := h.persist.RunScopes().Create(ctx, persistence.RunScopeRow{
			ID: childScope, ParentRunScopeID: &root, ParentNodeRunID: &parentRunID,
			GraphName: "sub-flow", InstanceID: instUUID,
		}, tx); err != nil {
			return err
		}
		return h.persist.RunScopes().Close(ctx, childScope, tx)
	}))

	h.tickFrameEngine(t)
	drainStagedLifecycle(t, ctx, h)

	terminals := runScopeTerminalsByScope(t, h, "topics-ring")
	require.Equal(t, 1, terminals[rootScope.String()],
		"the settling frame stages exactly one terminal for the root scope it closes")
	require.Zero(t, terminals[childScope.String()],
		"a child scope the supervisor already closed at rendezvous owes no second OnRunScopeTerminal")
}

// @concept: run-scope
// @decision: lifecycle-fanout-after-commit
func TestTerminateInstance_StagesNoTerminalForAChildScopeAlreadyClosedAtRendezvous(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	instID := newInstanceFromBody(t, h,
		templateWithClaimProducersAndLocks("terminate-closed-child-"+uuid.NewString()))
	instUUID := mustParseUUID(t, instID)
	frameID, _ := seedFrameForTest(t, ctx, h, instUUID, "test/terminate-closed-child")
	rootScope := rootRunScopeOfFrame(t, ctx, h, frameID)
	claimNode := findNodeIDByType(t, h, instUUID, "claim-topic")
	parentRunID := seedNodeRunInState(ctx, t, h, claimNode.ID, frameID, rootScope, string(cascade.NodeStateFresh))

	childScope := shared.UUID(uuid.New())
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		root := rootScope
		if err := h.persist.RunScopes().Create(ctx, persistence.RunScopeRow{
			ID: childScope, ParentRunScopeID: &root, ParentNodeRunID: &parentRunID,
			GraphName: "sub-flow", InstanceID: instUUID,
		}, tx); err != nil {
			return err
		}
		return h.persist.RunScopes().Close(ctx, childScope, tx)
	}))

	status, out := h.httpJSON(t, "POST", "/v1/instances/"+instID+"/terminate", map[string]any{})
	require.Equal(t, http.StatusOK, status, out)
	drainStagedLifecycle(t, ctx, h)

	terminals := runScopeTerminalsByScope(t, h, "topics-ring")
	require.Equal(t, 1, terminals[rootScope.String()],
		"terminate stages exactly one terminal for the root scope it closes")
	require.Zero(t, terminals[childScope.String()],
		"a child scope the supervisor already closed at rendezvous owes no second OnRunScopeTerminal")
}

func rootRunScopeOfFrame(t *testing.T, ctx context.Context, h *harness, frameID shared.UUID) shared.UUID {
	t.Helper()
	var root shared.UUID
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		row, err := h.persist.Frames().GetForObservability(ctx, frameID, tx)
		if err != nil {
			return err
		}
		require.NotNil(t, row)
		root = row.RootRunScopeID
		return nil
	}))
	return root
}

func drainStagedLifecycle(t *testing.T, ctx context.Context, h *harness) {
	t.Helper()
	drain := runtime.NewLifecycleReconciler(runtime.LifecycleReconcilerConfig{
		Persist:        h.deps.Persist,
		AdvisoryLocker: h.deps.AdvisoryLocker,
		Subscribers:    h.deps.LifecycleSubs,
		Logger:         shared.SilentLogger{},
	})
	drain.DrainOnce(ctx)
}

func runScopeTerminalsByScope(t *testing.T, h *harness, service string) map[string]int {
	t.Helper()
	producer, ok := h.producers.Get(service)
	require.True(t, ok)
	fake, ok := producer.(*storetest.Fake)
	require.True(t, ok)
	byScope := map[string]int{}
	for _, c := range fake.Calls() {
		if c.Verb != "on_run_scope_terminal" {
			continue
		}
		byScope[c.RunScopeID]++
	}
	return byScope
}
