// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Substantive coverage for the §4.10 invariant 13 auto-terminal mechanism in
// isolation. Drives CheckAndFireResolution against a real Postgres + a
// stub-filesystem store and asserts the aggregate-outcome semantics.

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

// seedRunForNode enqueues a fresh `rimsky_node_runs` row for the given
// node and returns the run id. Post-stage-5 of the run-row lifecycle
// cutover, claim-holders rows key on `holder_run_id` (a
// `rimsky_node_runs.id`), so the auto-terminal tests in this file need a
// real run id per holder.
func seedRunForNode(
	ctx context.Context, t *testing.T, sb persistence.Tables, q persistence.Queue,
	nodeID, frameID shared.UUID,
) shared.UUID {
	t.Helper()
	var out shared.UUID
	// Resolve the node's instance + main RunScope so the dispatch row
	// satisfies the run_scope_id NOT NULL constraint.
	var mainScopeID shared.UUID
	require.NoError(t, sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		nd, err := sb.Nodes().Get(ctx, nodeID, tx)
		if err != nil {
			return err
		}
		if nd == nil {
			t.Fatalf("seedRunForNode: node %s missing", nodeID)
		}
		inst, err := sb.Instances().Get(ctx, nd.InstanceID, tx)
		if err != nil {
			return err
		}
		if inst == nil {
			t.Fatalf("seedRunForNode: instance %s missing", nd.InstanceID)
		}
		mainScopeID = inst.MainRunScopeID
		return nil
	}))
	require.NoError(t, sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := q.EnqueueInTx(ctx, persistence.DispatchRequest{
			NodeID:         nodeID,
			ExecutorName:   "stub",
			RequiredStores: []string{},
			EnqueuedAt:     time.Now().Add(-1 * time.Second),
			FrameID:        frameID,
			RunScopeID:     mainScopeID,
		}, tx); err != nil {
			return err
		}
		cands, err := q.SelectCandidates(ctx, tx, persistence.SelectCandidatesRequest{
			AcceptedExecutors: []string{"stub"},
			AcceptedStores:    []string{},
			Limit:             16,
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

// seedFrame creates a 'running' frame for the instance and returns its
// frame_id. Pairs with seedRunForNode to give holder INSERTs a valid
// FK chain.
func seedFrame(ctx context.Context, t *testing.T, sb persistence.Tables, instanceID, sourceNodeID shared.UUID) shared.UUID {
	t.Helper()
	var frameID shared.UUID
	require.NoError(t, sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		fid, err := sb.Frames().EnqueueSerialFrame(ctx, instanceID, sourceNodeID, 600000, tx)
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

// seedInstanceWithMainScope creates an instance with its required main
// RunScope row in a single tx. Per the RunScope-first migration both
// rimsky_instances.main_run_scope_id and rimsky_run_scopes.instance_id
// are NOT NULL and FK each other (DEFERRABLE INITIALLY DEFERRED), so
// the canonical seed path must land both rows in one tx.
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
		Params:         map[string]any{},
		MainRunScopeID: mainScopeID,
	}, tx)
	if err != nil {
		t.Fatalf("seedInstanceWithMainScope: Instances.Create: %v", err)
	}
	return row, mainScopeID
}

// insertDeployedTemplate inserts a template row in 'deployed' state with a
// deterministic content hash derived from name+version.
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

// TestCheckAndFireResolution_AllCompletedFiresCommit seeds a held
// subgraph with two completed claim_holders rows and confirms
// CheckAndFireResolution invokes Commit on the store and
// deletes the lock-holder row. Cascade FK removes the claim-holders
// rows.
func TestCheckAndFireResolution_AllCompletedFiresCommit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)
	backend := d.Tables()

	tmpl := insertDeployedTemplate(ctx, t, backend, node.TemplateSpec{
		Name: "auto-term-commit", Version: "1",
	})
	ck := "ck"
	var inst persistence.InstanceRow
	var acqNode, inhNode persistence.NodeRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		i, _ := seedInstanceWithMainScope(ctx, t, backend, tx, tmpl.ID, &ck)
		inst = i
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

	// Seed a frame + run row per holder so the post-stage-5 FK chain on
	// rimsky_claim_holders.holder_run_id resolves.
	frameID := seedFrame(ctx, t, backend, inst.ID, acqNode.ID)
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
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return runtime.CheckAndFireResolution(ctx, args, tx, lockHolderID)
	}))

	var row *persistence.ClaimHandleRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := backend.ClaimHandles().Get(ctx, lockHolderID, tx)
		row = r
		return err
	}))
	// Post-Stage-3 of the claim-handle state-column refactor: the row
	// is promoted (not deleted) at auto-terminal; assert state=committed
	// + holder_supervisor_id nulled + resolved_at set instead of nil row.
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

