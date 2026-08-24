// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: instance
// @concept: frame
// @concept: run-scope

package controlapi

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks/storetest"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
	"github.com/rimsky-ai/rimsky-core/test/support/pgdbtest"
)

func fiveNodeTemplateBody(name string) map[string]any {
	return map[string]any{
		"spec": map[string]any{
			"name":    name,
			"version": "v1",
			"messages": []map[string]any{
				{"type": "system/invalidate"},
			},
			"nodes": []map[string]any{
				{"type": "n-pending", "executor": "worker"},
				{"type": "n-stale", "executor": "worker"},
				{"type": "n-running", "executor": "worker"},
				{"type": "n-held", "executor": "worker"},
				{"type": "n-parked", "executor": "worker"},
			},
		},
	}
}

func newInstanceFromBody(t *testing.T, h *harness, body map[string]any) string {
	t.Helper()
	_, out := h.httpJSON(t, "POST", "/v1/templates", body)
	tplID, _ := out["template_id"].(string)
	require.NotEmpty(t, tplID)
	deployStatus, _ := h.httpJSON(t, "POST", "/v1/templates/"+tplID+"/deploy", map[string]any{})
	require.Equal(t, http.StatusOK, deployStatus)
	status, out := h.httpJSON(t, "POST", "/v1/instances", map[string]any{
		"template":     tplID,
		"instance_key": "kill-ck-" + uuid.NewString(),
	})
	require.Equal(t, http.StatusCreated, status, out)
	id, _ := out["instance_id"].(string)
	require.NotEmpty(t, id)
	return id
}

// @story: instance-create-is-idle
func TestTerminateInstance_ForceFailsAllFiveInFlightStates(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	instID := newInstanceFromBody(t, h, fiveNodeTemplateBody("kill-matrix-"+uuid.NewString()))
	instUUID := mustParseUUID(t, instID)
	frameID, _ := seedFrameForTest(t, ctx, h, instUUID, "test/kill-matrix")
	var rootScope shared.UUID
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		row, err := h.persist.Frames().GetForObservability(ctx, frameID, tx)
		require.NotNil(t, row)
		rootScope = row.RootRunScopeID
		return err
	}))

	runsByState := map[string]shared.UUID{}
	for _, st := range []string{"pending", "stale", "running", "held", "parked"} {
		node := findNodeIDByType(t, h, instUUID, "n-"+st)
		runsByState[st] = seedNodeRunInState(ctx, t, h, node.ID, frameID, rootScope, st)
	}

	status, out := h.httpJSON(t, "POST", "/v1/instances/"+instID+"/terminate", map[string]any{})
	require.Equal(t, http.StatusOK, status, out)

	for st, runID := range runsByState {
		var gotState, gotSignal string
		pgdbtest.QueryRowForTest(ctx, t, h.driver,
			`SELECT state, COALESCE(settling_signal_type, '') FROM rimsky_node_runs WHERE id = $1`,
			[]any{uuid.UUID(runID)}, &gotState, &gotSignal)
		require.Equal(t, "failed", gotState,
			"a %s run must be force-failed by administrative terminate", st)
		require.Equal(t, cascade.SettlingSignalInstanceKilled, gotSignal,
			"a killed %s run must carry the instance_killed settling signal", st)
	}

	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		row, err := h.persist.Frames().GetForObservability(ctx, frameID, tx)
		if err != nil {
			return err
		}
		require.NotNil(t, row)
		require.NotNil(t, row.EndedAt, "kill must stamp ended_at on the in-flight frame")
		require.Equal(t, "terminated", row.State,
			"a frame killed with in-flight runs must derive terminated, not failed/running")
		return nil
	}))
}

func TestTerminateInstance_GenuineFailureStillDerivesFailed(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	instID := newInstanceForMessages(t, h, "kill-genuine-failed")
	instUUID := mustParseUUID(t, instID)
	frameID, _ := seedFrameForTest(t, ctx, h, instUUID, "test/genuine-failed")
	var rootScope shared.UUID
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		row, err := h.persist.Frames().GetForObservability(ctx, frameID, tx)
		require.NotNil(t, row)
		rootScope = row.RootRunScopeID
		return err
	}))
	rootNode := findNodeIDByType(t, h, instUUID, "root")
	runID := seedNodeRunInState(ctx, t, h, rootNode.ID, frameID, rootScope, "stale")
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		sig := "terminal/error/some_genuine_failure"
		return h.persist.Nodes().UpdateState(ctx, runID,
			cascade.NodeStateFailed, cascade.ReasonPolicyGiveUp, &sig, tx)
	}))

	status, out := h.httpJSON(t, "POST", "/v1/instances/"+instID+"/terminate", map[string]any{})
	require.Equal(t, http.StatusOK, status, out)

	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		row, err := h.persist.Frames().GetForObservability(ctx, frameID, tx)
		if err != nil {
			return err
		}
		require.NotNil(t, row)
		require.Equal(t, "failed", row.State,
			"a frame whose own cascade broke must stay failed even when the kill also ends it")
		return nil
	}))
}

