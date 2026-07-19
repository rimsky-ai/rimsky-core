// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks/storetest"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/peer"
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
				out = c.NodeRunID
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
		fid, err := sb.Frames().InsertRunningFrame(ctx, instanceID, msgID, rootScope, 600000, tx)
		if err != nil {
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
			ID: shared.UUID(uuid.New()), ClaimHandleID: lockHolderID, HolderNodeRunID: acqRunID,
		}, tx); err != nil {
			return err
		}
		return backend.ClaimHolders().Insert(ctx, persistence.ClaimHolderInsertInput{
			ID: shared.UUID(uuid.New()), ClaimHandleID: lockHolderID, HolderNodeRunID: inhRunID,
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
			ID: shared.UUID(uuid.New()), ClaimHandleID: claimHandleID, HolderNodeRunID: acqRunID,
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
	parentNodeRunID, parentNodeID shared.UUID, supervisorID string,
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
			NodeRunID:          &parentNodeRunID,
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
				NodeRunID:           &parentNodeRunID,
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
	parentNodeRunID := seedRunForNode(ctx, t, backend, d.Queue(), parentNode.ID, frameID)

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
		ctx, t, backend, parentNodeRunID, parentNode.ID, "sup-BE",
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
	parentNodeRunID := seedRunForNode(ctx, t, backend, d.Queue(), parentNode.ID, frameID)

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
		ctx, t, backend, parentNodeRunID, parentNode.ID, "sup-TH",
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
	parentNodeRunID := seedRunForNode(ctx, t, backend, d.Queue(), parentNode.ID, frameID)

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
		ctx, t, backend, parentNodeRunID, parentNode.ID, "sup-ST",
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
	parentNodeRunID := seedRunForNode(ctx, t, backend, d.Queue(), parentNode.ID, frameID)
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
		ctx, t, backend, parentNodeRunID, parentNode.ID, "sup-HP",
		"hp-store", policy, 2,
	)
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return backend.ClaimHolders().Insert(ctx, persistence.ClaimHolderInsertInput{
			ID: shared.UUID(uuid.New()), ClaimHandleID: parentID, HolderNodeRunID: coRunID,
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
	parentNodeRunID := seedRunForNode(ctx, t, backend, d.Queue(), parentNode.ID, frameID)

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
		ctx, t, backend, parentNodeRunID, parentNode.ID, "sup-CQ",
		"cq-store", policy, 2,
	)

	resolveSubclaim(ctx, t, backend, args, subIDs[0], parentID, store, runtime.OutcomeCommit)

	parentHolderID := shared.UUID(uuid.New())
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return backend.ClaimHolders().Insert(ctx, persistence.ClaimHolderInsertInput{
			ID: parentHolderID, ClaimHandleID: parentID, HolderNodeRunID: parentNodeRunID,
		}, tx)
	}))
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return backend.ClaimHolders().CompleteByClaimHandleAndRun(
			ctx, parentID, parentNodeRunID, persistence.ClaimHolderStateCompleted, tx,
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
			ID: shared.UUID(uuid.New()), ClaimHandleID: lockHolderID, HolderNodeRunID: acqRunID,
		}, tx); err != nil {
			return err
		}
		return backend.ClaimHolders().Insert(ctx, persistence.ClaimHolderInsertInput{
			ID: shared.UUID(uuid.New()), ClaimHandleID: lockHolderID, HolderNodeRunID: inhRunID,
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
	parentNodeRunID := seedRunForNode(ctx, t, backend, d.Queue(), parentNode.ID, frameID)

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
		ctx, t, backend, parentNodeRunID, parentNode.ID, "sup-BD",
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
	parentNodeRunID := seedRunForNode(ctx, t, backend, d.Queue(), parentNode.ID, frameID)

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
		ctx, t, backend, parentNodeRunID, parentNode.ID, "sup-CS",
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
	parentNodeRunID := seedRunForNode(ctx, t, backend, d.Queue(), parentNode.ID, frameID)

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
		ctx, t, backend, parentNodeRunID, parentNode.ID, "sup-CD",
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

func TestCheckAndFireResolution_HeldSubgraph_DefersUntilAllExpectedMembersJoin(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)
	backend := d.Tables()

	storeName := "held-subgraph-store"
	tmpl := insertDeployedTemplate(ctx, t, backend, node.TemplateSpec{
		Name: "held-subgraph-defers", Version: "1",
		Nodes: []node.TemplateNodeDef{
			{
				Type: "acquirer", Executor: "stub",
				ClaimProducers: []node.NodeClaimProducerRef{
					{Name: storeName, Selector: "/region", Intent: "rw", Alias: "schema"},
				},
			},
			{
				Type: "inheritorA", Executor: "stub",
				Holds: map[string]node.HoldsBinding{"schema": {From: "acquirer"}},
			},
			{
				Type: "inheritorB", Executor: "stub",
				Holds: map[string]node.HoldsBinding{"schema": {From: "acquirer"}},
			},
		},
	})
	ck := "ck-held-subgraph"
	var mainScopeID shared.UUID
	var inst persistence.InstanceRow
	var acqNode, inhANode, inhBNode persistence.NodeRow
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
		ihA, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: shared.UUID(uuid.New()), InstanceID: inst.ID, NodeType: "inheritorA", Executor: "stub",
		}, tx)
		if err != nil {
			return err
		}
		inhANode = ihA
		ihB, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: shared.UUID(uuid.New()), InstanceID: inst.ID, NodeType: "inheritorB", Executor: "stub",
		}, tx)
		if err != nil {
			return err
		}
		inhBNode = ihB
		return nil
	}))

	reg := locks.NewRegistry()
	store := storetest.NewFake(storeName, claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
	})
	reg.Add(storeName, store)

	frameID := seedFrame(ctx, t, backend, inst.ID, acqNode.ID, mainScopeID)
	acqRunID := seedRunForNode(ctx, t, backend, d.Queue(), acqNode.ID, frameID)
	inhARunID := seedRunForNode(ctx, t, backend, d.Queue(), inhANode.ID, frameID)
	inhBRunID := seedRunForNode(ctx, t, backend, d.Queue(), inhBNode.ID, frameID)

	claimHandleID := shared.UUID(uuid.New())
	intent := "rw"
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := backend.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID: claimHandleID, LockKind: persistence.LockKindScope,
			ProducerName: &storeName, ClaimScopeData: []byte(`"/region"`), Address: []byte(`"/region-addr"`),
			Intent:             &intent,
			HolderSupervisorID: "sup-HS", HolderNodeID: acqNode.ID,
			ExpiresAt: time.Now().Add(10 * time.Minute),
			IsHeld:    true,
		}, tx); err != nil {
			return err
		}
		if err := backend.ClaimHolders().Insert(ctx, persistence.ClaimHolderInsertInput{
			ID: shared.UUID(uuid.New()), ClaimHandleID: claimHandleID, HolderNodeRunID: acqRunID,
		}, tx); err != nil {
			return err
		}
		return backend.ClaimHolders().Insert(ctx, persistence.ClaimHolderInsertInput{
			ID: shared.UUID(uuid.New()), ClaimHandleID: claimHandleID, HolderNodeRunID: inhARunID,
		}, tx)
	}))
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := backend.ClaimHolders().CompleteByClaimHandleAndRun(
			ctx, claimHandleID, acqRunID, persistence.ClaimHolderStateCompleted, tx,
		); err != nil {
			return err
		}
		return backend.ClaimHolders().CompleteByClaimHandleAndRun(
			ctx, claimHandleID, inhARunID, persistence.ClaimHolderStateCompleted, tx,
		)
	}))

	args := runtime.RunArgs{
		Persist:       backend,
		Queue:         d.Queue(),
		ClaimHandles:  backend.ClaimHandles(),
		StoreRegistry: reg,
		Logger:        shared.SilentLogger{},
		SupervisorID:  "sup-HS",
	}
	var postA func(context.Context)
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		pc, err := runtime.CheckAndFireResolution(ctx, args, tx, claimHandleID)
		postA = pc
		return err
	}))
	if postA != nil {
		postA(ctx)
	}

	require.Equal(t, 0, countCallsOnID(store.Calls(), claimHandleID.String(), "commit"),
		"expected-inheritor guard must defer Commit while inheritorB (a declared holding-subgraph member) "+
			"has not yet joined as a holder, even though every joined holder (acquirer, inheritorA) is completed")
	require.Equal(t, 0, countCallsOnID(store.Calls(), claimHandleID.String(), "abandon"),
		"deferred resolution must not fire Abandon either while a member is merely missing (not failed)")

	var midRow *persistence.ClaimHandleRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := backend.ClaimHandles().Get(ctx, claimHandleID, tx)
		midRow = r
		return err
	}))
	require.NotNil(t, midRow)
	require.Equal(t, spec.ClaimHandleStateActive, midRow.State,
		"claim handle must remain active while an expected holding-subgraph member has not yet joined")

	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := backend.ClaimHolders().Insert(ctx, persistence.ClaimHolderInsertInput{
			ID: shared.UUID(uuid.New()), ClaimHandleID: claimHandleID, HolderNodeRunID: inhBRunID,
		}, tx); err != nil {
			return err
		}
		return backend.ClaimHolders().CompleteByClaimHandleAndRun(
			ctx, claimHandleID, inhBRunID, persistence.ClaimHolderStateCompleted, tx,
		)
	}))
	var postB func(context.Context)
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		pc, err := runtime.CheckAndFireResolution(ctx, args, tx, claimHandleID)
		postB = pc
		return err
	}))
	if postB != nil {
		postB(ctx)
	}

	require.Equal(t, 1, countCallsOnID(store.Calls(), claimHandleID.String(), "commit"),
		"once the last expected holding-subgraph member (inheritorB) joins and completes, resolution must fire Commit")
	require.Equal(t, 0, countCallsOnID(store.Calls(), claimHandleID.String(), "abandon"))

	var finalRow *persistence.ClaimHandleRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := backend.ClaimHandles().Get(ctx, claimHandleID, tx)
		finalRow = r
		return err
	}))
	require.NotNil(t, finalRow)
	require.Equal(t, spec.ClaimHandleStateCommitted, finalRow.State)
}

