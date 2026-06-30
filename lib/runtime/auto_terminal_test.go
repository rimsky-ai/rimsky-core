// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks/storetest"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
	pgtest "github.com/rimsky-ai/rimsky-core/test/support/pgmigrate"
)

func seedRunForNode(
	ctx context.Context, t *testing.T, sb persistence.Tables, q persistence.Queue,
	nodeID, frameID shared.UUID,
) shared.UUID {
	t.Helper()
	var out shared.UUID
	var mainScopeID shared.UUID
	require.NoError(t, sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		frameRow, err := sb.Frames().GetForObservability(ctx, frameID, tx)
		if err != nil {
			return err
		}
		if frameRow == nil {
			t.Fatalf("seedRunForNode: frame %s missing", frameID)
		}
		mainScopeID = frameRow.RootRunScopeID
		return nil
	}))
	require.NoError(t, sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := q.EnqueueInTx(ctx, persistence.DispatchRequest{
			NodeID:                 nodeID,
			ExecutorName:           "stub",
			RequiredClaimProducers: []string{},
			EnqueuedAt:             time.Now().Add(-1 * time.Second),
			FrameID:                frameID,
			RunScopeID:             mainScopeID,
		}, tx); err != nil {
			return err
		}
		cands, err := q.SelectCandidates(ctx, tx, persistence.SelectCandidatesRequest{
			AcceptedExecutors:      []string{"stub"},
			AcceptedClaimProducers: []string{},
			Limit:                  16,
		})
		if err != nil {
			return err
		}
		for _, c := range cands {
			if c.NodeID == nodeID {
				out = c.DispatchID
				return nil
			}
		}
		t.Fatalf("seedRunForNode: candidate not surfaced for %s", nodeID)
		return nil
	}))
	return out
}

func seedFrame(ctx context.Context, t *testing.T, sb persistence.Tables, instanceID, sourceNodeID, rootScope shared.UUID) shared.UUID {
	t.Helper()
	_ = sourceNodeID
	var frameID shared.UUID
	require.NoError(t, sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		msgID := shared.UUID(uuid.New())
		if err := sb.Messages().Insert(ctx, tx, persistence.EnqueueMessageRequest{
			ID:         msgID,
			InstanceID: instanceID,
			Type:       "test/seed",
			Sender:     "test",
			SenderKind: "operator",
		}); err != nil {
			return err
		}
		fid, err := sb.Frames().InsertFrame(ctx, instanceID, msgID, rootScope, 600000, tx)
		if err != nil {
			return err
		}
		if _, err := sb.Frames().PromoteQueuedFrameToRunning(ctx, fid, tx); err != nil {
			return err
		}
		frameID = fid
		return nil
	}))
	return frameID
}

func seedInstanceWithMainScope(ctx context.Context, t *testing.T, sb persistence.Tables,
	tx persistence.Tx, templateHash string, ck *string,
) (persistence.InstanceRow, shared.UUID) {
	t.Helper()
	instID := shared.UUID(uuid.New())
	mainScopeID := shared.UUID(uuid.New())
	if err := sb.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
		ID:         mainScopeID,
		GraphName:  "main",
		InstanceID: instID,
	}); err != nil {
		t.Fatalf("seedInstanceWithMainScope: RunScopes.Create: %v", err)
	}
	row, err := sb.Instances().Create(ctx, persistence.InstanceCreateInput{
		ID: instID, TemplateHash: templateHash, InstanceKey: ck,
		Params: map[string]any{},
	}, tx)
	if err != nil {
		t.Fatalf("seedInstanceWithMainScope: Instances.Create: %v", err)
	}
	return row, mainScopeID
}

func insertDeployedTemplate(ctx context.Context, t *testing.T, sb persistence.Tables, spec node.TemplateSpec) persistence.TemplateRow {
	t.Helper()
	sum := sha256.Sum256([]byte(spec.Name + ":" + spec.Version))
	hash := "sha256-" + hex.EncodeToString(sum[:])
	var row *persistence.TemplateRow
	require.NoError(t, sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := sb.Templates().Insert(ctx, persistence.TemplateInsertInput{
			ID: hash, Spec: spec, State: persistence.TemplateStateRegistered,
		}, tx); err != nil {
			return err
		}
		if err := sb.Templates().UpdateState(ctx, hash, persistence.TemplateStateDeployed, tx); err != nil {
			return err
		}
		r, err := sb.Templates().GetByHash(ctx, hash, tx)
		row = r
		return err
	}))
	require.NotNil(t, row)
	return *row
}

