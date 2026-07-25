// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks/storetest"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
	"github.com/rimsky-ai/rimsky-core/test/support/pgdbtest"
)

// @concept: inertness
func TestCheckAndFireResolution_ProducerCandidateHandleRoundTripsFromDBToDataProcessor(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgdbtest.OpenDriver(ctx, t)
	backend := d.Tables()

	tmpl := insertDeployedTemplate(ctx, t, backend, node.TemplateSpec{
		Name: "producer-candidate-handle-roundtrip", Version: "1",
	})
	ck := "ck"
	var mainScopeID shared.UUID
	var inst persistence.InstanceRow
	var acqNode persistence.NodeRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		i, ms := seedInstanceWithMainScope(ctx, t, backend, tmpl.ID, &ck, tx)
		inst = i
		mainScopeID = ms
		a, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: shared.UUID(uuid.New()), InstanceID: inst.ID, NodeType: "acquirer", Executor: "stub",
		}, tx)
		if err != nil {
			return err
		}
		acqNode = a
		return nil
	}))

	reg := locks.NewRegistry()
	stubStore := storetest.NewFake("workspace", claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}})
	reg.Add("workspace", stubStore)

	dpClient := newFakeDataProcessingClient("workspace")
	dpReg := newFakeDataProcessingRegistry(dpClient)

	frameID := seedFrame(ctx, t, backend, inst.ID, mainScopeID)
	acqRunID := seedRunForNode(ctx, t, backend, d.Queue(), acqNode.ID, frameID)

	producerName := "workspace"
	intent := "rw"
	claimHandleID := shared.UUID(uuid.New())
	persistedCandidateHandle := []byte("db-persisted-candidate-handle-6f21a8")

	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		insertBytes := append([]byte(nil), persistedCandidateHandle...)
		if err := backend.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID: claimHandleID, LockKind: persistence.LockKindScope,
			ProducerName: &producerName, ClaimScopeData: []byte(`"scope"`), Address: []byte(`"addr"`),
			Intent:                  &intent,
			HolderSupervisorID:      "sup-A",
			HolderNodeID:            acqNode.ID,
			ExpiresAt:               time.Now().Add(10 * time.Minute),
			ProducerCandidateHandle: insertBytes,
		}, tx); err != nil {
			return err
		}
		return backend.ClaimHolders().Insert(ctx, persistence.ClaimHolderInsertInput{
			ID: shared.UUID(uuid.New()), ClaimHandleID: claimHandleID, HolderNodeRunID: acqRunID,
		}, tx)
	}))

	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return backend.ClaimHolders().CompleteByClaimHandleAndRun(
			ctx, claimHandleID, acqRunID, persistence.ClaimHolderStateCompleted, tx,
		)
	}))

	args := runtime.RunArgs{
		Persist:               backend,
		ClaimHandles:          backend.ClaimHandles(),
		ClaimProducerRegistry: reg,
		DataProcessors:        dpReg,
		Logger:                shared.SilentLogger{},
		SupervisorID:          "sup-A",
	}
	args = withSyncVerbFlush(args)
	var post func(context.Context)
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		pc, err := runtime.CheckAndFireResolution(ctx, args, claimHandleID, tx)
		post = pc
		return err
	}))
	if post != nil {
		post(ctx)
	}

	commits := dpClient.Commits()
	require.Len(t, commits, 1, "aggregate-completed with a producer_candidate_handle on the row must fire "+
		"CommitCandidate against the DataProcessor")
	require.Equal(t, persistedCandidateHandle, commits[0].CandidateHandle,
		"CommitCandidate.CandidateHandle must be the exact bytes fetched back from the persisted "+
			"claim_handle row (store -> fetch -> producer), not a stale in-memory value")
}