// @concept: message
func TestTerminateInstance_CancelsPendingMessages(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	instID := newInstanceForMessages(t, h, "kill-cancel-pending")
	instUUID := mustParseUUID(t, instID)

	msgID := shared.UUID(uuid.New())
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return h.persist.Messages().Insert(ctx, persistence.EnqueueMessageRequest{
			ID:         msgID,
			InstanceID: instUUID,
			Type:       "system/invalidate",
			Sender:     "test-kill",
			SenderKind: "operator",
			ReceivedAt: time.Now().UTC(),
		}, tx)
	}))

	status, out := h.httpJSON(t, "POST", "/v1/instances/"+instID+"/terminate", map[string]any{})
	require.Equal(t, http.StatusOK, status, out)

	var cancelled bool
	var delivered *time.Time
	pgdbtest.QueryRowForTest(ctx, t, h.driver,
		`SELECT cancelled, delivered_at FROM rimsky_messages WHERE id = $1`,
		[]any{uuid.UUID(msgID)}, &cancelled, &delivered)
	require.True(t, cancelled,
		"terminate must cancel the instance's pending queued messages (finding 437)")
	require.Nil(t, delivered)
}

// @concept: run-scope
func TestTerminateInstance_ClosesNestedScopeTreeChildrenFirst(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	body := templateWithClaimProducersAndLocks("kill-scopes-" + uuid.NewString())
	_, out := h.httpJSON(t, "POST", "/v1/templates", body)
	tplID, _ := out["template_id"].(string)
	require.NotEmpty(t, tplID)
	deployStatus, _ := h.httpJSON(t, "POST", "/v1/templates/"+tplID+"/deploy", map[string]any{})
	require.Equal(t, http.StatusOK, deployStatus)
	status, out := h.httpJSON(t, "POST", "/v1/instances", map[string]any{
		"template":     tplID,
		"instance_key": "kill-scopes-ck-" + uuid.NewString(),
	})
	require.Equal(t, http.StatusCreated, status, out)
	instID, _ := out["instance_id"].(string)
	instUUID := mustParseUUID(t, instID)

	frameID, _ := seedFrameForTest(t, ctx, h, instUUID, "test/kill-scopes")
	var rootScope shared.UUID
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		row, err := h.persist.Frames().GetForObservability(ctx, frameID, tx)
		require.NotNil(t, row)
		rootScope = row.RootRunScopeID
		return err
	}))

	claimNode := findNodeIDByType(t, h, instUUID, "claim-topic")
	parentRunID := seedNodeRunInState(ctx, t, h, claimNode.ID, frameID, rootScope, "running")

	subgraphScope := shared.UUID(uuid.New())
	partitionScope := shared.UUID(uuid.New())
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		root := rootScope
		sub := subgraphScope
		if err := h.persist.RunScopes().Create(ctx, persistence.RunScopeRow{
			ID: subgraphScope, ParentRunScopeID: &root, ParentNodeRunID: &parentRunID,
			GraphName: "sub-flow", InstanceID: instUUID,
		}, tx); err != nil {
			return err
		}
		return h.persist.RunScopes().Create(ctx, persistence.RunScopeRow{
			ID: partitionScope, ParentRunScopeID: &sub, ParentNodeRunID: &parentRunID,
			GraphName: "sub-flow", PartitionKey: "partition-a", InstanceID: instUUID,
		}, tx)
	}))

	status, out = h.httpJSON(t, "POST", "/v1/instances/"+instID+"/terminate", map[string]any{})
	require.Equal(t, http.StatusOK, status, out)

	for _, scopeID := range []shared.UUID{rootScope, subgraphScope, partitionScope} {
		var closed *time.Time
		pgdbtest.QueryRowForTest(ctx, t, h.driver,
			`SELECT closed_at FROM rimsky_run_scopes WHERE id = $1`,
			[]any{uuid.UUID(scopeID)}, &closed)
		require.NotNil(t, closed, "terminate must close run scope %s (nested scopes included)", scopeID)
	}

	contentFake, ok := h.producers.Get("content")
	require.True(t, ok)
	fake := contentFake.(*storetest.Fake)
	seqByScope := map[string]int{}
	for _, c := range fake.Calls() {
		if c.Verb != "on_run_scope_terminal" {
			continue
		}
		_, dup := seqByScope[c.RunScopeID]
		require.False(t, dup, "scope %s must fire on_run_scope_terminal exactly once", c.RunScopeID)
		seqByScope[c.RunScopeID] = c.Sequence
	}
	require.Contains(t, seqByScope, rootScope.String())
	require.Contains(t, seqByScope, subgraphScope.String())
	require.Contains(t, seqByScope, partitionScope.String())
	require.Less(t, seqByScope[partitionScope.String()], seqByScope[subgraphScope.String()],
		"grandchild scope must fire before its parent")
	require.Less(t, seqByScope[subgraphScope.String()], seqByScope[rootScope.String()],
		"child scope must fire before the frame root")
}