// TestCheckAndFireResolution_DurableLifetimeIdempotency drives the
// Fix 6 idempotency guard: after a `lifetime: durable` row gets
// promoted to state='committed' (held-durable Promote contract per
// @blessed-invariant 22), re-entering CheckAndFireResolution MUST NOT
// re-fire Commit. The full durable-claim E2E lives in
// `test/scenarios/asset/durable_lifetime_e2e_test.go` (the runtime
// fixtures here exercise the re-entry guard in isolation since the
// idempotency property is a runtime concern).
func TestCheckAndFireResolution_DurableLifetimeIdempotency(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)
	backend := d.Tables()

	tmpl := insertDeployedTemplate(ctx, t, backend, node.TemplateSpec{
		Name: "auto-term-durable-idempotency", Version: "1",
	})
	ck := "ck-durable-idem"
	var inst persistence.InstanceRow
	var acqNode persistence.NodeRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		i, _ := seedInstanceWithMainScope(ctx, t, backend, tx, tmpl.ID, &ck)
		inst = i
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

	frameID := seedFrame(ctx, t, backend, inst.ID, acqNode.ID)
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
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return runtime.CheckAndFireResolution(ctx, args, tx, claimHandleID)
	}))

	// First entry: row must survive + state=committed + exactly one Commit.
	// Durable-Commit flips state to 'committed' (Promote-not-delete) and
	// preserves the row past auto-terminal (held-durable Promote contract
	// per @blessed-invariant 22).
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

	// Fix 6 guard: re-entry on the durable row is a no-op.
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return runtime.CheckAndFireResolution(ctx, args, tx, claimHandleID)
	}))
	postReEntryCommitCount := 0
	for _, c := range stubStore.Calls() {
		if c.Verb == "commit" {
			postReEntryCommitCount++
		}
	}
	require.Equal(t, 1, postReEntryCommitCount,
		"re-entering CheckAndFireResolution on a held-durable row must NOT re-fire Commit")
}

// --- cycle 4 fan-out parent aggregation scenarios -----------------------
//
// Drive `ResolveClaimHandleTerminal` on multiple sub-claims and assert
// the parent's aggregate Commit/Abandon decision is computed per the
// snapshotted aggregation policy — not just the seedOutcome of the
// last-resolved child (cycle 4 issue C). Each scenario seeds a parent
// claim_handle row, N sub-claim rows (parent_claim_handle_id → parent),
// initial counter state (`expected_children_count = N`, policy bytes,
// matching supervisor), then resolves the children one by one and
// asserts the producer verbs fired on the parent claim_id.

// seedFanOutParentAndSubclaims creates the parent claim_handle + N
// sub-claim claim_handles rows (parent_claim_handle_id → parent) and
// pre-populates the parent's expected_children_count + aggregation_policy.
// Returns the parent_id + slice of sub-claim ids.
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

// resolveSubclaim drives ResolveClaimHandleTerminal on one sub-claim
// row, recursing into the parent via the standard path.
func resolveSubclaim(
	ctx context.Context, t *testing.T, backend persistence.Tables,
	args runtime.RunArgs, subID, parentID shared.UUID,
	producer locks.ClaimProducer, outcome runtime.AggregateOutcome,
) {
	t.Helper()
	resolveSubclaimWithLifetime(ctx, t, backend, args, subID, parentID, producer, outcome, "subgraph")
}

// resolveSubclaimWithLifetime is the lifetime-aware variant of
// resolveSubclaim used by the all-durable-Commit aggregation tests. A
// durable-Commit child gets promoted to state='committed' (the
// held-durable Promote contract preserves the row past auto-terminal);
// the parent's counter bump + recursive walker MUST still fire so
// best_effort / first policies see the child's success.
func resolveSubclaimWithLifetime(
	ctx context.Context, t *testing.T, backend persistence.Tables,
	args runtime.RunArgs, subID, parentID shared.UUID,
	producer locks.ClaimProducer, outcome runtime.AggregateOutcome,
	lifetime spec.ClaimLifetime,
) {
	t.Helper()
	pname := producer.Name()
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return runtime.ResolveClaimHandleTerminal(ctx, args, tx, runtime.TerminalDecision{
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
	}))
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