// @concept: claim-co-holdership
// @concept: auto-terminal
func TestCheckAndFireResolution_HeldSubgraph_AnyFailedBypassesExpectedMemberGate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)
	backend := d.Tables()

	storeName := "held-subgraph-poison-store"
	tmpl := insertDeployedTemplate(ctx, t, backend, node.TemplateSpec{
		Name: "held-subgraph-any-failed-bypasses-gate", Version: "1",
		Nodes: []node.TemplateNodeDef{
			{
				Type: "acquirer", Executor: "stub",
				ClaimProducers: []node.NodeClaimProducerRef{
					{Name: storeName, Selector: "/region", Intent: "rw", Alias: "schema"},
				},
			},
			{
				Type: "inheritorA", Executor: "stub",
				Holds: map[string]node.HoldsBinding{"schema": {From: "acquirer"}},
			},
			{
				Type: "inheritorB", Executor: "stub",
				Holds: map[string]node.HoldsBinding{"schema": {From: "acquirer"}},
			},
		},
	})
	ck := "ck-held-subgraph-any-failed"
	var mainScopeID shared.UUID
	var inst persistence.InstanceRow
	var acqNode, inhANode persistence.NodeRow
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
		ihA, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: shared.UUID(uuid.New()), InstanceID: inst.ID, NodeType: "inheritorA", Executor: "stub",
		}, tx)
		if err != nil {
			return err
		}
		inhANode = ihA
		_, err = backend.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: shared.UUID(uuid.New()), InstanceID: inst.ID, NodeType: "inheritorB", Executor: "stub",
		}, tx)
		return err
	}))

	reg := locks.NewRegistry()
	store := storetest.NewFake(storeName, claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
	})
	reg.Add(storeName, store)

	frameID := seedFrame(ctx, t, backend, inst.ID, acqNode.ID, mainScopeID)
	acqRunID := seedRunForNode(ctx, t, backend, d.Queue(), acqNode.ID, frameID)
	inhARunID := seedRunForNode(ctx, t, backend, d.Queue(), inhANode.ID, frameID)

	claimHandleID := shared.UUID(uuid.New())
	intent := "rw"
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := backend.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID: claimHandleID, LockKind: persistence.LockKindScope,
			ProducerName: &storeName, ClaimScopeData: []byte(`"/region"`), Address: []byte(`"/region-addr"`),
			Intent:             &intent,
			HolderSupervisorID: "sup-AF", HolderNodeID: acqNode.ID,
			ExpiresAt: time.Now().Add(10 * time.Minute),
			IsHeld:    true,
		}, tx); err != nil {
			return err
		}
		if err := backend.ClaimHolders().Insert(ctx, persistence.ClaimHolderInsertInput{
			ID: shared.UUID(uuid.New()), ClaimHandleID: claimHandleID, HolderNodeRunID: acqRunID,
		}, tx); err != nil {
			return err
		}
		return backend.ClaimHolders().Insert(ctx, persistence.ClaimHolderInsertInput{
			ID: shared.UUID(uuid.New()), ClaimHandleID: claimHandleID, HolderNodeRunID: inhARunID,
		}, tx)
	}))
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := backend.ClaimHolders().CompleteByClaimHandleAndRun(
			ctx, claimHandleID, acqRunID, persistence.ClaimHolderStateCompleted, tx,
		); err != nil {
			return err
		}
		return backend.ClaimHolders().CompleteByClaimHandleAndRun(
			ctx, claimHandleID, inhARunID, persistence.ClaimHolderStateFailed, tx,
		)
	}))

	args := runtime.RunArgs{
		Persist:       backend,
		Queue:         d.Queue(),
		ClaimHandles:  backend.ClaimHandles(),
		StoreRegistry: reg,
		Logger:        shared.SilentLogger{},
		SupervisorID:  "sup-AF",
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

	require.Equal(t, 1, countCallsOnID(store.Calls(), claimHandleID.String(), "abandon"),
		"the poison rule (any-failed) must fire Abandon immediately even though inheritorB, a declared "+
			"holding-subgraph member, never joined as a holder; the expected-inheritor gate is for the "+
			"all-succeeded path only and must not block poison resolution")
	require.Equal(t, 0, countCallsOnID(store.Calls(), claimHandleID.String(), "commit"))

	var finalRow *persistence.ClaimHandleRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := backend.ClaimHandles().Get(ctx, claimHandleID, tx)
		finalRow = r
		return err
	}))
	require.NotNil(t, finalRow)
	require.Equal(t, spec.ClaimHandleStateAbandoned, finalRow.State,
		"a poisoned held claim must resolve to abandoned without waiting for the missing holding-subgraph member")
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
	parentNodeRunID := seedRunForNode(ctx, t, backend, d.Queue(), parentNode.ID, frameID)

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
		ctx, t, backend, parentNodeRunID, parentNode.ID, "sup-CR",
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
				NodeRunID:           &parentNodeRunID,
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