func TestCheckAndFireResolution_AllCompletedFiresCommit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)
	backend := d.Tables()

	tmpl := insertDeployedTemplate(ctx, t, backend, node.TemplateSpec{
		Name: "auto-term-commit", Version: "1",
	})
	ck := "ck"
	var mainScopeID shared.UUID
	var inst persistence.InstanceRow
	var acqNode, inhNode persistence.NodeRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		i, ms := seedInstanceWithMainScope(ctx, t, backend, tx, tmpl.ID, &ck)
		inst = i
		mainScopeID = ms
		a, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: shared.UUID(uuid.New()), InstanceID: inst.ID, NodeType: "acquirer", Executor: "stub",
		}, tx)
		if err != nil {
			return err
		}
		acqNode = a
		ih, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: shared.UUID(uuid.New()), InstanceID: inst.ID, NodeType: "inheritor", Executor: "stub",
		}, tx)
		if err != nil {
			return err
		}
		inhNode = ih
		return nil
	}))

	reg := locks.NewRegistry()
	stubStore := storetest.NewFake("workspace", claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}})
	reg.Add("workspace", stubStore)

	frameID := seedFrame(ctx, t, backend, inst.ID, acqNode.ID, mainScopeID)
	acqRunID := seedRunForNode(ctx, t, backend, d.Queue(), acqNode.ID, frameID)
	inhRunID := seedRunForNode(ctx, t, backend, d.Queue(), inhNode.ID, frameID)

	storeName := "workspace"
	intent := "rw"
	lockHolderID := shared.UUID(uuid.New())
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := backend.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID: lockHolderID, LockKind: persistence.LockKindScope,
			ProducerName: &storeName, ClaimScopeData: []byte(`"r"`), Address: []byte(`"r"`),
			Intent:             &intent,
			HolderSupervisorID: "sup-A", HolderNodeID: acqNode.ID,
			ExpiresAt: time.Now().Add(10 * time.Minute),
		}, tx); err != nil {
			return err
		}
		if err := backend.ClaimHolders().Insert(ctx, persistence.ClaimHolderInsertInput{
			ID: shared.UUID(uuid.New()), ClaimHandleID: lockHolderID, HolderRunID: acqRunID,
		}, tx); err != nil {
			return err
		}
		return backend.ClaimHolders().Insert(ctx, persistence.ClaimHolderInsertInput{
			ID: shared.UUID(uuid.New()), ClaimHandleID: lockHolderID, HolderRunID: inhRunID,
		}, tx)
	}))

	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := backend.ClaimHolders().CompleteByClaimHandleAndRun(
			ctx, lockHolderID, acqRunID, persistence.ClaimHolderStateCompleted, tx,
		); err != nil {
			return err
		}
		return backend.ClaimHolders().CompleteByClaimHandleAndRun(
			ctx, lockHolderID, inhRunID, persistence.ClaimHolderStateCompleted, tx,
		)
	}))

	args := runtime.RunArgs{
		Persist:       backend,
		ClaimHandles:  backend.ClaimHandles(),
		StoreRegistry: reg,
		Logger:        shared.SilentLogger{},
		SupervisorID:  "sup-A",
	}
	var post func(context.Context)
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		pc, err := runtime.CheckAndFireResolution(ctx, args, tx, lockHolderID)
		post = pc
		return err
	}))
	if post != nil {
		post(ctx)
	}

	var row *persistence.ClaimHandleRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := backend.ClaimHandles().Get(ctx, lockHolderID, tx)
		row = r
		return err
	}))
	require.NotNil(t, row, "auto-terminal must preserve lock-holder past terminal (Promote-not-delete)")
	require.Equal(t, spec.ClaimHandleStateCommitted, row.State,
		"auto-terminal must promote to committed on aggregate-completed")
	require.Empty(t, row.HolderSupervisorID,
		"committed row must have holder_supervisor_id nulled")
	require.NotNil(t, row.ResolvedAt, "committed row must have resolved_at set")

	abandonSeen, commitSeen := false, false
	for _, c := range stubStore.Calls() {
		if c.Verb == "abandon" {
			abandonSeen = true
		}
		if c.Verb == "commit" {
			commitSeen = true
		}
	}
	require.True(t, commitSeen, "aggregate-completed must invoke Commit on the store")
	require.False(t, abandonSeen, "aggregate-completed must NOT invoke Abandon on the store")
}

func TestCheckAndFireResolution_DurableLifetimeIdempotency(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)
	backend := d.Tables()

	tmpl := insertDeployedTemplate(ctx, t, backend, node.TemplateSpec{
		Name: "auto-term-durable-idempotency", Version: "1",
	})
	ck := "ck-durable-idem"
	var mainScopeID shared.UUID
	var inst persistence.InstanceRow
	var acqNode persistence.NodeRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		i, ms := seedInstanceWithMainScope(ctx, t, backend, tx, tmpl.ID, &ck)
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
	stubStore := storetest.NewFake("workspace", claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
	})
	reg.Add("workspace", stubStore)

	frameID := seedFrame(ctx, t, backend, inst.ID, acqNode.ID, mainScopeID)
	acqRunID := seedRunForNode(ctx, t, backend, d.Queue(), acqNode.ID, frameID)

	storeName := "workspace"
	intent := "rw"
	claimHandleID := shared.UUID(uuid.New())
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := backend.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID: claimHandleID, LockKind: persistence.LockKindScope,
			ProducerName: &storeName, ClaimScopeData: []byte(`"durable"`), Address: []byte(`"durable-addr"`),
			Intent:             &intent,
			HolderSupervisorID: "sup-D", HolderNodeID: acqNode.ID,
			ExpiresAt: time.Now().Add(10 * time.Minute),
			IsHeld:    true,
			Lifetime:  "durable",
		}, tx); err != nil {
			return err
		}
		return backend.ClaimHolders().Insert(ctx, persistence.ClaimHolderInsertInput{
			ID: shared.UUID(uuid.New()), ClaimHandleID: claimHandleID, HolderRunID: acqRunID,
		}, tx)
	}))

	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return backend.ClaimHolders().CompleteByClaimHandleAndRun(
			ctx, claimHandleID, acqRunID, persistence.ClaimHolderStateCompleted, tx,
		)
	}))

	args := runtime.RunArgs{
		Persist:       backend,
		ClaimHandles:  backend.ClaimHandles(),
		StoreRegistry: reg,
		Logger:        shared.SilentLogger{},
		SupervisorID:  "sup-D",
	}
	var post func(context.Context)
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		pc, err := runtime.CheckAndFireResolution(ctx, args, tx, claimHandleID)
		post = pc
		return err
	}))
	if post != nil {
		post(ctx)
	}

	var row *persistence.ClaimHandleRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := backend.ClaimHandles().Get(ctx, claimHandleID, tx)
		row = r
		return err
	}))
	require.NotNil(t, row)
	require.Equal(t, spec.ClaimHandleStateCommitted, row.State,
		"durable-Commit must promote the row to state=committed")
	require.Equal(t, spec.ClaimLifetimeDurable, row.Lifetime,
		"durable lifetime preserved")
	require.NotNil(t, row.ResolvedAt, "committed row must have resolved_at set")
	commitCount := 0
	for _, c := range stubStore.Calls() {
		if c.Verb == "commit" {
			commitCount++
		}
	}
	require.Equal(t, 1, commitCount)

	var post2 func(context.Context)
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		pc, err := runtime.CheckAndFireResolution(ctx, args, tx, claimHandleID)
		post2 = pc
		return err
	}))
	if post2 != nil {
		post2(ctx)
	}
	postReEntryCommitCount := 0
	for _, c := range stubStore.Calls() {
		if c.Verb == "commit" {
			postReEntryCommitCount++
		}
	}
	require.Equal(t, 1, postReEntryCommitCount,
		"re-entering CheckAndFireResolution on a held-durable row must NOT re-fire Commit")
}

