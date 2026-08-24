// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package runtime_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
	"github.com/rimsky-ai/rimsky-core/test/support/pgdbtest"
)

func seedDispositionFixture(ctx context.Context, t *testing.T, d persistence.Database, templateName string) (persistence.InstanceRow, persistence.NodeRow, shared.UUID, shared.UUID, shared.UUID) {
	t.Helper()
	backend := d.Tables()
	tmpl := insertDeployedTemplate(ctx, t, backend, node.TemplateSpec{
		Name: templateName, Version: "1",
		Nodes: []node.TemplateNodeDef{{Type: "worker", Executor: "stub"}},
	})
	ck := "ck-" + uuid.NewString()
	var (
		inst        persistence.InstanceRow
		workerNode  persistence.NodeRow
		mainScopeID shared.UUID
	)
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		i, ms := seedInstanceWithMainScope(ctx, t, backend, tmpl.ID, &ck, tx)
		inst = i
		mainScopeID = ms
		n, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: shared.UUID(uuid.New()), InstanceID: inst.ID, NodeType: "worker", Executor: "stub",
		}, tx)
		if err != nil {
			return err
		}
		workerNode = n
		return nil
	}))
	frameID := seedFrame(ctx, t, backend, inst.ID, mainScopeID)
	runID := seedRunForNode(ctx, t, backend, d.Queue(), workerNode.ID, frameID)
	return inst, workerNode, mainScopeID, frameID, runID
}

func queryDispositionRow(ctx context.Context, t *testing.T, d persistence.Database, runID shared.UUID) (priorID *string, disposition *string, claimedBy *string, state string, lastProgress *time.Time) {
	t.Helper()
	pgdbtest.QueryRowForTest(ctx, t, d,
		`SELECT prior_dispatch_id::text, prior_dispatch_disposition, claimed_by, state, last_progress_at
		   FROM rimsky_node_runs WHERE id = $1`,
		[]any{runID}, &priorID, &disposition, &claimedBy, &state, &lastProgress)
	return priorID, disposition, claimedBy, state, lastProgress
}

func TestErrorPolicyRetry_StampsRetryAfterErrorAndBumpsProgressWithScratch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgdbtest.OpenDriver(ctx, t)
	backend := d.Tables()

	inst, workerNode, mainScopeID, frameID, runID := seedDispositionFixture(ctx, t, d, "retry-disposition-stamp")

	maxRetries := 3
	nodeDef := &node.TemplateNodeDef{
		Type: "worker", Executor: "stub",
		MaxRetries: &maxRetries,
		ErrorTypes: map[string]node.ErrorTypePolicy{
			"stub/boom": {Action: "retry"},
		},
	}

	args := runtime.RunArgs{
		Persist: backend, Queue: d.Queue(),
		Clock: shared.SystemClock{}, Logger: shared.SilentLogger{},
		SupervisorID: "sup-retry",
	}
	var (
		gotPrior       *shared.UUID
		gotDisposition string
	)
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		p, disp, err := runtime.ApplyErrorPolicyForTest(ctx, args, runtime.ErrorPolicyTestInput{
			NodeRunID:  runID,
			NodeID:     workerNode.ID,
			InstanceID: inst.ID,
			FrameID:    frameID,
			RunScopeID: mainScopeID,
			NodeType:   "worker",
			Executor:   "stub",
			ErrorClass: "stub/boom",
			NodeDef:    nodeDef,
			Scratch:    []byte("scratch-after-error"),
		}, tx)
		gotPrior = p
		gotDisposition = disp
		return err
	}))

	require.NotNil(t, gotPrior, "error-policy retry must thread prior_dispatch_id onto the superseding dispatch")
	require.Equal(t, runID, *gotPrior)
	require.Equal(t, "retry_after_error", gotDisposition)

	priorID, disposition, _, _, lastProgress := queryDispositionRow(ctx, t, d, runID)
	require.NotNil(t, priorID, "prior_dispatch_id must be persisted so a restarted supervisor re-emits it")
	require.Equal(t, runID.String(), *priorID)
	require.NotNil(t, disposition)
	require.Equal(t, "retry_after_error", *disposition)
	require.NotNil(t, lastProgress,
		"the scratch write and the last_progress_at bump must land in the same transaction")

	var scratch []byte
	pgdbtest.QueryRowForTest(ctx, t, d,
		`SELECT scratch FROM rimsky_node_runs WHERE id = $1`, []any{runID}, &scratch)
	require.Equal(t, "scratch-after-error", string(scratch))
}

func TestSweepExecutorDeadlines_StampsStaleRecoveryOnReleasedRow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgdbtest.OpenDriver(ctx, t)
	backend := d.Tables()

	_, _, _, _, runID := seedDispositionFixture(ctx, t, d, "quiet-period-stale-recovery")

	maxQuiet := 1
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		ok, err := d.Queue().ClaimDispatchRow(ctx, runID, "sup-reaped", tx)
		if err != nil {
			return err
		}
		require.True(t, ok, "seeded run must claim")
		return d.Queue().RegisterAsyncAck(ctx, runID, "ack-"+uuid.NewString(), time.Now().UTC(), &maxQuiet, nil, "", "", tx)
	}))

	farFuture := shared.NewControllableClock(time.Now().UTC().Add(24 * time.Hour))
	require.NoError(t, runtime.SweepExecutorDeadlines(ctx, runtime.ConductorArgs{
		Persist: backend,
		Queue:   d.Queue(),
		Clock:   farFuture,
		Logger:  shared.SilentLogger{},
	}))

	priorID, disposition, claimedBy, state, _ := queryDispositionRow(ctx, t, d, runID)
	require.Nil(t, claimedBy, "quiet-period reap must release the claim")
	require.Equal(t, "stale", state)
	require.NotNil(t, priorID, "quiet-period reap must stamp prior_dispatch_id on the re-dispatched row")
	require.Equal(t, runID.String(), *priorID)
	require.NotNil(t, disposition)
	require.Equal(t, "stale_recovery", *disposition,
		"the quiet-period release stamps stale_recovery: the predecessor released without an outcome")
}