// TestResolveParentClaimChain_BestEffort_PartialAbandonStillCommits
// pins issue-C: under best_effort, parent Commits even when one child
// Abandoned (committed > 0).
func TestResolveParentClaimChain_BestEffort_PartialAbandonStillCommits(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)
	backend := d.Tables()

	tmpl := insertDeployedTemplate(ctx, t, backend, node.TemplateSpec{
		Name: "best-effort-fanout", Version: "1",
	})
	ck := "ck-be"
	var inst persistence.InstanceRow
	var parentNode persistence.NodeRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		i, _ := seedInstanceWithMainScope(ctx, t, backend, tx, tmpl.ID, &ck)
		inst = i
		p, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: shared.UUID(uuid.New()), InstanceID: inst.ID, NodeType: "parent", Executor: "stub",
		}, tx)
		if err != nil {
			return err
		}
		parentNode = p
		return nil
	}))

	frameID := seedFrame(ctx, t, backend, inst.ID, parentNode.ID)
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
	// Resolve: sub[0] Abandon, sub[1] Commit → parent Commits under best_effort.
	resolveSubclaim(ctx, t, backend, args, subIDs[0], parentID, store, runtime.AggregateAbandon)
	resolveSubclaim(ctx, t, backend, args, subIDs[1], parentID, store, runtime.AggregateCommit)

	require.Equal(t, 1, countCallsOnID(store.Calls(), parentID.String(), "commit"),
		"best_effort with 1 commit + 1 abandon must Commit the parent")
	require.Equal(t, 0, countCallsOnID(store.Calls(), parentID.String(), "abandon"),
		"best_effort must NOT Abandon the parent when at least one child committed")
}

// TestResolveParentClaimChain_Threshold_AbandonExceedsMax pins
// issue-C: threshold(max_failures=N) Abandons when abandoned > N,
// Commits otherwise.
func TestResolveParentClaimChain_Threshold_AbandonWhenBelowMax(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)
	backend := d.Tables()

	tmpl := insertDeployedTemplate(ctx, t, backend, node.TemplateSpec{
		Name: "threshold-fanout", Version: "1",
	})
	ck := "ck-th"
	var inst persistence.InstanceRow
	var parentNode persistence.NodeRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		i, _ := seedInstanceWithMainScope(ctx, t, backend, tx, tmpl.ID, &ck)
		inst = i
		p, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: shared.UUID(uuid.New()), InstanceID: inst.ID, NodeType: "parent", Executor: "stub",
		}, tx)
		if err != nil {
			return err
		}
		parentNode = p
		return nil
	}))

	frameID := seedFrame(ctx, t, backend, inst.ID, parentNode.ID)
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
	// Resolve: 2 Commits + 1 Abandon → abandoned=1 ≤ MaxFailures=2 → parent Commits.
	resolveSubclaim(ctx, t, backend, args, subIDs[0], parentID, store, runtime.AggregateCommit)
	resolveSubclaim(ctx, t, backend, args, subIDs[1], parentID, store, runtime.AggregateCommit)
	resolveSubclaim(ctx, t, backend, args, subIDs[2], parentID, store, runtime.AggregateAbandon)

	require.Equal(t, 1, countCallsOnID(store.Calls(), parentID.String(), "commit"),
		"threshold(2) with abandoned=1 must Commit the parent")
	require.Equal(t, 0, countCallsOnID(store.Calls(), parentID.String(), "abandon"),
		"threshold(2) with abandoned=1 must NOT Abandon the parent")
}

// TestResolveParentClaimChain_Strict_AbandonsOnAnyFail
// pins issue-C: under strict (default), any abandoned child → parent
// Abandons regardless of order.
func TestResolveParentClaimChain_Strict_AbandonsOnAnyFail(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)
	backend := d.Tables()

	tmpl := insertDeployedTemplate(ctx, t, backend, node.TemplateSpec{
		Name: "strict-fanout", Version: "1",
	})
	ck := "ck-st"
	var inst persistence.InstanceRow
	var parentNode persistence.NodeRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		i, _ := seedInstanceWithMainScope(ctx, t, backend, tx, tmpl.ID, &ck)
		inst = i
		p, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: shared.UUID(uuid.New()), InstanceID: inst.ID, NodeType: "parent", Executor: "stub",
		}, tx)
		if err != nil {
			return err
		}
		parentNode = p
		return nil
	}))

	frameID := seedFrame(ctx, t, backend, inst.ID, parentNode.ID)
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
	// Resolve: 1 Commit + 1 Abandon → strict → parent Abandons.
	resolveSubclaim(ctx, t, backend, args, subIDs[0], parentID, store, runtime.AggregateCommit)
	resolveSubclaim(ctx, t, backend, args, subIDs[1], parentID, store, runtime.AggregateAbandon)

	require.Equal(t, 1, countCallsOnID(store.Calls(), parentID.String(), "abandon"),
		"strict with abandoned=1 must Abandon the parent")
	require.Equal(t, 0, countCallsOnID(store.Calls(), parentID.String(), "commit"),
		"strict with abandoned=1 must NOT Commit the parent")
}