func TestSettleFromFanoutChild_MalformedAggregationPolicy_SafeFallback(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)
	backend := d.Tables()

	tmpl := insertDeployedTemplate(ctx, t, backend, node.TemplateSpec{
		Name: "malformed-policy-fallback", Version: "1",
	})
	ck := "ck-malformed"
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
	parentNodeRunID := seedRunForNode(ctx, t, backend, d.Queue(), parentNode.ID, frameID)

	reg := locks.NewRegistry()
	store := storetest.NewFake("malformed-store", claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
	})
	reg.Add("malformed-store", store)
	args := runtime.RunArgs{
		Persist:       backend,
		ClaimHandles:  backend.ClaimHandles(),
		StoreRegistry: reg,
		Logger:        shared.SilentLogger{},
		SupervisorID:  "sup-MP",
	}

	parentID := shared.UUID(uuid.New())
	sub0 := shared.UUID(uuid.New())
	sub1 := shared.UUID(uuid.New())
	intent := "rw"
	pName := "malformed-store"
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := backend.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID:                 parentID,
			LockKind:           persistence.LockKindScope,
			ProducerName:       &pName,
			ClaimScopeData:     []byte(`"parent-scope"`),
			Address:            []byte(`"parent-addr"`),
			Intent:             &intent,
			HolderSupervisorID: "sup-MP",
			HolderNodeID:       parentNode.ID,
			ExpiresAt:          time.Now().Add(10 * time.Minute),
			NodeRunID:          &parentNodeRunID,
			AggregationPolicy:  []byte(`"not-an-aggregation-policy-object"`),
		}, tx); err != nil {
			return err
		}
		for _, sid := range []shared.UUID{sub0, sub1} {
			parent := parentID
			if err := backend.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
				ID:                  sid,
				LockKind:            persistence.LockKindScope,
				ProducerName:        &pName,
				ClaimScopeData:      []byte(`"sub-scope"`),
				Address:             []byte(`"sub-addr"`),
				Intent:              &intent,
				HolderSupervisorID:  "sup-MP",
				HolderNodeID:        parentNode.ID,
				ExpiresAt:           time.Now().Add(10 * time.Minute),
				NodeRunID:           &parentNodeRunID,
				ParentClaimHandleID: &parent,
			}, tx); err != nil {
				return err
			}
			if err := backend.ClaimHandles().BumpExpectedChildrenCount(ctx, parentID, "sup-MP", 1, tx); err != nil {
				return err
			}
		}
		return nil
	}))

	resolveSubclaim(ctx, t, backend, args, sub0, parentID, store, runtime.OutcomeAbandon)

	var sub1Row *persistence.ClaimHandleRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := backend.ClaimHandles().Get(ctx, sub1, tx)
		sub1Row = r
		return err
	}))
	require.NotNil(t, sub1Row)
	require.Equal(t, spec.ClaimHandleStateActive, sub1Row.State,
		"malformed aggregation_policy must be treated as no cancel_siblings: sibling must remain untouched")
	require.Equal(t, 0, countCallsOnID(store.Calls(), sub1.String(), "abandon"),
		"sibling must receive no verb call under malformed-policy fallback")

	resolveSubclaim(ctx, t, backend, args, sub1, parentID, store, runtime.OutcomeCommit)

	var parentRow *persistence.ClaimHandleRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := backend.ClaimHandles().Get(ctx, parentID, tx)
		parentRow = r
		return err
	}))
	require.NotNil(t, parentRow)
	require.Equal(t, spec.ClaimHandleStateAbandoned, parentRow.State,
		"aggregator must fall back to strict semantics on malformed policy: any-abandoned child still forces parent Abandon")
	require.Equal(t, 1, countCallsOnID(store.Calls(), parentID.String(), "abandon"),
		"parent must receive Abandon under the strict-fallback verdict")
	require.Equal(t, 0, countCallsOnID(store.Calls(), parentID.String(), "commit"),
		"parent must NOT Commit despite one child committing, since strict fallback treats any-abandoned as decisive")
}