func seedFanOutParentAndSubclaims(
	ctx context.Context, t *testing.T, backend persistence.Tables,
	parentRunID, parentNodeID shared.UUID, supervisorID string,
	producerName string, policy spec.AggregationPolicy, n int,
) (shared.UUID, []shared.UUID) {
	t.Helper()
	parentID := shared.UUID(uuid.New())
	subIDs := make([]shared.UUID, 0, n)
	policyBytes, mErr := persistence.MarshalAggregationPolicy(policy)
	require.NoError(t, mErr)
	intent := "rw"
	pName := producerName
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := backend.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID:                 parentID,
			LockKind:           persistence.LockKindScope,
			ProducerName:       &pName,
			ClaimScopeData:     []byte(`"parent-scope"`),
			Address:            []byte(`"parent-addr"`),
			Intent:             &intent,
			HolderSupervisorID: supervisorID,
			HolderNodeID:       parentNodeID,
			ExpiresAt:          time.Now().Add(10 * time.Minute),
			NodeRunID:          &parentRunID,
			AggregationPolicy:  policyBytes,
		}, tx); err != nil {
			return err
		}
		for i := 0; i < n; i++ {
			sid := shared.UUID(uuid.New())
			parent := parentID
			if err := backend.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
				ID:                  sid,
				LockKind:            persistence.LockKindScope,
				ProducerName:        &pName,
				ClaimScopeData:      []byte(`"sub-scope"`),
				Address:             []byte(`"sub-addr"`),
				Intent:              &intent,
				HolderSupervisorID:  supervisorID,
				HolderNodeID:        parentNodeID,
				ExpiresAt:           time.Now().Add(10 * time.Minute),
				NodeRunID:           &parentRunID,
				ParentClaimHandleID: &parent,
			}, tx); err != nil {
				return err
			}
			subIDs = append(subIDs, sid)
			if err := backend.ClaimHandles().BumpExpectedChildrenCount(ctx, parentID, supervisorID, 1, tx); err != nil {
				return err
			}
		}
		return nil
	}))
	return parentID, subIDs
}

func resolveSubclaim(
	ctx context.Context, t *testing.T, backend persistence.Tables,
	args runtime.RunArgs, subID, parentID shared.UUID,
	producer locks.ClaimProducer, outcome runtime.TerminalOutcome,
) {
	t.Helper()
	resolveSubclaimWithLifetime(ctx, t, backend, args, subID, parentID, producer, outcome, "subgraph")
}

func resolveSubclaimWithLifetime(
	ctx context.Context, t *testing.T, backend persistence.Tables,
	args runtime.RunArgs, subID, parentID shared.UUID,
	producer locks.ClaimProducer, outcome runtime.TerminalOutcome,
	lifetime spec.ClaimLifetime,
) {
	t.Helper()
	pname := producer.Name()
	var post func(context.Context)
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		pc, err := runtime.ResolveClaimHandleTerminal(ctx, args, tx, runtime.TerminalDecision{
			ClaimHandleID:       subID,
			SupervisorID:        args.SupervisorID,
			Source:              runtime.ActiveTerminal,
			Outcome:             outcome,
			Producer:            producer,
			Scope:               []byte(`"sub-scope"`),
			Address:             []byte(`"sub-addr"`),
			Lifetime:            lifetime,
			ProducerName:        pname,
			ParentClaimHandleID: &parentID,
		})
		post = pc
		return err
	}))
	if post != nil {
		post(ctx)
	}
}

func countCallsOnID(calls []storetest.FakeCall, claimID string, verb string) int {
	n := 0
	for _, c := range calls {
		if string(c.ClaimID) == claimID && c.Verb == verb {
			n++
		}
	}
	return n
}

func TestResolveParentClaimChain_BestEffort_PartialAbandonStillCommits(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)
	backend := d.Tables()

	tmpl := insertDeployedTemplate(ctx, t, backend, node.TemplateSpec{
		Name: "best-effort-fanout", Version: "1",
	})
	ck := "ck-be"
	var mainScopeID shared.UUID
	var inst persistence.InstanceRow
	var parentNode persistence.NodeRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		i, ms := seedInstanceWithMainScope(ctx, t, backend, tx, tmpl.ID, &ck)
		inst = i
		mainScopeID = ms
		p, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: shared.UUID(uuid.New()), InstanceID: inst.ID, NodeType: "parent", Executor: "stub",
		}, tx)
		if err != nil {
			return err
		}
		parentNode = p
		return nil
	}))

	frameID := seedFrame(ctx, t, backend, inst.ID, parentNode.ID, mainScopeID)
	parentRunID := seedRunForNode(ctx, t, backend, d.Queue(), parentNode.ID, frameID)

	reg := locks.NewRegistry()
	store := storetest.NewFake("be-store", claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
	})
	reg.Add("be-store", store)
	args := runtime.RunArgs{
		Persist:       backend,
		ClaimHandles:  backend.ClaimHandles(),
		StoreRegistry: reg,
		Logger:        shared.SilentLogger{},
		SupervisorID:  "sup-BE",
	}

	policy := spec.AggregationPolicy{Kind: spec.AggregationKindBestEffort}
	parentID, subIDs := seedFanOutParentAndSubclaims(
		ctx, t, backend, parentRunID, parentNode.ID, "sup-BE",
		"be-store", policy, 2,
	)
	resolveSubclaim(ctx, t, backend, args, subIDs[0], parentID, store, runtime.OutcomeAbandon)
	resolveSubclaim(ctx, t, backend, args, subIDs[1], parentID, store, runtime.OutcomeCommit)

	require.Equal(t, 1, countCallsOnID(store.Calls(), parentID.String(), "commit"),
		"best_effort with 1 commit + 1 abandon must Commit the parent")
	require.Equal(t, 0, countCallsOnID(store.Calls(), parentID.String(), "abandon"),
		"best_effort must NOT Abandon the parent when at least one child committed")
}