// TestResolveParentClaimChain_ParentHeldWithActiveCoHolders_Defers
// pins issue-D: when the parent is itself a held claim with active
// co-holders, parent resolution defers until the last holder transitions.
func TestResolveParentClaimChain_ParentHeldWithActiveCoHolders_Defers(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)
	backend := d.Tables()

	tmpl := insertDeployedTemplate(ctx, t, backend, node.TemplateSpec{
		Name: "held-parent-fanout", Version: "1",
	})
	ck := "ck-hp"
	var inst persistence.InstanceRow
	var parentNode, coNode persistence.NodeRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		i, _ := seedInstanceWithMainScope(ctx, t, backend, tx, tmpl.ID, &ck)
		inst = i
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

	frameID := seedFrame(ctx, t, backend, inst.ID, parentNode.ID)
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

	// Seed a parent claim handle with 2 sub-claims AND 1 active
	// co-holder row on the parent.
	policy := spec.AggregationPolicy{Kind: spec.AggregationKindStrict}
	parentID, subIDs := seedFanOutParentAndSubclaims(
		ctx, t, backend, parentRunID, parentNode.ID, "sup-HP",
		"hp-store", policy, 2,
	)
	// Add a co-holder row on the parent (active state).
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return backend.ClaimHolders().Insert(ctx, persistence.ClaimHolderInsertInput{
			ID: shared.UUID(uuid.New()), ClaimHandleID: parentID, HolderRunID: coRunID,
		}, tx)
	}))

	// Resolve both sub-claims (one commit, one commit so strict aggregate
	// would otherwise fire Commit). Parent MUST NOT resolve yet because
	// the co-holder row is still active.
	resolveSubclaim(ctx, t, backend, args, subIDs[0], parentID, store, runtime.AggregateCommit)
	resolveSubclaim(ctx, t, backend, args, subIDs[1], parentID, store, runtime.AggregateCommit)

	require.Equal(t, 0, countCallsOnID(store.Calls(), parentID.String(), "commit"),
		"parent must NOT Commit while a co-holder is still active (issue D)")
	require.Equal(t, 0, countCallsOnID(store.Calls(), parentID.String(), "abandon"),
		"parent must NOT Abandon either while a co-holder is still active")

	// Parent row must still be present.
	var parentRow *persistence.ClaimHandleRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := backend.ClaimHandles().Get(ctx, parentID, tx)
		parentRow = r
		return err
	}))
	require.NotNil(t, parentRow,
		"parent claim_handle must survive while co-holder is still active")

	// Now complete the co-holder. Drive CheckAndFireResolution on the
	// parent (the normal path that fires when the last holder
	// transitions). This time aggregation should fire Commit on the
	// parent.
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return backend.ClaimHolders().CompleteByClaimHandleAndRun(
			ctx, parentID, coRunID, persistence.ClaimHolderStateCompleted, tx,
		)
	}))
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return runtime.CheckAndFireResolution(ctx, args, tx, parentID)
	}))

	require.Equal(t, 1, countCallsOnID(store.Calls(), parentID.String(), "commit"),
		"parent must Commit once the last co-holder completes (issue D)")
}