func TestCancelInFlightSiblings_DifferentSupervisorSkipped(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)
	backend := d.Tables()

	tmpl := insertDeployedTemplate(ctx, t, backend, node.TemplateSpec{
		Name: "cancel-siblings-multi-supervisor", Version: "1",
	})
	ck := "ck-cs-ms"
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
	parentNodeRunID := seedRunForNode(ctx, t, backend, d.Queue(), parentNode.ID, frameID)

	reg := locks.NewRegistry()
	store := storetest.NewFake("cs-ms-store", claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
	})
	reg.Add("cs-ms-store", store)
	args := runtime.RunArgs{
		Persist:       backend,
		ClaimHandles:  backend.ClaimHandles(),
		StoreRegistry: reg,
		Logger:        shared.SilentLogger{},
		SupervisorID:  "sup-MS-A",
	}

	policy := spec.AggregationPolicy{Kind: spec.AggregationKindStrict, CancelSiblings: true}
	policyBytes, mErr := persistence.MarshalAggregationPolicy(policy)
	require.NoError(t, mErr)

	parentID := shared.UUID(uuid.New())
	triggerID := shared.UUID(uuid.New())
	sameSupID := shared.UUID(uuid.New())
	otherSupID := shared.UUID(uuid.New())
	intent := "rw"
	pName := "cs-ms-store"
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := backend.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID: parentID, LockKind: persistence.LockKindScope,
			ProducerName: &pName, ClaimScopeData: []byte(`"parent-scope"`), Address: []byte(`"parent-addr"`),
			Intent:             &intent,
			HolderSupervisorID: "sup-MS-A", HolderNodeID: parentNode.ID,
			ExpiresAt:         time.Now().Add(10 * time.Minute),
			NodeRunID:         &parentNodeRunID,
			AggregationPolicy: policyBytes,
		}, tx); err != nil {
			return err
		}
		for _, entry := range []struct {
			id  shared.UUID
			sup string
		}{
			{triggerID, "sup-MS-A"},
			{sameSupID, "sup-MS-A"},
			{otherSupID, "sup-MS-B"},
		} {
			parent := parentID
			if err := backend.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
				ID: entry.id, LockKind: persistence.LockKindScope,
				ProducerName: &pName, ClaimScopeData: []byte(`"sub-scope"`), Address: []byte(`"sub-addr"`),
				Intent:              &intent,
				HolderSupervisorID:  entry.sup,
				HolderNodeID:        parentNode.ID,
				ExpiresAt:           time.Now().Add(10 * time.Minute),
				NodeRunID:           &parentNodeRunID,
				ParentClaimHandleID: &parent,
			}, tx); err != nil {
				return err
			}
			if err := backend.ClaimHandles().BumpExpectedChildrenCount(ctx, parentID, "sup-MS-A", 1, tx); err != nil {
				return err
			}
		}
		return nil
	}))

	resolveSubclaim(ctx, t, backend, args, triggerID, parentID, store, runtime.OutcomeAbandon)

	var sameSupRow, otherSupRow *persistence.ClaimHandleRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := backend.ClaimHandles().Get(ctx, sameSupID, tx)
		sameSupRow = r
		if err != nil {
			return err
		}
		r2, err := backend.ClaimHandles().Get(ctx, otherSupID, tx)
		otherSupRow = r2
		return err
	}))
	require.NotNil(t, sameSupRow)
	require.Equal(t, spec.ClaimHandleStateAbandoned, sameSupRow.State,
		"sibling held by the same supervisor must be force-cancelled")
	require.Equal(t, 1, countCallsOnID(store.Calls(), sameSupID.String(), "abandon"),
		"sibling held by the same supervisor must receive exactly one Abandon")

	require.NotNil(t, otherSupRow)
	require.Equal(t, spec.ClaimHandleStateActive, otherSupRow.State,
		"sibling held by a different supervisor must be skipped by the cancel_siblings walk")
	require.Equal(t, 0, countCallsOnID(store.Calls(), otherSupID.String(), "abandon"),
		"sibling held by a different supervisor must receive no verb call")
}