func TestResolveParentClaimChain_Threshold_AbandonWhenBelowMax(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)
	backend := d.Tables()

	tmpl := insertDeployedTemplate(ctx, t, backend, node.TemplateSpec{
		Name: "threshold-fanout", Version: "1",
	})
	ck := "ck-th"
	var mainScopeID shared.UUID
	var inst persistence.InstanceRow
	var parentNode persistence.NodeRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		i, ms := seedInstanceWithMainScope(ctx, t, backend, tx, tmpl.ID, &ck)
		inst = i
		mainScopeID = ms
		p, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: shared.UUID(uuid.New()), InstanceID: inst.ID, NodeType: "parent", Executor: "stub",
		}, tx)
		if err != nil {
			return err
		}
		parentNode = p
		return nil
	}))

	frameID := seedFrame(ctx, t, backend, inst.ID, parentNode.ID, mainScopeID)
	parentRunID := seedRunForNode(ctx, t, backend, d.Queue(), parentNode.ID, frameID)

	reg := locks.NewRegistry()
	store := storetest.NewFake("th-store", claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
	})
	reg.Add("th-store", store)
	args := runtime.RunArgs{
		Persist:       backend,
		ClaimHandles:  backend.ClaimHandles(),
		StoreRegistry: reg,
		Logger:        shared.SilentLogger{},
		SupervisorID:  "sup-TH",
	}

	policy := spec.AggregationPolicy{Kind: spec.AggregationKindThreshold, MaxFailures: 2}
	parentID, subIDs := seedFanOutParentAndSubclaims(
		ctx, t, backend, parentRunID, parentNode.ID, "sup-TH",
		"th-store", policy, 3,
	)
	resolveSubclaim(ctx, t, backend, args, subIDs[0], parentID, store, runtime.OutcomeCommit)
	resolveSubclaim(ctx, t, backend, args, subIDs[1], parentID, store, runtime.OutcomeCommit)
	resolveSubclaim(ctx, t, backend, args, subIDs[2], parentID, store, runtime.OutcomeAbandon)

	require.Equal(t, 1, countCallsOnID(store.Calls(), parentID.String(), "commit"),
		"threshold(2) with abandoned=1 must Commit the parent")
	require.Equal(t, 0, countCallsOnID(store.Calls(), parentID.String(), "abandon"),
		"threshold(2) with abandoned=1 must NOT Abandon the parent")
}

func TestResolveParentClaimChain_Strict_AbandonsOnAnyFail(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)
	backend := d.Tables()

	tmpl := insertDeployedTemplate(ctx, t, backend, node.TemplateSpec{
		Name: "strict-fanout", Version: "1",
	})
	ck := "ck-st"
	var mainScopeID shared.UUID
	var inst persistence.InstanceRow
	var parentNode persistence.NodeRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		i, ms := seedInstanceWithMainScope(ctx, t, backend, tx, tmpl.ID, &ck)
		inst = i
		mainScopeID = ms
		p, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: shared.UUID(uuid.New()), InstanceID: inst.ID, NodeType: "parent", Executor: "stub",
		}, tx)
		if err != nil {
			return err
		}
		parentNode = p
		return nil
	}))

	frameID := seedFrame(ctx, t, backend, inst.ID, parentNode.ID, mainScopeID)
	parentRunID := seedRunForNode(ctx, t, backend, d.Queue(), parentNode.ID, frameID)

	reg := locks.NewRegistry()
	store := storetest.NewFake("st-store", claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
	})
	reg.Add("st-store", store)
	args := runtime.RunArgs{
		Persist:       backend,
		ClaimHandles:  backend.ClaimHandles(),
		StoreRegistry: reg,
		Logger:        shared.SilentLogger{},
		SupervisorID:  "sup-ST",
	}

	policy := spec.AggregationPolicy{Kind: spec.AggregationKindStrict}
	parentID, subIDs := seedFanOutParentAndSubclaims(
		ctx, t, backend, parentRunID, parentNode.ID, "sup-ST",
		"st-store", policy, 2,
	)
	resolveSubclaim(ctx, t, backend, args, subIDs[0], parentID, store, runtime.OutcomeCommit)
	resolveSubclaim(ctx, t, backend, args, subIDs[1], parentID, store, runtime.OutcomeAbandon)

	require.Equal(t, 1, countCallsOnID(store.Calls(), parentID.String(), "abandon"),
		"strict with abandoned=1 must Abandon the parent")
	require.Equal(t, 0, countCallsOnID(store.Calls(), parentID.String(), "commit"),
		"strict with abandoned=1 must NOT Commit the parent")
}

func TestResolveParentClaimChain_ParentHeldWithActiveCoHolders_Defers(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)
	backend := d.Tables()

	tmpl := insertDeployedTemplate(ctx, t, backend, node.TemplateSpec{
		Name: "held-parent-fanout", Version: "1",
	})
	ck := "ck-hp"
	var mainScopeID shared.UUID
	var inst persistence.InstanceRow
	var parentNode, coNode persistence.NodeRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		i, ms := seedInstanceWithMainScope(ctx, t, backend, tx, tmpl.ID, &ck)
		inst = i
		mainScopeID = ms
		p, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: shared.UUID(uuid.New()), InstanceID: inst.ID, NodeType: "parent", Executor: "stub",
		}, tx)
		if err != nil {
			return err
		}
		parentNode = p
		co, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: shared.UUID(uuid.New()), InstanceID: inst.ID, NodeType: "co", Executor: "stub",
		}, tx)
		if err != nil {
			return err
		}
		coNode = co
		return nil
	}))

	frameID := seedFrame(ctx, t, backend, inst.ID, parentNode.ID, mainScopeID)
	parentRunID := seedRunForNode(ctx, t, backend, d.Queue(), parentNode.ID, frameID)
	coRunID := seedRunForNode(ctx, t, backend, d.Queue(), coNode.ID, frameID)

	reg := locks.NewRegistry()
	store := storetest.NewFake("hp-store", claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
	})
	reg.Add("hp-store", store)
	args := runtime.RunArgs{
		Persist:       backend,
		ClaimHandles:  backend.ClaimHandles(),
		StoreRegistry: reg,
		Logger:        shared.SilentLogger{},
		SupervisorID:  "sup-HP",
	}

	policy := spec.AggregationPolicy{Kind: spec.AggregationKindStrict}
	parentID, subIDs := seedFanOutParentAndSubclaims(
		ctx, t, backend, parentRunID, parentNode.ID, "sup-HP",
		"hp-store", policy, 2,
	)
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return backend.ClaimHolders().Insert(ctx, persistence.ClaimHolderInsertInput{
			ID: shared.UUID(uuid.New()), ClaimHandleID: parentID, HolderRunID: coRunID,
		}, tx)
	}))

	resolveSubclaim(ctx, t, backend, args, subIDs[0], parentID, store, runtime.OutcomeCommit)
	resolveSubclaim(ctx, t, backend, args, subIDs[1], parentID, store, runtime.OutcomeCommit)

	require.Equal(t, 0, countCallsOnID(store.Calls(), parentID.String(), "commit"),
		"parent must NOT Commit while a co-holder is still active (issue D)")
	require.Equal(t, 0, countCallsOnID(store.Calls(), parentID.String(), "abandon"),
		"parent must NOT Abandon either while a co-holder is still active")

	var parentRow *persistence.ClaimHandleRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := backend.ClaimHandles().Get(ctx, parentID, tx)
		parentRow = r
		return err
	}))
	require.NotNil(t, parentRow,
		"parent claim_handle must survive while co-holder is still active")

	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return backend.ClaimHolders().CompleteByClaimHandleAndRun(
			ctx, parentID, coRunID, persistence.ClaimHolderStateCompleted, tx,
		)
	}))
	var postCo func(context.Context)
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		pc, err := runtime.CheckAndFireResolution(ctx, args, tx, parentID)
		postCo = pc
		return err
	}))
	if postCo != nil {
		postCo(ctx)
	}

	require.Equal(t, 1, countCallsOnID(store.Calls(), parentID.String(), "commit"),
		"parent must Commit once the last co-holder completes (issue D)")
}