// TestCheckAndFireResolution_ChildrenIncomplete_DefersUntilAllResolve
// exercises the cycle-6 children-quorum defense-in-depth guard at
// `runtime/auto_terminal.go::CheckAndFireResolution` lines 190-194.
// Drives `CheckAndFireResolution` directly on a fan-out parent while
// `ExpectedChildrenCount > CommittedChildrenCount + AbandonedChildrenCount`
// and asserts the guard defers (no Commit/Abandon, parent row survives)
// until every child resolves. Without the guard, a caller that fires
// `CheckAndFireResolution` mid-flight against a fan-out parent would
// see incomplete counters and compute the wrong verdict (e.g.
// `best_effort` would read `committed_children_count == 0` →
// Abandon despite pending Commits). The guard is currently redundant
// under the normal call graph (the run-tree `Aggregate` orders parent
// terminal strictly after children), so this test pins the
// defense-in-depth posture explicitly.
func TestCheckAndFireResolution_ChildrenIncomplete_DefersUntilAllResolve(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)
	backend := d.Tables()

	tmpl := insertDeployedTemplate(ctx, t, backend, node.TemplateSpec{
		Name: "children-quorum-defers", Version: "1",
	})
	ck := "ck-cq"
	var inst persistence.InstanceRow
	var parentNode persistence.NodeRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		i, _ := seedInstanceWithMainScope(ctx, t, backend, tx, tmpl.ID, &ck)
		inst = i
		p, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: shared.UUID(uuid.New()), InstanceID: inst.ID, NodeType: "parent", Executor: "stub",
		}, tx)
		if err != nil {
			return err
		}
		parentNode = p
		return nil
	}))

	frameID := seedFrame(ctx, t, backend, inst.ID, parentNode.ID)
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

	// Seed a fan-out parent with 2 sub-claims. Strict policy so the
	// final verdict on all-commits is Commit.
	policy := spec.AggregationPolicy{Kind: spec.AggregationKindStrict}
	parentID, subIDs := seedFanOutParentAndSubclaims(
		ctx, t, backend, parentRunID, parentNode.ID, "sup-CQ",
		"cq-store", policy, 2,
	)

	// Resolve ONE sub-claim (commit). committed_children_count=1,
	// expected_children_count=2 — quorum NOT met. The standard
	// `resolveParentClaimChain` walker invoked by this resolution will
	// see sub[1] still present (non-durable) and return nil without
	// touching the parent, so the parent row stays intact for the
	// direct `CheckAndFireResolution` call below.
	resolveSubclaim(ctx, t, backend, args, subIDs[0], parentID, store, runtime.AggregateCommit)

	// Insert the parent's own holder row and immediately complete it so
	// the `CheckAndFireResolution` holder-loop sees no active holders
	// (otherwise the function would defer at the holder check, not the
	// cycle-6 children-quorum guard we want to exercise).
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

	// Drive `CheckAndFireResolution` directly on the parent. The
	// cycle-6 children-quorum guard MUST defer because
	// committed(1) + abandoned(0) < expected(2).
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return runtime.CheckAndFireResolution(ctx, args, tx, parentID)
	}))

	require.Equal(t, 0, countCallsOnID(store.Calls(), parentID.String(), "commit"),
		"cycle-6 guard must defer parent Commit while children quorum is not met")
	require.Equal(t, 0, countCallsOnID(store.Calls(), parentID.String(), "abandon"),
		"cycle-6 guard must defer parent Abandon while children quorum is not met")

	// Parent row must still be present after the deferred call.
	var parentRow *persistence.ClaimHandleRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := backend.ClaimHandles().Get(ctx, parentID, tx)
		parentRow = r
		return err
	}))
	require.NotNil(t, parentRow,
		"parent claim_handle must survive the deferred cycle-6 quorum check")

	// Resolve the second sub-claim. This bumps committed_children_count
	// to 2 inside `ResolveClaimHandleTerminal` and then recurses into
	// `resolveParentClaimChain` — which now sees no remaining children
	// + no active holders and fires Commit on the parent via the same
	// `aggregateParentOutcome` aggregator the cycle-6 guard would have
	// used. The two paths converge on the same Commit verdict.
	resolveSubclaim(ctx, t, backend, args, subIDs[1], parentID, store, runtime.AggregateCommit)

	require.Equal(t, 1, countCallsOnID(store.Calls(), parentID.String(), "commit"),
		"parent must Commit via `resolveParentClaimChain` once the last child resolves")
	require.Equal(t, 0, countCallsOnID(store.Calls(), parentID.String(), "abandon"),
		"parent must NOT Abandon under strict aggregation when both children committed")
}