func TestCancelDescendantClaims_DifferentSupervisorSkipped(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)
	backend := d.Tables()

	tmpl := insertDeployedTemplate(ctx, t, backend, node.TemplateSpec{
		Name: "descendant-cancel-multi-supervisor", Version: "1",
	})
	ck := "ck-dc-ms"
	var mainScopeID shared.UUID
	var inst persistence.InstanceRow
	var rootNode persistence.NodeRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		i, ms := seedInstanceWithMainScope(ctx, t, backend, tx, tmpl.ID, &ck)
		inst = i
		mainScopeID = ms
		n, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: shared.UUID(uuid.New()), InstanceID: inst.ID, NodeType: "root", Executor: "stub",
		}, tx)
		if err != nil {
			return err
		}
		rootNode = n
		return nil
	}))

	frameID := seedFrame(ctx, t, backend, inst.ID, rootNode.ID, mainScopeID)
	rootNodeRunID := seedRunForNode(ctx, t, backend, d.Queue(), rootNode.ID, frameID)

	reg := locks.NewRegistry()
	store := storetest.NewFake("dc-ms-store", claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
	})
	reg.Add("dc-ms-store", store)
	args := runtime.RunArgs{
		Persist:       backend,
		ClaimHandles:  backend.ClaimHandles(),
		StoreRegistry: reg,
		Logger:        shared.SilentLogger{},
		SupervisorID:  "sup-DC-A",
	}

	rootID := shared.UUID(uuid.New())
	sameSupChild := shared.UUID(uuid.New())
	otherSupChild := shared.UUID(uuid.New())
	intent := "rw"
	pName := "dc-ms-store"
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := backend.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID: rootID, LockKind: persistence.LockKindScope,
			ProducerName: &pName, ClaimScopeData: []byte(`"root-scope"`), Address: []byte(`"root-addr"`),
			Intent:             &intent,
			HolderSupervisorID: "sup-DC-A", HolderNodeID: rootNode.ID,
			ExpiresAt: time.Now().Add(10 * time.Minute),
			NodeRunID: &rootNodeRunID,
		}, tx); err != nil {
			return err
		}
		for _, entry := range []struct {
			id  shared.UUID
			sup string
		}{
			{sameSupChild, "sup-DC-A"},
			{otherSupChild, "sup-DC-B"},
		} {
			parent := rootID
			if err := backend.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
				ID: entry.id, LockKind: persistence.LockKindScope,
				ProducerName: &pName, ClaimScopeData: []byte(`"child-scope"`), Address: []byte(`"child-addr"`),
				Intent:              &intent,
				HolderSupervisorID:  entry.sup,
				HolderNodeID:        rootNode.ID,
				ExpiresAt:           time.Now().Add(10 * time.Minute),
				NodeRunID:           &rootNodeRunID,
				ParentClaimHandleID: &parent,
			}, tx); err != nil {
				return err
			}
		}
		return nil
	}))

	var post func(context.Context)
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		pc, err := runtime.ResolveClaimHandleTerminal(ctx, args, tx, runtime.TerminalDecision{
			ClaimHandleID: rootID,
			SupervisorID:  args.SupervisorID,
			Source:        runtime.ActiveTerminal,
			Outcome:       runtime.OutcomeAbandon,
			Producer:      store,
			Scope:         []byte(`"root-scope"`),
			Address:       []byte(`"root-addr"`),
			Lifetime:      "subgraph",
			ProducerName:  pName,
			LineageHint: runtime.ClaimLineageHint{
				InstanceID:   inst.ID,
				FrameID:      frameID,
				NodeRunID:    rootNodeRunID,
				NodeID:       rootNode.ID,
				ProducerName: pName,
			},
		})
		post = pc
		return err
	}))
	if post != nil {
		post(ctx)
	}

	var rootRow, sameSupRow, otherSupRow *persistence.ClaimHandleRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := backend.ClaimHandles().Get(ctx, rootID, tx)
		rootRow = r
		if err != nil {
			return err
		}
		r2, err := backend.ClaimHandles().Get(ctx, sameSupChild, tx)
		sameSupRow = r2
		if err != nil {
			return err
		}
		r3, err := backend.ClaimHandles().Get(ctx, otherSupChild, tx)
		otherSupRow = r3
		return err
	}))
	require.NotNil(t, rootRow)
	require.Equal(t, spec.ClaimHandleStateAbandoned, rootRow.State, "root must be abandoned")

	require.NotNil(t, sameSupRow)
	require.Equal(t, spec.ClaimHandleStateAbandoned, sameSupRow.State,
		"descendant held by the same supervisor must be force-cancelled by the descendant-cancel walk")
	require.Equal(t, 1, countCallsOnID(store.Calls(), sameSupChild.String(), "abandon"),
		"descendant held by the same supervisor must receive exactly one Abandon")

	require.NotNil(t, otherSupRow)
	require.Equal(t, spec.ClaimHandleStateActive, otherSupRow.State,
		"descendant held by a different supervisor must be skipped by the descendant-cancel walk and can remain active")
	require.Equal(t, 0, countCallsOnID(store.Calls(), otherSupChild.String(), "abandon"),
		"descendant held by a different supervisor must receive no verb call")
}