func TestCheckAndFireResolution_ChildrenIncomplete_DefersUntilAllResolve(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)
	backend := d.Tables()

	tmpl := insertDeployedTemplate(ctx, t, backend, node.TemplateSpec{
		Name: "children-quorum-defers", Version: "1",
	})
	ck := "ck-cq"
	var mainScopeID shared.UUID
	var inst persistence.InstanceRow
	var parentNode persistence.NodeRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		i, ms := seedInstanceWithMainScope(ctx, t, backend, tx, tmpl.ID, &ck)
		inst = i
		mainScopeID = ms
		p, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: shared.UUID(uuid.New()), InstanceID: inst.ID, NodeType: "parent", Executor: "stub",
		}, tx)
		if err != nil {
			return err
		}
		parentNode = p
		return nil
	}))

	frameID := seedFrame(ctx, t, backend, inst.ID, parentNode.ID, mainScopeID)
	parentRunID := seedRunForNode(ctx, t, backend, d.Queue(), parentNode.ID, frameID)

	reg := locks.NewRegistry()
	store := storetest.NewFake("cq-store", claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
	})
	reg.Add("cq-store", store)
	args := runtime.RunArgs{
		Persist:       backend,
		ClaimHandles:  backend.ClaimHandles(),
		StoreRegistry: reg,
		Logger:        shared.SilentLogger{},
		SupervisorID:  "sup-CQ",
	}

	policy := spec.AggregationPolicy{Kind: spec.AggregationKindStrict}
	parentID, subIDs := seedFanOutParentAndSubclaims(
		ctx, t, backend, parentRunID, parentNode.ID, "sup-CQ",
		"cq-store", policy, 2,
	)

	resolveSubclaim(ctx, t, backend, args, subIDs[0], parentID, store, runtime.OutcomeCommit)

	parentHolderID := shared.UUID(uuid.New())
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return backend.ClaimHolders().Insert(ctx, persistence.ClaimHolderInsertInput{
			ID: parentHolderID, ClaimHandleID: parentID, HolderRunID: parentRunID,
		}, tx)
	}))
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return backend.ClaimHolders().CompleteByClaimHandleAndRun(
			ctx, parentID, parentRunID, persistence.ClaimHolderStateCompleted, tx,
		)
	}))

	var postCq func(context.Context)
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		pc, err := runtime.CheckAndFireResolution(ctx, args, tx, parentID)
		postCq = pc
		return err
	}))
	if postCq != nil {
		postCq(ctx)
	}

	require.Equal(t, 0, countCallsOnID(store.Calls(), parentID.String(), "commit"),
		"cycle-6 guard must defer parent Commit while children quorum is not met")
	require.Equal(t, 0, countCallsOnID(store.Calls(), parentID.String(), "abandon"),
		"cycle-6 guard must defer parent Abandon while children quorum is not met")

	var parentRow *persistence.ClaimHandleRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := backend.ClaimHandles().Get(ctx, parentID, tx)
		parentRow = r
		return err
	}))
	require.NotNil(t, parentRow,
		"parent claim_handle must survive the deferred cycle-6 quorum check")

	resolveSubclaim(ctx, t, backend, args, subIDs[1], parentID, store, runtime.OutcomeCommit)

	require.Equal(t, 1, countCallsOnID(store.Calls(), parentID.String(), "commit"),
		"parent must Commit via `SettleFromFanoutChild` once the last child resolves")
	require.Equal(t, 0, countCallsOnID(store.Calls(), parentID.String(), "abandon"),
		"parent must NOT Abandon under strict aggregation when both children committed")
}