// TestCheckAndFireResolution_AnyFailedFiresGiveUp seeds a held subgraph
// with one completed and one failed claim_holders row and confirms the
// aggregate-outcome path picks Abandon and deletes the lock-holder.
func TestCheckAndFireResolution_AnyFailedFiresGiveUp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)
	backend := d.Tables()

	tmpl := insertDeployedTemplate(ctx, t, backend, node.TemplateSpec{
		Name: "auto-term-give-up", Version: "1",
	})
	ck := "ck"
	var inst persistence.InstanceRow
	var acqNode, inhNode persistence.NodeRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		i, _ := seedInstanceWithMainScope(ctx, t, backend, tx, tmpl.ID, &ck)
		inst = i
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

	frameID := seedFrame(ctx, t, backend, inst.ID, acqNode.ID)
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
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return runtime.CheckAndFireResolution(ctx, args, tx, lockHolderID)
	}))

	var row *persistence.ClaimHandleRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := backend.ClaimHandles().Get(ctx, lockHolderID, tx)
		row = r
		return err
	}))
	// Post-Stage-3: the row is promoted (not deleted) at auto-terminal;
	// assert state=abandoned + holder_supervisor_id nulled + resolved_at
	// set instead of nil row.
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

// TestResolveParentClaimChain_BestEffort_AllDurableCommits pins the
// cycle-5 fix to the durable-Commit early-return regression: under
// best_effort, all-durable-Commit children MUST count toward the
// parent's `committed_children_count` so the aggregate verdict
// resolves to Commit (committed > 0 → Commit). Before the fix, the
// `Lifetime == "durable" && AggregateCommit` branch in
// `ResolveClaimHandleTerminal` returned BEFORE bumping the parent
// counter and recursing → counters stayed at 0 → best_effort's
// `committed > 0 ? Commit : Abandon` rule flipped to Abandon →
// the parent never fired Commit.
func TestResolveParentClaimChain_BestEffort_AllDurableCommits(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)
	backend := d.Tables()

	tmpl := insertDeployedTemplate(ctx, t, backend, node.TemplateSpec{
		Name: "best-effort-all-durable", Version: "1",
	})
	ck := "ck-be-dur"
	var inst persistence.InstanceRow
	var parentNode persistence.NodeRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		i, _ := seedInstanceWithMainScope(ctx, t, backend, tx, tmpl.ID, &ck)
		inst = i
		p, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: shared.UUID(uuid.New()), InstanceID: inst.ID, NodeType: "parent", Executor: "stub",
		}, tx)
		if err != nil {
			return err
		}
		parentNode = p
		return nil
	}))

	frameID := seedFrame(ctx, t, backend, inst.ID, parentNode.ID)
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
	// Resolve both sub-claims as durable-Commit. The durable branch
	// promotes the child rows to state='committed' (they survive) but
	// MUST still bump the parent's committed_children_count + recurse.
	// Under best_effort, committed > 0 → parent Commits.
	resolveSubclaimWithLifetime(ctx, t, backend, args, subIDs[0], parentID, store, runtime.AggregateCommit, spec.ClaimLifetimeDurable)
	resolveSubclaimWithLifetime(ctx, t, backend, args, subIDs[1], parentID, store, runtime.AggregateCommit, spec.ClaimLifetimeDurable)

	require.Equal(t, 1, countCallsOnID(store.Calls(), parentID.String(), "commit"),
		"best_effort with all-durable-Commit children must Commit the parent (counters must bump despite durable-promotion early return)")
	require.Equal(t, 0, countCallsOnID(store.Calls(), parentID.String(), "abandon"),
		"best_effort with all-durable-Commit children must NOT Abandon the parent")

	// Sanity: the children must have been promoted to committed (not
	// deleted). Every Commit (durable or subgraph) flips state to
	// 'committed' (Promote-not-delete). The retention sweep reaps
	// subgraph rows at cutoff; durable rows are reaped only via Release.
	//
	// Note: the row's Lifetime column reflects what was seeded
	// (subgraph here, since `seedFanOutParentAndSubclaims` doesn't
	// override Lifetime). `TerminalDecision.Lifetime=durable` (the
	// in-flight Promote signal) is decoupled from the row's persisted
	// lifetime; the state-promotion path is uniform.
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