func TestCancelDescendantClaims_MultiLevelRecursion_SkipsCommittedChild(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)
	backend := d.Tables()

	tmpl := insertDeployedTemplate(ctx, t, backend, node.TemplateSpec{
		Name: "descendant-cancel-multilevel", Version: "1",
	})
	ck := "ck-dc-ml"
	var mainScopeID shared.UUID
	var inst persistence.InstanceRow
	var rootNode persistence.NodeRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		i, ms := seedInstanceWithMainScope(ctx, t, backend, tx, tmpl.ID, &ck)
		inst = i
		mainScopeID = ms
		n, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: shared.UUID(uuid.New()), InstanceID: inst.ID, NodeType: "root", Executor: "stub",
		}, tx)
		if err != nil {
			return err
		}
		rootNode = n
		return nil
	}))

	frameID := seedFrame(ctx, t, backend, inst.ID, rootNode.ID, mainScopeID)
	rootNodeRunID := seedRunForNode(ctx, t, backend, d.Queue(), rootNode.ID, frameID)

	reg := locks.NewRegistry()
	store := storetest.NewFake("dc-ml-store", claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
	})
	reg.Add("dc-ml-store", store)
	args := runtime.RunArgs{
		Persist:       backend,
		ClaimHandles:  backend.ClaimHandles(),
		StoreRegistry: reg,
		Logger:        shared.SilentLogger{},
		SupervisorID:  "sup-DL",
		Clock:         shared.SystemClock{},
	}

	policy := spec.AggregationPolicy{Kind: spec.AggregationKindStrict}
	rootID, subIDs := seedFanOutParentAndSubclaims(
		ctx, t, backend, rootNodeRunID, rootNode.ID, "sup-DL",
		"dc-ml-store", policy, 3,
	)
	activeChild, committedChild, midChild := subIDs[0], subIDs[1], subIDs[2]

	resolveSubclaimWithLifetime(ctx, t, backend, args, committedChild, rootID, store, runtime.OutcomeCommit, spec.ClaimLifetimeDurable)

	grandchild := shared.UUID(uuid.New())
	intent := "rw"
	pName := "dc-ml-store"
	midParent := midChild
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := backend.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID: grandchild, LockKind: persistence.LockKindScope,
			ProducerName: &pName, ClaimScopeData: []byte(`"gc-scope"`), Address: []byte(`"gc-addr"`),
			Intent:              &intent,
			HolderSupervisorID:  "sup-DL",
			HolderNodeID:        rootNode.ID,
			ExpiresAt:           time.Now().Add(10 * time.Minute),
			NodeRunID:           &rootNodeRunID,
			ParentClaimHandleID: &midParent,
		}, tx); err != nil {
			return err
		}
		return backend.ClaimHandles().BumpExpectedChildrenCount(ctx, midChild, "sup-DL", 1, tx)
	}))

	var post func(context.Context)
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		pc, err := runtime.ResolveClaimHandleTerminal(ctx, args, tx, runtime.TerminalDecision{
			ClaimHandleID: rootID,
			SupervisorID:  args.SupervisorID,
			Source:        runtime.ActiveTerminal,
			Outcome:       runtime.OutcomeAbandon,
			Producer:      store,
			Scope:         []byte(`"root-scope"`),
			Address:       []byte(`"root-addr"`),
			Lifetime:      "subgraph",
			ProducerName:  pName,
			LineageHint: runtime.ClaimLineageHint{
				InstanceID:   inst.ID,
				FrameID:      frameID,
				NodeRunID:    rootNodeRunID,
				NodeID:       rootNode.ID,
				ProducerName: pName,
			},
		})
		post = pc
		return err
	}))
	if post != nil {
		post(ctx)
	}

	getRow := func(id shared.UUID) *persistence.ClaimHandleRow {
		var row *persistence.ClaimHandleRow
		require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			r, err := backend.ClaimHandles().Get(ctx, id, tx)
			row = r
			return err
		}))
		return row
	}

	rootRow := getRow(rootID)
	require.NotNil(t, rootRow)
	require.Equal(t, spec.ClaimHandleStateAbandoned, rootRow.State, "root must abandon on its own natural terminal")

	activeRow := getRow(activeChild)
	require.NotNil(t, activeRow)
	require.Equal(t, spec.ClaimHandleStateAbandoned, activeRow.State,
		"active child must be swept by the descendant-cancel walk (claim-tree.md invariant 1)")
	require.Equal(t, 1, countCallsOnID(store.Calls(), activeChild.String(), "abandon"))
	verifyLineageOutcomeRT(ctx, t, backend, activeChild, persistence.LineageOutcomeForceCancelled, "descendant_cancel")

	committedRow := getRow(committedChild)
	require.NotNil(t, committedRow)
	require.Equal(t, spec.ClaimHandleStateCommitted, committedRow.State,
		"committed child must be skipped by the descendant-cancel walk (claim-tree.md invariant 5)")
	require.Equal(t, 0, countCallsOnID(store.Calls(), committedChild.String(), "abandon"),
		"committed child must receive no Abandon from the descendant-cancel walk")

	midRow := getRow(midChild)
	require.NotNil(t, midRow)
	require.Equal(t, spec.ClaimHandleStateAbandoned, midRow.State,
		"intermediate child must be swept by the descendant-cancel walk")
	verifyLineageOutcomeRT(ctx, t, backend, midChild, persistence.LineageOutcomeForceCancelled, "descendant_cancel")

	grandchildRow := getRow(grandchild)
	require.NotNil(t, grandchildRow)
	require.Equal(t, spec.ClaimHandleStateAbandoned, grandchildRow.State,
		"grandchild must be swept by the recursive descendant-cancel walk (claim-tree.md invariants 1 and 3)")
	require.Equal(t, 1, countCallsOnID(store.Calls(), grandchild.String(), "abandon"))
	verifyLineageOutcomeRT(ctx, t, backend, grandchild, persistence.LineageOutcomeForceCancelled, "descendant_cancel")
}