// @concept: claim-handle
func TestTerminateInstance_AbandonsActiveClaimThroughProducer(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	body := templateWithClaimProducersAndLocks("kill-claims-real-" + uuid.NewString())
	_, out := h.httpJSON(t, "POST", "/v1/templates", body)
	tplID, _ := out["template_id"].(string)
	require.NotEmpty(t, tplID)
	deployStatus, _ := h.httpJSON(t, "POST", "/v1/templates/"+tplID+"/deploy", map[string]any{})
	require.Equal(t, http.StatusOK, deployStatus)
	status, out := h.httpJSON(t, "POST", "/v1/instances", map[string]any{
		"template":     tplID,
		"instance_key": "kill-claims-ck-" + uuid.NewString(),
	})
	require.Equal(t, http.StatusCreated, status, out)
	instID, _ := out["instance_id"].(string)
	instUUID := mustParseUUID(t, instID)

	frameID, _ := seedFrameForTest(t, ctx, h, instUUID, "test/kill-claims-real")
	var rootScope shared.UUID
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		row, err := h.persist.Frames().GetForObservability(ctx, frameID, tx)
		require.NotNil(t, row)
		rootScope = row.RootRunScopeID
		return err
	}))
	claimNode := findNodeIDByType(t, h, instUUID, "claim-topic")
	runID := seedNodeRunInState(ctx, t, h, claimNode.ID, frameID, rootScope, "running")

	producerName := "content"
	intent := "rw"
	handleID := shared.UUID(uuid.New())
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return h.persist.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID:                 handleID,
			LockKind:           persistence.LockKindScope,
			ProducerName:       &producerName,
			ClaimScopeData:     []byte(`"kill-scope"`),
			Intent:             &intent,
			HolderSupervisorID: "test-sup-real",
			HolderNodeID:       claimNode.ID,
			NodeRunID:          &runID,
			ExpiresAt:          time.Now().Add(5 * time.Minute),
			FrameID:            &frameID,
		}, tx)
	}))

	status, out = h.httpJSON(t, "POST", "/v1/instances/"+instID+"/terminate", map[string]any{})
	require.Equal(t, http.StatusOK, status, out)

	var handle *persistence.ClaimHandleRow
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := h.persist.ClaimHandles().Get(ctx, handleID, tx)
		handle = r
		return err
	}))
	require.NotNil(t, handle)
	require.Equal(t, spec.ClaimHandleStateAbandoned, handle.State)

	_, ferr := runtime.FlushProducerVerbOutbox(ctx, runtime.RunArgs{
		Persist:               h.persist,
		ClaimHandles:          h.persist.ClaimHandles(),
		ClaimProducerRegistry: h.producers,
		Logger:                shared.SilentLogger{},
		Clock:                 shared.SystemClock{},
	})
	require.NoError(t, ferr)

	contentFake, ok := h.producers.Get("content")
	require.True(t, ok)
	fake := contentFake.(*storetest.Fake)
	abandoned := false
	for _, c := range fake.Calls() {
		if c.Verb == "abandon" && string(c.ClaimID) == handleID.String() {
			abandoned = true
		}
	}
	require.True(t, abandoned,
		"terminate must resolve an in-flight active claim through the producer's Abandon verb, not a record-only state write")
}

// @concept: instance
func TestPauseResume_NonIdempotentVerbs409(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	instID := newInstanceForMessages(t, h, "pause-409")

	status, out := h.httpJSON(t, "POST", "/v1/instances/"+instID+"/pause", map[string]any{})
	require.Equal(t, http.StatusOK, status, out)
	require.Equal(t, true, out["paused"])
	status, out = h.httpJSON(t, "POST", "/v1/instances/"+instID+"/pause", map[string]any{})
	require.Equal(t, http.StatusConflict, status, out,
		"pausing an already-paused instance must 409, not silently succeed")

	status, out = h.httpJSON(t, "POST", "/v1/instances/"+instID+"/resume", map[string]any{})
	require.Equal(t, http.StatusOK, status, out)
	require.Equal(t, true, out["resumed"])
	status, out = h.httpJSON(t, "POST", "/v1/instances/"+instID+"/resume", map[string]any{})
	require.Equal(t, http.StatusConflict, status, out,
		"resuming a non-paused instance must 409, not silently succeed")
}