// TestResolveParentClaimChain_StrictCancelSiblings_AbandonForcesOtherChildren
// pins the cycle-8 cancel_siblings implementation: under
// `strict.cancel_siblings: true`, the first child Abandon must force-
// Abandon every other in-flight sibling via the recursive walker in
// `code:runtime/terminal_decision.go::cancelInFlightSiblings`. The
// producer must see one Abandon per sibling (in addition to the parent's
// own Abandon) and every sibling row must be deleted.
func TestResolveParentClaimChain_StrictCancelSiblings_AbandonForcesOtherChildren(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)
	backend := d.Tables()

	tmpl := insertDeployedTemplate(ctx, t, backend, node.TemplateSpec{
		Name: "cancel-siblings-fanout", Version: "1",
	})
	ck := "ck-cs"
	var inst persistence.InstanceRow
	var parentNode persistence.NodeRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		i, _ := seedInstanceWithMainScope(ctx, t, backend, tx, tmpl.ID, &ck)
		inst = i
		p, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: shared.UUID(uuid.New()), InstanceID: inst.ID, NodeType: "parent", Executor: "stub",
		}, tx)
		if err != nil {
			return err
		}
		parentNode = p
		return nil
	}))

	frameID := seedFrame(ctx, t, backend, inst.ID, parentNode.ID)
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

	// Resolve sub[0] → Abandon. The cancel_siblings walker must force-
	// Abandon sub[1] and sub[2] inside the same tx, then the parent
	// aggregator (strict → any failed → Abandon) fires the parent's
	// Abandon. Net producer-side: three child Abandon verbs + one
	// parent Abandon = 4 abandon calls total.
	resolveSubclaim(ctx, t, backend, args, subIDs[0], parentID, store, runtime.AggregateAbandon)

	// Each sub-claim's row must be promoted to abandoned (post-Stage-3
	// Promote-not-delete; the row is preserved past terminal for
	// forensics / retention).
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
	// Parent row must also be promoted to abandoned (strict aggregator
	// fired Abandon).
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

	// Producer-side: each sub-claim got Abandon (3) + parent got Abandon (1).
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

// TestResolveParentClaimChain_StrictCancelSiblings_SkipsDurableSibling
// pins the durable-sibling filter in
// `code:runtime/terminal_decision.go::cancelInFlightSiblings`. A
// `lifetime: durable` sibling that has already promoted to state =
// 'committed' must NOT be force-Abandoned — that would violate
// the durable-Commit contract. The other in-flight non-durable sibling
// is force-Abandoned normally.
func TestResolveParentClaimChain_StrictCancelSiblings_SkipsDurableSibling(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)
	backend := d.Tables()

	tmpl := insertDeployedTemplate(ctx, t, backend, node.TemplateSpec{
		Name: "cancel-siblings-skip-durable", Version: "1",
	})
	ck := "ck-cs-dur"
	var inst persistence.InstanceRow
	var parentNode persistence.NodeRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		i, _ := seedInstanceWithMainScope(ctx, t, backend, tx, tmpl.ID, &ck)
		inst = i
		p, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: shared.UUID(uuid.New()), InstanceID: inst.ID, NodeType: "parent", Executor: "stub",
		}, tx)
		if err != nil {
			return err
		}
		parentNode = p
		return nil
	}))

	frameID := seedFrame(ctx, t, backend, inst.ID, parentNode.ID)
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

	// Resolve sub[0] as a durable-Commit first. Post-Stage-3 the row
	// is promoted (state=committed, lifetime=durable preserved) but
	// bumps the parent's committed_children_count and recurses through
	// resolveParentClaimChain. Strict aggregator sees 1 commit + 0
	// abandons + 2 outstanding children → parent NOT yet resolved.
	resolveSubclaimWithLifetime(ctx, t, backend, args, subIDs[0], parentID, store, runtime.AggregateCommit, spec.ClaimLifetimeDurable)

	// Sanity: sub[0] is now state=committed; parent row still present.
	// (Lifetime column reflects the seed default — `seedFanOutParentAnd
	// Subclaims` doesn't override it. The state-promotion path is
	// uniform post-refactor; durable-vs-subgraph signal flows through
	// TerminalDecision.Lifetime only for control-flow decisions, not
	// for persisting the row's lifetime column.)
	var s0 *persistence.ClaimHandleRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := backend.ClaimHandles().Get(ctx, subIDs[0], tx)
		s0 = r
		return err
	}))
	require.NotNil(t, s0)
	require.Equal(t, spec.ClaimHandleStateCommitted, s0.State,
		"sub[0] must be promoted to state=committed on Commit")

	// Now resolve sub[1] as Abandon. The cancel_siblings walker should:
	//   - SKIP sub[0] because state != 'active' (durable-Commit
	//     promoted it to state=committed),
	//   - force-Abandon sub[2] (the only remaining in-flight sibling).
	// Then the parent aggregator (strict → any abandoned → Abandon)
	// fires Abandon on the parent.
	resolveSubclaim(ctx, t, backend, args, subIDs[1], parentID, store, runtime.AggregateAbandon)

	// sub[0] (durable) must still be present + state=committed.
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := backend.ClaimHandles().Get(ctx, subIDs[0], tx)
		s0 = r
		return err
	}))
	require.NotNil(t, s0,
		"durable-Commit sibling must survive cancel_siblings walk (state=committed filter)")
	require.Equal(t, spec.ClaimHandleStateCommitted, s0.State,
		"sub[0] must remain state=committed (durable-Commit contract)")
	// sub[1] (triggering) and sub[2] (force-Abandoned) must be promoted
	// to state=abandoned (Promote-not-delete; rows preserved for
	// forensics / retention).
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
	// Parent row must be promoted to state=abandoned (strict aggregator
	// fired Abandon — 1 commit + 2 abandons + 0 outstanding → any
	// failed → Abandon).
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

	// Producer-side: sub[0] received Commit (no Abandon!), sub[1] +
	// sub[2] received Abandon, parent received Abandon. sub[0] MUST NOT
	// have been Abandoned even though cancel_siblings fired.
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