func TestCheckAndFireResolution_ProducerVerbError_RoutesToAbandonAndUnsticksCoHolders(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)
	backend := d.Tables()

	tmpl := insertDeployedTemplate(ctx, t, backend, node.TemplateSpec{
		Name: "auto-term-verb-error", Version: "1",
	})
	ck := "ck-ve"
	var mainScopeID shared.UUID
	var inst persistence.InstanceRow
	var acqNode, coholderNode persistence.NodeRow
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
		c, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: shared.UUID(uuid.New()), InstanceID: inst.ID, NodeType: "coholder", Executor: "stub",
		}, tx)
		if err != nil {
			return err
		}
		coholderNode = c
		return nil
	}))

	reg := locks.NewRegistry()
	store := storetest.NewFake("verb-err-store", claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
	})
	store.ErrorFunc = func(verb string, _ claimproducer.ClaimID) error {
		if verb != "commit" {
			return nil
		}
		return &peer.ProducerCallError{
			ProducerName: "verb-err-store",
			Method:       "Commit",
			ErrorClass:   "conflict",
			Underlying:   errors.New("boom"),
		}
	}
	reg.Add("verb-err-store", store)

	frameID := seedFrame(ctx, t, backend, inst.ID, acqNode.ID, mainScopeID)
	acqRunID := seedRunForNode(ctx, t, backend, d.Queue(), acqNode.ID, frameID)
	coholderRunID := seedRunForNode(ctx, t, backend, d.Queue(), coholderNode.ID, frameID)

	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		claimed, err := d.Queue().ClaimDispatchRow(ctx, tx, coholderRunID, "sup-VE")
		if err != nil {
			return err
		}
		require.True(t, claimed, "coholder run must be claimable")
		promoted, err := d.Queue().PromoteClaimedToRunning(ctx, tx, coholderRunID, "sup-VE")
		if err != nil {
			return err
		}
		require.True(t, promoted, "coholder run must promote to running")
		return backend.Nodes().UpdateState(ctx, coholderRunID,
			cascade.NodeStateHeld, cascade.ReasonHandlerHeld, nil, tx)
	}))

	storeName := "verb-err-store"
	intent := "rw"
	claimID := shared.UUID(uuid.New())
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := backend.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID: claimID, LockKind: persistence.LockKindScope,
			ProducerName: &storeName, ClaimScopeData: []byte(`"r"`), Address: []byte(`"r"`),
			Intent:             &intent,
			HolderSupervisorID: "sup-VE", HolderNodeID: acqNode.ID,
			ExpiresAt: time.Now().Add(10 * time.Minute),
			NodeRunID: &acqRunID, FrameID: &frameID,
		}, tx); err != nil {
			return err
		}
		if err := backend.ClaimHolders().Insert(ctx, persistence.ClaimHolderInsertInput{
			ID: shared.UUID(uuid.New()), ClaimHandleID: claimID, HolderNodeRunID: acqRunID,
		}, tx); err != nil {
			return err
		}
		return backend.ClaimHolders().Insert(ctx, persistence.ClaimHolderInsertInput{
			ID: shared.UUID(uuid.New()), ClaimHandleID: claimID, HolderNodeRunID: coholderRunID,
		}, tx)
	}))
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := backend.ClaimHolders().CompleteByClaimHandleAndRun(
			ctx, claimID, acqRunID, persistence.ClaimHolderStateCompleted, tx,
		); err != nil {
			return err
		}
		return backend.ClaimHolders().CompleteByClaimHandleAndRun(
			ctx, claimID, coholderRunID, persistence.ClaimHolderStateCompleted, tx,
		)
	}))

	args := runtime.RunArgs{
		Persist:       backend,
		ClaimHandles:  backend.ClaimHandles(),
		StoreRegistry: reg,
		Logger:        shared.SilentLogger{},
		SupervisorID:  "sup-VE",
		Clock:         shared.SystemClock{},
	}
	var post func(context.Context)
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		pc, err := runtime.CheckAndFireResolution(ctx, args, tx, claimID)
		post = pc
		return err
	}))
	if post != nil {
		post(ctx)
	}

	var row *persistence.ClaimHandleRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := backend.ClaimHandles().Get(ctx, claimID, tx)
		row = r
		return err
	}))
	require.NotNil(t, row, "claim_handle row must be preserved past a producer verb error (Promote-not-delete)")
	require.Equal(t, spec.ClaimHandleStateAbandoned, row.State,
		"a classified producer verb error must promote the handle straight to abandoned, with no retried verb call")
	require.Equal(t, 1, countCallsOnID(store.Calls(), claimID.String(), "commit"),
		"exactly one Commit attempt must be made; the engine must not retry after a classified error")
	require.Equal(t, 0, countCallsOnID(store.Calls(), claimID.String(), "abandon"),
		"the verb-error fallback promotes state directly; it must not additionally fire an Abandon verb call")

	var page persistence.EventListResult
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		p, err := backend.Events().List(ctx, persistence.EventListFilter{InstanceID: &inst.ID},
			persistence.ListPagination{Limit: 50}, tx)
		page = p
		return err
	}))
	sawErrorSignal := false
	for _, ev := range page.Events {
		if ev.Payload["error_class"] != "conflict" {
			continue
		}
		errPayload, ok := ev.Payload["error_payload"].(map[string]any)
		if !ok {
			continue
		}
		if errPayload["source"] == "claim_terminal_verb" {
			sawErrorSignal = true
		}
	}
	require.True(t, sawErrorSignal,
		"a claim_terminal_verb error-policy signal (error_class=conflict) must be emitted on verb error")

	var coholderRun *persistence.NodeRunForGate
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := backend.Nodes().GetRunForGate(ctx, tx, coholderRunID)
		coholderRun = r
		return err
	}))
	require.NotNil(t, coholderRun)
	require.Equal(t, cascade.NodeStateFailed, coholderRun.State,
		"a co-holder held on the now-resolved claim must be unstuck by the verb-error fallback, "+
			"not left in held forever: its portfolio is fully resolved (poisoned) once this claim promotes to abandoned")
}