func TestCheckAndFireResolution_AnyFailedFiresGiveUp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)
	backend := d.Tables()

	tmpl := insertDeployedTemplate(ctx, t, backend, node.TemplateSpec{
		Name: "auto-term-give-up", Version: "1",
	})
	ck := "ck"
	var mainScopeID shared.UUID
	var inst persistence.InstanceRow
	var acqNode, inhNode persistence.NodeRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		i, ms := seedInstanceWithMainScope(ctx, t, backend, tx, tmpl.ID, &ck)
		inst = i
		mainScopeID = ms
		a, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: shared.UUID(uuid.New()), InstanceID: inst.ID, NodeType: "acquirer", Executor: "stub",
		}, tx)
		if err != nil {
			return err
		}
		acqNode = a
		ih, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: shared.UUID(uuid.New()), InstanceID: inst.ID, NodeType: "inheritor", Executor: "stub",
		}, tx)
		if err != nil {
			return err
		}
		inhNode = ih
		return nil
	}))

	reg := locks.NewRegistry()
	stubStore := storetest.NewFake("workspace", claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}})
	reg.Add("workspace", stubStore)

	frameID := seedFrame(ctx, t, backend, inst.ID, acqNode.ID, mainScopeID)
	acqRunID := seedRunForNode(ctx, t, backend, d.Queue(), acqNode.ID, frameID)
	inhRunID := seedRunForNode(ctx, t, backend, d.Queue(), inhNode.ID, frameID)

	storeName := "workspace"
	intent := "rw"
	lockHolderID := shared.UUID(uuid.New())
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := backend.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID: lockHolderID, LockKind: persistence.LockKindScope,
			ProducerName: &storeName, ClaimScopeData: []byte(`"r"`), Address: []byte(`"r"`),
			Intent:             &intent,
			HolderSupervisorID: "sup-G", HolderNodeID: acqNode.ID,
			ExpiresAt: time.Now().Add(10 * time.Minute),
		}, tx); err != nil {
			return err
		}
		if err := backend.ClaimHolders().Insert(ctx, persistence.ClaimHolderInsertInput{
			ID: shared.UUID(uuid.New()), ClaimHandleID: lockHolderID, HolderRunID: acqRunID,
		}, tx); err != nil {
			return err
		}
		return backend.ClaimHolders().Insert(ctx, persistence.ClaimHolderInsertInput{
			ID: shared.UUID(uuid.New()), ClaimHandleID: lockHolderID, HolderRunID: inhRunID,
		}, tx)
	}))
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := backend.ClaimHolders().CompleteByClaimHandleAndRun(
			ctx, lockHolderID, acqRunID, persistence.ClaimHolderStateCompleted, tx,
		); err != nil {
			return err
		}
		return backend.ClaimHolders().CompleteByClaimHandleAndRun(
			ctx, lockHolderID, inhRunID, persistence.ClaimHolderStateFailed, tx,
		)
	}))

	args := runtime.RunArgs{
		Persist:       backend,
		ClaimHandles:  backend.ClaimHandles(),
		StoreRegistry: reg,
		Logger:        shared.SilentLogger{},
		SupervisorID:  "sup-G",
	}
	var postG func(context.Context)
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		pc, err := runtime.CheckAndFireResolution(ctx, args, tx, lockHolderID)
		postG = pc
		return err
	}))
	if postG != nil {
		postG(ctx)
	}

	var row *persistence.ClaimHandleRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := backend.ClaimHandles().Get(ctx, lockHolderID, tx)
		row = r
		return err
	}))
	require.NotNil(t, row, "auto-terminal must preserve lock-holder past terminal (Promote-not-delete)")
	require.Equal(t, spec.ClaimHandleStateAbandoned, row.State,
		"auto-terminal must promote to abandoned on aggregate-failed")
	require.Empty(t, row.HolderSupervisorID,
		"abandoned row must have holder_supervisor_id nulled")
	require.NotNil(t, row.ResolvedAt, "abandoned row must have resolved_at set")

	abandonSeen, commitSeen := false, false
	for _, c := range stubStore.Calls() {
		if c.Verb == "abandon" {
			abandonSeen = true
		}
		if c.Verb == "commit" {
			commitSeen = true
		}
	}
	require.True(t, abandonSeen, "aggregate-failed must invoke Abandon on the store")
	require.False(t, commitSeen, "aggregate-failed must NOT invoke Commit on the store")
}

func TestResolveParentClaimChain_BestEffort_AllDurableCommits(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)
	backend := d.Tables()

	tmpl := insertDeployedTemplate(ctx, t, backend, node.TemplateSpec{
		Name: "best-effort-all-durable", Version: "1",
	})
	ck := "ck-be-dur"
	var mainScopeID shared.UUID
	var inst persistence.InstanceRow
	var parentNode persistence.NodeRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		i, ms := seedInstanceWithMainScope(ctx, t, backend, tx, tmpl.ID, &ck)
		inst = i
		mainScopeID = ms
		p, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: shared.UUID(uuid.New()), InstanceID: inst.ID, NodeType: "parent", Executor: "stub",
		}, tx)
		if err != nil {
			return err
		}
		parentNode = p
		return nil
	}))

	frameID := seedFrame(ctx, t, backend, inst.ID, parentNode.ID, mainScopeID)
	parentRunID := seedRunForNode(ctx, t, backend, d.Queue(), parentNode.ID, frameID)

	reg := locks.NewRegistry()
	store := storetest.NewFake("be-dur-store", claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
	})
	reg.Add("be-dur-store", store)
	args := runtime.RunArgs{
		Persist:       backend,
		ClaimHandles:  backend.ClaimHandles(),
		StoreRegistry: reg,
		Logger:        shared.SilentLogger{},
		SupervisorID:  "sup-BD",
	}

	policy := spec.AggregationPolicy{Kind: spec.AggregationKindBestEffort}
	parentID, subIDs := seedFanOutParentAndSubclaims(
		ctx, t, backend, parentRunID, parentNode.ID, "sup-BD",
		"be-dur-store", policy, 2,
	)
	resolveSubclaimWithLifetime(ctx, t, backend, args, subIDs[0], parentID, store, runtime.OutcomeCommit, spec.ClaimLifetimeDurable)
	resolveSubclaimWithLifetime(ctx, t, backend, args, subIDs[1], parentID, store, runtime.OutcomeCommit, spec.ClaimLifetimeDurable)

	require.Equal(t, 1, countCallsOnID(store.Calls(), parentID.String(), "commit"),
		"best_effort with all-durable-Commit children must Commit the parent (counters must bump despite durable-promotion early return)")
	require.Equal(t, 0, countCallsOnID(store.Calls(), parentID.String(), "abandon"),
		"best_effort with all-durable-Commit children must NOT Abandon the parent")

	for _, sid := range subIDs {
		var row *persistence.ClaimHandleRow
		require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			r, err := backend.ClaimHandles().Get(ctx, sid, tx)
			row = r
			return err
		}))
		require.NotNil(t, row, "Commit child must survive auto-terminal (Promote-not-delete)")
		require.Equal(t, spec.ClaimHandleStateCommitted, row.State,
			"Commit child must be promoted to state=committed")
	}
}