// TestResolveParentClaimChain_StrictCancelSiblings_RecursivelyCancelsGrandchildren
// pins the spec §435 load-bearing requirement: under strict +
// cancel_siblings, when a sibling that is itself a fan-out parent
// (fan-out of fan-out) is force-Abandoned, its OWN in-flight
// grandchildren must each receive `Producer.Abandon` BEFORE the
// sibling's claim_handle row is deleted (otherwise the FK
// `parent_claim_handle_id ON DELETE SET NULL` orphans the
// grandchildren in-flight). The recursion is driven by
// `code:runtime/terminal_decision.go::cancelDescendantClaims`.
//
// Tree shape:
//
//	PARENT
//	├── sub[0]                 (resolves Abandon → triggers cancel_siblings)
//	└── sub[1]                 (sibling, itself a fan-out parent)
//	    ├── g1                 (grandchild, in-flight)
//	    └── g2                 (grandchild, in-flight)
//
// Expected producer Abandon verbs: sub[0], sub[1], g1, g2, PARENT — 5 total.
// Expected claim_handle rows after the run: all 5 deleted.
func TestResolveParentClaimChain_StrictCancelSiblings_RecursivelyCancelsGrandchildren(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)
	backend := d.Tables()

	tmpl := insertDeployedTemplate(ctx, t, backend, node.TemplateSpec{
		Name: "cancel-siblings-recursive", Version: "1",
	})
	ck := "ck-cs-rec"
	var inst persistence.InstanceRow
	var parentNode persistence.NodeRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		i, _ := seedInstanceWithMainScope(ctx, t, backend, tx, tmpl.ID, &ck)
		inst = i
		p, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: shared.UUID(uuid.New()), InstanceID: inst.ID, NodeType: "parent", Executor: "stub",
		}, tx)
		if err != nil {
			return err
		}
		parentNode = p
		return nil
	}))

	frameID := seedFrame(ctx, t, backend, inst.ID, parentNode.ID)
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
	// Seed PARENT → [sub[0], sub[1]] using the existing helper.
	parentID, subIDs := seedFanOutParentAndSubclaims(
		ctx, t, backend, parentRunID, parentNode.ID, "sup-CR",
		"cs-rec-store", policy, 2,
	)

	// Seed sub[1] → [g1, g2] manually so sub[1] becomes itself a fan-out
	// parent. The grandchildren live under sub[1]'s parent_claim_handle_id.
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

	// Resolve sub[0] → Abandon. The cancel_siblings walker:
	//   1. Force-Abandons sub[1] (the in-flight sibling), which itself
	//      triggers cancelDescendantClaims to force-Abandon g1 + g2.
	//   2. The descendant walk MUST fire BEFORE sub[1]'s own Delete so
	//      g1/g2's parent_claim_handle_id FK chain stays intact.
	// Then the parent aggregator fires Abandon on PARENT (strict +
	// any-failed → Abandon).
	// Net producer-side: 5 Abandon verbs total (sub[0], sub[1], g1, g2, PARENT).
	resolveSubclaim(ctx, t, backend, args, subIDs[0], parentID, store, runtime.AggregateAbandon)

	// All five claim_handle rows must be promoted to state=abandoned
	// (Promote-not-delete; rows preserved past terminal for forensics
	// / retention).
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

	// Producer-side: each row got exactly one Abandon, no Commits.
	for i, id := range allIDs {
		require.Equalf(t, 1, countCallsOnID(store.Calls(), id.String(), "abandon"),
			"%s must receive exactly one Abandon verb on the producer", allNames[i])
		require.Equalf(t, 0, countCallsOnID(store.Calls(), id.String(), "commit"),
			"%s must NOT receive Commit under recursive cancel_siblings", allNames[i])
	}
}