func verifyLineageOutcomeRT(
	ctx context.Context, t *testing.T, backend persistence.Tables,
	claimID shared.UUID, expectedOutcome, expectedCause string,
) {
	t.Helper()
	rows, err := backend.Lineage().GetByClaimHandleID(ctx, claimID)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(rows), 1, "claim_terminal row must exist for %s", claimID)
	row := rows[len(rows)-1]
	require.Equal(t, persistence.LineageRecordKindClaimTerminal, row.RecordKind,
		"row for %s must be claim_terminal", claimID)
	require.Equal(t, expectedOutcome, row.Outcome,
		"outcome for %s: got %q want %q", claimID, row.Outcome, expectedOutcome)
	if expectedCause == "" {
		return
	}
	var rec runtime.ClaimTerminalRecord
	require.NoError(t, json.Unmarshal(row.Record, &rec))
	require.Equal(t, expectedCause, rec.Cause,
		"cause for %s: got %q want %q", claimID, rec.Cause, expectedCause)
}

// @concept: claim-handle
func TestCheckAndFireResolution_HeldCoHolderSettlement_EmitsEmptyAttributesDelta(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)
	backend := d.Tables()

	tmpl := insertDeployedTemplate(ctx, t, backend, node.TemplateSpec{
		Name: "auto-term-passive-holder-delta", Version: "1",
	})
	ck := "ck-phd"
	var mainScopeID shared.UUID
	var inst persistence.InstanceRow
	var acqNode, coholderNode persistence.NodeRow
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
		c, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: shared.UUID(uuid.New()), InstanceID: inst.ID, NodeType: "coholder", Executor: "stub",
		}, tx)
		if err != nil {
			return err
		}
		coholderNode = c
		return nil
	}))

	reg := locks.NewRegistry()
	store := storetest.NewFake("passive-holder-store", claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
	})
	reg.Add("passive-holder-store", store)

	frameID := seedFrame(ctx, t, backend, inst.ID, acqNode.ID, mainScopeID)
	acqRunID := seedRunForNode(ctx, t, backend, d.Queue(), acqNode.ID, frameID)
	coholderRunID := seedRunForNode(ctx, t, backend, d.Queue(), coholderNode.ID, frameID)

	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		claimed, err := d.Queue().ClaimDispatchRow(ctx, tx, coholderRunID, "sup-PHD")
		if err != nil {
			return err
		}
		require.True(t, claimed, "coholder run must be claimable")
		promoted, err := d.Queue().PromoteClaimedToRunning(ctx, tx, coholderRunID, "sup-PHD")
		if err != nil {
			return err
		}
		require.True(t, promoted, "coholder run must promote to running")
		return backend.Nodes().UpdateState(ctx, coholderRunID,
			cascade.NodeStateHeld, cascade.ReasonHandlerHeld, nil, tx)
	}))

	storeName := "passive-holder-store"
	intent := "rw"
	claimID := shared.UUID(uuid.New())
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := backend.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID: claimID, LockKind: persistence.LockKindScope,
			ProducerName: &storeName, ClaimScopeData: []byte(`"r"`), Address: []byte(`"r"`),
			Intent:             &intent,
			HolderSupervisorID: "sup-PHD", HolderNodeID: acqNode.ID,
			ExpiresAt: time.Now().Add(10 * time.Minute),
			NodeRunID: &acqRunID, FrameID: &frameID,
		}, tx); err != nil {
			return err
		}
		if err := backend.ClaimHolders().Insert(ctx, persistence.ClaimHolderInsertInput{
			ID: shared.UUID(uuid.New()), ClaimHandleID: claimID, HolderNodeRunID: acqRunID,
		}, tx); err != nil {
			return err
		}
		return backend.ClaimHolders().Insert(ctx, persistence.ClaimHolderInsertInput{
			ID: shared.UUID(uuid.New()), ClaimHandleID: claimID, HolderNodeRunID: coholderRunID,
		}, tx)
	}))
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := backend.ClaimHolders().CompleteByClaimHandleAndRun(
			ctx, claimID, acqRunID, persistence.ClaimHolderStateCompleted, tx,
		); err != nil {
			return err
		}
		return backend.ClaimHolders().CompleteByClaimHandleAndRun(
			ctx, claimID, coholderRunID, persistence.ClaimHolderStateCompleted, tx,
		)
	}))

	args := runtime.RunArgs{
		Persist:       backend,
		ClaimHandles:  backend.ClaimHandles(),
		StoreRegistry: reg,
		Logger:        shared.SilentLogger{},
		SupervisorID:  "sup-PHD",
		Clock:         shared.SystemClock{},
	}
	var post func(context.Context)
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		pc, err := runtime.CheckAndFireResolution(ctx, args, tx, claimID)
		post = pc
		return err
	}))
	if post != nil {
		post(ctx)
	}

	var coholderRun *persistence.NodeRunForGate
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := backend.Nodes().GetRunForGate(ctx, tx, coholderRunID)
		coholderRun = r
		return err
	}))
	require.NotNil(t, coholderRun)
	require.Equal(t, cascade.NodeStateFresh, coholderRun.State,
		"a held co-holder whose portfolio fully resolves (committed, not poisoned) must settle to fresh")

	var page persistence.EventListResult
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		p, err := backend.Events().List(ctx, persistence.EventListFilter{NodeID: &coholderNode.ID},
			persistence.ListPagination{Limit: 50}, tx)
		page = p
		return err
	}))
	var sawSuccess bool
	for _, ev := range page.Events {
		if ev.KindRaw != "terminal/success" {
			continue
		}
		sawSuccess = true
		delta, ok := ev.Payload["attributes_delta"].(map[string]any)
		require.True(t, ok, "terminal/success payload must carry an attributes_delta map, got %#v", ev.Payload["attributes_delta"])
		require.Empty(t, delta,
			"a passive held co-holder's own auto-terminal settlement must carry an empty attributes_delta "+
				"(it did not itself produce new attributes at this settlement moment); got %#v", delta)
	}
	require.True(t, sawSuccess, "co-holder %s must have a terminal/success event recorded on its own settlement", coholderNode.ID)
}