func TestResolveParentClaimChain_StrictCancelSiblings_AbandonForcesOtherChildren(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)
	backend := d.Tables()

	tmpl := insertDeployedTemplate(ctx, t, backend, node.TemplateSpec{
		Name: "cancel-siblings-fanout", Version: "1",
	})
	ck := "ck-cs"
	var mainScopeID shared.UUID
	var inst persistence.InstanceRow
	var parentNode persistence.NodeRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		i, ms := seedInstanceWithMainScope(ctx, t, backend, tx, tmpl.ID, &ck)
		inst = i
		mainScopeID = ms
		p, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: shared.UUID(uuid.New()), InstanceID: inst.ID, NodeType: "parent", Executor: "stub",
		}, tx)
		if err != nil {
			return err
		}
		parentNode = p
		return nil
	}))

	frameID := seedFrame(ctx, t, backend, inst.ID, parentNode.ID, mainScopeID)
	parentRunID := seedRunForNode(ctx, t, backend, d.Queue(), parentNode.ID, frameID)

	reg := locks.NewRegistry()
	store := storetest.NewFake("cs-store", claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
	})
	reg.Add("cs-store", store)
	args := runtime.RunArgs{
		Persist:       backend,
		ClaimHandles:  backend.ClaimHandles(),
		StoreRegistry: reg,
		Logger:        shared.SilentLogger{},
		SupervisorID:  "sup-CS",
	}

	policy := spec.AggregationPolicy{
		Kind:           spec.AggregationKindStrict,
		CancelSiblings: true,
	}
	parentID, subIDs := seedFanOutParentAndSubclaims(
		ctx, t, backend, parentRunID, parentNode.ID, "sup-CS",
		"cs-store", policy, 3,
	)

	resolveSubclaim(ctx, t, backend, args, subIDs[0], parentID, store, runtime.OutcomeAbandon)

	for i, sid := range subIDs {
		var row *persistence.ClaimHandleRow
		require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			r, err := backend.ClaimHandles().Get(ctx, sid, tx)
			row = r
			return err
		}))
		require.NotNil(t, row,
			"sub-claim %d row must be preserved past cancel_siblings force-Abandon (Promote-not-delete)", i)
		require.Equal(t, spec.ClaimHandleStateAbandoned, row.State,
			"sub-claim %d must be promoted to state=abandoned after force-Abandon", i)
	}
	var parentRow *persistence.ClaimHandleRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := backend.ClaimHandles().Get(ctx, parentID, tx)
		parentRow = r
		return err
	}))
	require.NotNil(t, parentRow,
		"parent claim_handle row must be preserved past terminal (Promote-not-delete)")
	require.Equal(t, spec.ClaimHandleStateAbandoned, parentRow.State,
		"parent claim_handle must be promoted to state=abandoned after strict aggregator fires Abandon")

	for i, sid := range subIDs {
		require.Equal(t, 1, countCallsOnID(store.Calls(), sid.String(), "abandon"),
			"sub-claim %d must receive exactly one Abandon verb on the producer", i)
		require.Equal(t, 0, countCallsOnID(store.Calls(), sid.String(), "commit"),
			"sub-claim %d must NOT receive Commit under cancel_siblings", i)
	}
	require.Equal(t, 1, countCallsOnID(store.Calls(), parentID.String(), "abandon"),
		"parent must receive Abandon under strict aggregation with any failed child")
	require.Equal(t, 0, countCallsOnID(store.Calls(), parentID.String(), "commit"),
		"parent must NOT Commit when cancel_siblings forced sibling Abandons")
}

func TestResolveParentClaimChain_StrictCancelSiblings_SkipsDurableSibling(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)
	backend := d.Tables()

	tmpl := insertDeployedTemplate(ctx, t, backend, node.TemplateSpec{
		Name: "cancel-siblings-skip-durable", Version: "1",
	})
	ck := "ck-cs-dur"
	var mainScopeID shared.UUID
	var inst persistence.InstanceRow
	var parentNode persistence.NodeRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		i, ms := seedInstanceWithMainScope(ctx, t, backend, tx, tmpl.ID, &ck)
		inst = i
		mainScopeID = ms
		p, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: shared.UUID(uuid.New()), InstanceID: inst.ID, NodeType: "parent", Executor: "stub",
		}, tx)
		if err != nil {
			return err
		}
		parentNode = p
		return nil
	}))

	frameID := seedFrame(ctx, t, backend, inst.ID, parentNode.ID, mainScopeID)
	parentRunID := seedRunForNode(ctx, t, backend, d.Queue(), parentNode.ID, frameID)

	reg := locks.NewRegistry()
	store := storetest.NewFake("cs-dur-store", claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
	})
	reg.Add("cs-dur-store", store)
	args := runtime.RunArgs{
		Persist:       backend,
		ClaimHandles:  backend.ClaimHandles(),
		StoreRegistry: reg,
		Logger:        shared.SilentLogger{},
		SupervisorID:  "sup-CD",
	}

	policy := spec.AggregationPolicy{
		Kind:           spec.AggregationKindStrict,
		CancelSiblings: true,
	}
	parentID, subIDs := seedFanOutParentAndSubclaims(
		ctx, t, backend, parentRunID, parentNode.ID, "sup-CD",
		"cs-dur-store", policy, 3,
	)

	resolveSubclaimWithLifetime(ctx, t, backend, args, subIDs[0], parentID, store, runtime.OutcomeCommit, spec.ClaimLifetimeDurable)

	var s0 *persistence.ClaimHandleRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := backend.ClaimHandles().Get(ctx, subIDs[0], tx)
		s0 = r
		return err
	}))
	require.NotNil(t, s0)
	require.Equal(t, spec.ClaimHandleStateCommitted, s0.State,
		"sub[0] must be promoted to state=committed on Commit")

	resolveSubclaim(ctx, t, backend, args, subIDs[1], parentID, store, runtime.OutcomeAbandon)

	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := backend.ClaimHandles().Get(ctx, subIDs[0], tx)
		s0 = r
		return err
	}))
	require.NotNil(t, s0,
		"durable-Commit sibling must survive cancel_siblings walk (state=committed filter)")
	require.Equal(t, spec.ClaimHandleStateCommitted, s0.State,
		"sub[0] must remain state=committed (durable-Commit contract)")
	for i, idx := range []int{1, 2} {
		var row *persistence.ClaimHandleRow
		require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			r, err := backend.ClaimHandles().Get(ctx, subIDs[idx], tx)
			row = r
			return err
		}))
		require.NotNil(t, row,
			"non-durable sub-claim slot %d (subID idx %d) row must be preserved past cancel_siblings (Promote-not-delete)", i, idx)
		require.Equal(t, spec.ClaimHandleStateAbandoned, row.State,
			"non-durable sub-claim slot %d (subID idx %d) must be promoted to state=abandoned after cancel_siblings", i, idx)
	}
	var parentRow *persistence.ClaimHandleRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := backend.ClaimHandles().Get(ctx, parentID, tx)
		parentRow = r
		return err
	}))
	require.NotNil(t, parentRow,
		"parent row must be preserved past terminal (Promote-not-delete)")
	require.Equal(t, spec.ClaimHandleStateAbandoned, parentRow.State,
		"parent must be promoted to state=abandoned under strict aggregation once cancel_siblings settles non-durable siblings")

	require.Equal(t, 1, countCallsOnID(store.Calls(), subIDs[0].String(), "commit"),
		"durable sibling must have received Commit (its own durable resolution)")
	require.Equal(t, 0, countCallsOnID(store.Calls(), subIDs[0].String(), "abandon"),
		"durable sibling must NOT be Abandoned by cancel_siblings (durable-Commit contract)")
	require.Equal(t, 1, countCallsOnID(store.Calls(), subIDs[1].String(), "abandon"),
		"triggering sibling must receive its own Abandon")
	require.Equal(t, 1, countCallsOnID(store.Calls(), subIDs[2].String(), "abandon"),
		"in-flight non-durable sibling must be force-Abandoned by cancel_siblings")
	require.Equal(t, 1, countCallsOnID(store.Calls(), parentID.String(), "abandon"),
		"parent must Abandon once cancel_siblings settles the remaining children")
}

func TestResolveParentClaimChain_StrictCancelSiblings_RecursivelyCancelsGrandchildren(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)
	backend := d.Tables()

	tmpl := insertDeployedTemplate(ctx, t, backend, node.TemplateSpec{
		Name: "cancel-siblings-recursive", Version: "1",
	})
	ck := "ck-cs-rec"
	var mainScopeID shared.UUID
	var inst persistence.InstanceRow
	var parentNode persistence.NodeRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		i, ms := seedInstanceWithMainScope(ctx, t, backend, tx, tmpl.ID, &ck)
		inst = i
		mainScopeID = ms
		p, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: shared.UUID(uuid.New()), InstanceID: inst.ID, NodeType: "parent", Executor: "stub",
		}, tx)
		if err != nil {
			return err
		}
		parentNode = p
		return nil
	}))

	frameID := seedFrame(ctx, t, backend, inst.ID, parentNode.ID, mainScopeID)
	parentRunID := seedRunForNode(ctx, t, backend, d.Queue(), parentNode.ID, frameID)

	reg := locks.NewRegistry()
	store := storetest.NewFake("cs-rec-store", claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
	})
	reg.Add("cs-rec-store", store)
	args := runtime.RunArgs{
		Persist:       backend,
		ClaimHandles:  backend.ClaimHandles(),
		StoreRegistry: reg,
		Logger:        shared.SilentLogger{},
		SupervisorID:  "sup-CR",
	}

	policy := spec.AggregationPolicy{
		Kind:           spec.AggregationKindStrict,
		CancelSiblings: true,
	}
	parentID, subIDs := seedFanOutParentAndSubclaims(
		ctx, t, backend, parentRunID, parentNode.ID, "sup-CR",
		"cs-rec-store", policy, 2,
	)

	g1 := shared.UUID(uuid.New())
	g2 := shared.UUID(uuid.New())
	intent := "rw"
	pName := "cs-rec-store"
	sub1Parent := subIDs[1]
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		for _, gid := range []shared.UUID{g1, g2} {
			if err := backend.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
				ID:                  gid,
				LockKind:            persistence.LockKindScope,
				ProducerName:        &pName,
				ClaimScopeData:      []byte(`"grand-scope"`),
				Address:             []byte(`"grand-addr"`),
				Intent:              &intent,
				HolderSupervisorID:  "sup-CR",
				HolderNodeID:        parentNode.ID,
				ExpiresAt:           time.Now().Add(10 * time.Minute),
				NodeRunID:           &parentRunID,
				ParentClaimHandleID: &sub1Parent,
			}, tx); err != nil {
				return err
			}
			if err := backend.ClaimHandles().BumpExpectedChildrenCount(ctx, sub1Parent, "sup-CR", 1, tx); err != nil {
				return err
			}
		}
		return nil
	}))

	resolveSubclaim(ctx, t, backend, args, subIDs[0], parentID, store, runtime.OutcomeAbandon)

	allIDs := []shared.UUID{subIDs[0], subIDs[1], g1, g2, parentID}
	allNames := []string{"sub[0]", "sub[1]", "g1", "g2", "PARENT"}
	for i, id := range allIDs {
		var row *persistence.ClaimHandleRow
		require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			r, err := backend.ClaimHandles().Get(ctx, id, tx)
			row = r
			return err
		}))
		require.NotNilf(t, row,
			"%s claim_handle row must be preserved past recursive cancel_siblings (Promote-not-delete)", allNames[i])
		require.Equalf(t, spec.ClaimHandleStateAbandoned, row.State,
			"%s claim_handle row must be promoted to state=abandoned after recursive cancel_siblings", allNames[i])
	}

	for i, id := range allIDs {
		require.Equalf(t, 1, countCallsOnID(store.Calls(), id.String(), "abandon"),
			"%s must receive exactly one Abandon verb on the producer", allNames[i])
		require.Equalf(t, 0, countCallsOnID(store.Calls(), id.String(), "commit"),
			"%s must NOT receive Commit under recursive cancel_siblings", allNames[i])
	}
}
