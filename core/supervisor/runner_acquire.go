// Atomic acquisition under stores-redesign-v3 spec §7.3.
//
// Per-candidate try tx (rimsky-side bookkeeping only):
//   - candidate selection (FOR UPDATE SKIP LOCKED) in a short read tx;
//   - per-candidate try tx: in-Go eligibility, advisory locks for
//     named locks, claimant-guarded UPDATE on rimsky_dispatch, region
//     re-evaluation per store via byte-equal + ModeCoexists, per-spec
//     lock acquisition (Insert + remote Open + UpdateAddress for
//     ClaimSpec; Insert only for NamedLockSpec), held-claim
//     rimsky_claim_holders inserts when the alias is in a held
//     subgraph.
//   - COMMIT, then verify-before-run (separate read), then a second
//     short tx transitioning the node to running.
//
// Store.Open is invoked OVER THE WIRE in v3; the substrate runs its
// own state mutation in its own transaction. Tx-sharing via
// store.WithTx / TxFromContext is gone.
//
// Two primitives, two types: store.NamedLockSpec and store.ClaimSpec.

package supervisor

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/queue"
	pgqueue "github.com/fallguy/rimsky/core/queue/postgres"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/storage"
	pgstorage "github.com/fallguy/rimsky/core/storage/postgres"
	"github.com/fallguy/rimsky/core/store"
)

// acquisition is the in-memory record of one successful acquisition.
type acquisition struct {
	DispatchID     shared.UUID
	NodeID         shared.UUID
	InstanceID     shared.UUID
	NodeType       string
	Executor       string
	FrameID        shared.UUID
	Locks          []AcquiredLock
	NodeDef        *node.TemplateNodeDef
	HeldSubgraphs  []node.HoldingSubgraph
	InstanceParams map[string]any
}

// acquireCandidate runs the §7.3 flow against the live database.
func acquireCandidate(ctx context.Context, args RunArgs, heartbeatInterval time.Duration) (acquisition, bool, error) {
	candidates, err := selectCandidatesShortTx(ctx, args)
	if err != nil {
		return acquisition{}, false, err
	}
	if len(candidates) == 0 {
		return acquisition{}, false, nil
	}

	for _, cand := range candidates {
		if cand.FrameID == (shared.UUID{}) {
			args.Logger.Warn("acquireCandidate: skipping candidate with nil frame_id",
				"dispatch_id", cand.DispatchID.String(),
				"node_id", cand.NodeID.String(),
				"reason", "frame_id_null")
			continue
		}
		acq, ok, err := tryAcquireWithTx(ctx, args, cand, heartbeatInterval)
		if err != nil {
			return acquisition{}, false, err
		}
		if !ok {
			continue
		}
		if !verifyBeforeRun(ctx, args, acq) {
			handleOrphanedClaim(ctx, args, acq)
			return acquisition{}, false, nil
		}
		if err := transitionToRunning(ctx, args, acq); err != nil {
			handleOrphanedClaim(ctx, args, acq)
			return acquisition{}, false, nil
		}
		_ = args.Storage.Nodes().UpdateHeartbeat(ctx, acq.NodeID, args.Clock.Now(), args.SupervisorID, nil)
		_ = args.Storage.Events().Append(ctx, storage.EventAppendInput{
			NodeID: &acq.NodeID, InstanceID: &acq.InstanceID,
			Kind: "work_started", Payload: map[string]any{
				"supervisor_id": args.SupervisorID,
				"dispatch_id":   acq.DispatchID.String(),
			},
		}, nil)
		for _, lk := range acq.Locks {
			emitLockAcquired(ctx, args, acq, lk)
		}
		return acq, true, nil
	}
	return acquisition{}, false, nil
}

// selectCandidatesShortTx runs the candidate-selection helper in its
// own short read tx.
func selectCandidatesShortTx(ctx context.Context, args RunArgs) ([]queue.Candidate, error) {
	limit := args.SelectCandidatesLimit
	if limit <= 0 {
		limit = 8
	}
	tx, err := args.QueuePool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("acquireCandidate: begin select tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	candidates, err := args.Queue.SelectCandidates(ctx, tx, queue.SelectCandidatesRequest{
		AcceptedExecutors: args.AcceptedExecutors,
		AcceptedStores:    args.AcceptedStores,
		Limit:             limit,
	})
	if err != nil {
		return nil, fmt.Errorf("acquireCandidate: SelectCandidates: %w", err)
	}
	return candidates, nil
}

// tryAcquireWithTx wraps tryAcquire in its own tx so a failed
// candidate's partial mutations roll back rather than leak into the
// next candidate.
func tryAcquireWithTx(
	ctx context.Context, args RunArgs, cand queue.Candidate,
	heartbeatInterval time.Duration,
) (acquisition, bool, error) {
	tx, err := args.QueuePool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return acquisition{}, false, fmt.Errorf("tryAcquireWithTx: begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()
	acq, ok, err := tryAcquire(ctx, args, tx, cand, heartbeatInterval)
	if err != nil {
		return acquisition{}, false, err
	}
	if !ok {
		return acquisition{}, false, nil
	}
	if err := tx.Commit(ctx); err != nil {
		return acquisition{}, false, fmt.Errorf("tryAcquireWithTx: commit: %w", err)
	}
	committed = true
	return acq, true, nil
}

// tryAcquire runs the acquisition steps for a single candidate inside
// the open rimsky-side tx. Note: Store.Open RPCs over the wire and the
// substrate runs in its own tx (per spec §7.3).
func tryAcquire(
	ctx context.Context, args RunArgs, tx pgx.Tx,
	cand queue.Candidate, heartbeatInterval time.Duration,
) (acquisition, bool, error) {
	nd, err := args.Storage.Nodes().Get(ctx, cand.NodeID, nil)
	if err != nil {
		return acquisition{}, false, fmt.Errorf("tryAcquire: nodes.Get: %w", err)
	}
	if nd == nil {
		return acquisition{}, false, nil
	}
	inst, _ := args.Storage.Instances().Get(ctx, nd.InstanceID, nil)
	tmpl := lookupTemplate(ctx, args, inst)
	nodeDef := lookupNodeDef(tmpl, nd.NodeType)
	specs, err := buildLockSpecs(ctx, args, nd, nodeDef, inst)
	if err != nil {
		args.Logger.Warn("tryAcquire: lock-spec substitution failed",
			"node_id", cand.NodeID.String(), "error", err.Error())
		return acquisition{}, false, nil
	}
	sortLockSpecs(specs)

	if err := takeNamedAdvisoryLocks(ctx, tx, specs); err != nil {
		return acquisition{}, false, err
	}

	claimed, err := args.Queue.ClaimDispatchRow(ctx, tx, cand.DispatchID, args.SupervisorID)
	if err != nil {
		return acquisition{}, false, fmt.Errorf("tryAcquire: ClaimDispatchRow: %w", err)
	}
	if !claimed {
		return acquisition{}, false, nil
	}

	heldSubgraphs := node.HoldingSubgraphsForTemplate(tmpl)

	acquiredLocks := make([]AcquiredLock, 0, len(specs))
	for _, sp := range specs {
		al, ok, err := acquireOneLock(ctx, args, tx, sp, cand, heartbeatInterval, heldSubgraphs)
		if err != nil {
			return acquisition{}, false, err
		}
		if !ok {
			return acquisition{}, false, nil
		}
		acquiredLocks = append(acquiredLocks, al)
	}

	out := acquisition{
		DispatchID:    cand.DispatchID,
		NodeID:        cand.NodeID,
		InstanceID:    nd.InstanceID,
		NodeType:      nd.NodeType,
		Executor:      nd.Executor,
		FrameID:       cand.FrameID,
		Locks:         acquiredLocks,
		NodeDef:       nodeDef,
		HeldSubgraphs: heldSubgraphs,
	}
	if inst != nil {
		out.InstanceParams = inst.Params
	}
	return out, true, nil
}

// takeNamedAdvisoryLocks walks the sorted spec slice and takes one
// advisory lock per NamedLockSpec.
func takeNamedAdvisoryLocks(ctx context.Context, tx pgx.Tx, specs []any) error {
	for _, sp := range specs {
		named, ok := sp.(store.NamedLockSpec)
		if !ok {
			continue
		}
		if err := pgqueue.TakeNamedLockAdvisory(ctx, tx, named.Name); err != nil {
			return fmt.Errorf("takeNamedAdvisoryLocks(%q): %w", named.Name, err)
		}
	}
	return nil
}

// acquireOneLock handles one spec inside the acquisition tx.
func acquireOneLock(
	ctx context.Context, args RunArgs, tx pgx.Tx,
	sp any, cand queue.Candidate, heartbeatInterval time.Duration,
	heldSubgraphs []node.HoldingSubgraph,
) (AcquiredLock, bool, error) {
	switch spec := sp.(type) {
	case store.NamedLockSpec:
		return acquireNamedLock(ctx, args, tx, spec, cand, heartbeatInterval)
	case store.ClaimSpec:
		return acquireClaim(ctx, args, tx, spec, cand, heartbeatInterval, heldSubgraphs)
	}
	return AcquiredLock{}, false, fmt.Errorf("acquireOneLock: unknown spec kind %T", sp)
}

// acquireNamedLock enforces the counter-semaphore limit then inserts
// the named lock-holder row. The per-name advisory lock has been
// taken upstream (takeNamedAdvisoryLocks); under that lock the
// CountByNamedLock + Insert pair is atomic against the limit.
//
// When the operator's NamedLocks config has no entry for this name,
// no limit is enforced (limit defaults to ∞). Templates referencing
// undeclared names should have failed validation at deploy time
// (control-api wires NamedLockDeclared unconditionally).
func acquireNamedLock(
	ctx context.Context, args RunArgs, tx pgx.Tx,
	spec store.NamedLockSpec, cand queue.Candidate, heartbeatInterval time.Duration,
) (AcquiredLock, bool, error) {
	if cfg, ok := args.NamedLocks.Get(spec.Name); ok {
		count, err := args.LockHolders.CountByNamedLock(ctx, tx, spec.Name)
		if err != nil {
			return AcquiredLock{}, false, fmt.Errorf("acquireNamedLock: CountByNamedLock(%q): %w", spec.Name, err)
		}
		if count >= cfg.Limit {
			return AcquiredLock{}, false, nil
		}
	}
	rowID := uuid.New()
	frameID := cand.FrameID
	now := args.Clock.Now()
	nameCopy := spec.Name
	row := store.LockHolderRow{
		ID:                 rowID,
		Kind:               store.LockHolderKindNamed,
		LockName:           &nameCopy,
		HolderSupervisorID: args.SupervisorID,
		HolderNodeID:       cand.NodeID,
		ClaimedAt:          now,
		LastHeartbeatAt:    now,
		ExpiresAt:          now.Add(5 * heartbeatInterval),
		FrameID:            &frameID,
	}
	if err := args.LockHolders.Insert(ctx, tx, row); err != nil {
		return AcquiredLock{}, false, fmt.Errorf("acquireNamedLock: Insert: %w", err)
	}
	return AcquiredLock{
		Spec:         spec,
		LockHolderID: rowID,
	}, true, nil
}

// acquireClaim runs the claim-acquisition steps per spec §7.3 step 4.
//
// Conflict detection uses byte-equal comparison on region bytes (per
// spec §7.7); the candidate's pre-Open region is the substituted-
// selector bytes. For pick-policy claims the substrate's
// FOR UPDATE SKIP LOCKED prevents two supervisors picking the same
// item independently of rimsky's predicate. For regional claims
// rimsky's predicate is the source of truth for invariant 4b.
//
// To prevent two supervisors from concurrently passing the in-Go
// region-conflict predicate against each other's uncommitted INSERTs
// (READ COMMITTED hides them), this function takes a per-(store_name,
// region_data) transactional advisory lock before evaluateRegionConflict
// runs. Analogous to the named-lock advisory; under the same lock the
// list-then-INSERT pair is atomic against any concurrent acquirer
// targeting the same (store, region) pair.
func acquireClaim(
	ctx context.Context, args RunArgs, tx pgx.Tx,
	spec store.ClaimSpec, cand queue.Candidate, heartbeatInterval time.Duration,
	heldSubgraphs []node.HoldingSubgraph,
) (AcquiredLock, bool, error) {
	s, ok := args.StoreRegistry.Get(spec.StoreName)
	if !ok {
		return AcquiredLock{}, false, fmt.Errorf("acquireClaim: unknown store %q", spec.StoreName)
	}
	myCaps, err := s.Capabilities(ctx)
	if err != nil {
		return AcquiredLock{}, false, fmt.Errorf("acquireClaim: Capabilities: %w", err)
	}
	regionInitial, err := json.Marshal(spec.Selector)
	if err != nil {
		return AcquiredLock{}, false, fmt.Errorf("acquireClaim: marshal selector: %w", err)
	}
	if err := pgqueue.TakeRegionAdvisory(ctx, tx, spec.StoreName, regionInitial); err != nil {
		return AcquiredLock{}, false, fmt.Errorf("acquireClaim: TakeRegionAdvisory: %w", err)
	}
	conflicted, err := evaluateRegionConflict(ctx, args, tx, myCaps, spec, cand)
	if err != nil {
		return AcquiredLock{}, false, err
	}
	if conflicted {
		return AcquiredLock{}, false, nil
	}

	rowID := uuid.New()
	frameID := cand.FrameID
	now := args.Clock.Now()
	storeNameCopy := spec.StoreName
	intentCopy := string(spec.Intent)
	row := store.LockHolderRow{
		ID:                 rowID,
		Kind:               store.LockHolderKindRegion,
		StoreName:          &storeNameCopy,
		RegionData:         regionInitial,
		Intent:             &intentCopy,
		HolderSupervisorID: args.SupervisorID,
		HolderNodeID:       cand.NodeID,
		ClaimedAt:          now,
		LastHeartbeatAt:    now,
		ExpiresAt:          now.Add(5 * heartbeatInterval),
		FrameID:            &frameID,
	}
	if err := args.LockHolders.Insert(ctx, tx, row); err != nil {
		return AcquiredLock{}, false, fmt.Errorf("acquireClaim: Insert: %w", err)
	}

	claimID := store.ClaimID(rowID.String())
	outcome, err := s.Open(ctx, claimID, spec)
	if err != nil {
		return AcquiredLock{}, false, fmt.Errorf("acquireClaim: Open(%s): %w", spec.StoreName, err)
	}
	// Substrate has nothing to give right now (e.g. drained items-table
	// queue). Per the 2026-04-30 cleanup spec the substrate signals this
	// via OpenOutcome.Available=false (was: all-empty ClaimResult under
	// v3 §4.7's "pool-empty" convention). Roll back the tx and skip;
	// the next scheduler tick may retry.
	if !outcome.Available {
		return AcquiredLock{}, false, nil
	}
	cr := outcome.Result

	if err := args.LockHolders.UpdateAddress(ctx, tx, rowID, args.SupervisorID, cr.Address); err != nil {
		return AcquiredLock{}, false, fmt.Errorf("acquireClaim: UpdateAddress: %w", err)
	}
	// Pick-policy claims have substrate-chosen region; regional claims
	// keep the substituted selector (already written above).
	if len(cr.Region) > 0 && string(cr.Region) != string(regionInitial) {
		if err := updateLockHolderRegion(ctx, tx, rowID, args.SupervisorID, cr.Region); err != nil {
			return AcquiredLock{}, false, err
		}
	}

	if err := insertHeldClaimHoldersAtAcquire(ctx, args, tx, rowID, cand, spec.Alias, heldSubgraphs); err != nil {
		return AcquiredLock{}, false, err
	}

	return AcquiredLock{
		Spec:         spec,
		LockHolderID: rowID,
		ClaimResult:  cr,
		Store:        s,
		Alias:        spec.Alias,
	}, true, nil
}

// evaluateRegionConflict re-loads existing region holders for the
// store and runs RegionsByteEqual ∧ ModeCoexists against the candidate
// spec. Skips own-node rows. Returns true if any holder conflicts AND
// the modes don't coexist.
//
// Per spec §7.7: byte-equal comparison; substrate canonicalizes its
// region bytes such that two claims that should conflict produce
// byte-equal regions. The candidate's pre-Open region is the
// substituted-selector bytes (regional claims) — for pick-policy
// claims the actual collision check happens in the substrate's
// FOR UPDATE SKIP LOCKED.
//
// ModeCoexists is asymmetric: the (intent, write_semantics) pair on
// the candidate side and the holder side may differ when holders live
// in a different store (cross-store overlap is impossible by
// construction since holders are filtered by store_name above — but
// the holder's store may have been re-registered with different caps,
// so we re-look-up its Capabilities).
func evaluateRegionConflict(
	ctx context.Context, args RunArgs, tx pgx.Tx,
	myCaps store.Capabilities, spec store.ClaimSpec, cand queue.Candidate,
) (bool, error) {
	holders, err := args.LockHolders.ListByStoreRegion(ctx, tx, spec.StoreName)
	if err != nil {
		return false, fmt.Errorf("evaluateRegionConflict: ListByStoreRegion: %w", err)
	}
	candidateRegion, err := json.Marshal(spec.Selector)
	if err != nil {
		return false, err
	}
	for _, h := range holders {
		if h.HolderNodeID == cand.NodeID && h.HolderSupervisorID == args.SupervisorID {
			continue
		}
		if !store.RegionsByteEqual(candidateRegion, h.RegionData) {
			continue
		}
		var holderIntent store.Intent
		if h.Intent != nil {
			holderIntent = store.Intent(*h.Intent)
		}
		holderCaps, err := holderCapabilitiesFor(ctx, args, h)
		if err != nil {
			return false, err
		}
		if !store.ModeCoexists(spec.Intent, myCaps.WriteSemantics, holderIntent, holderCaps.WriteSemantics) {
			return true, nil
		}
	}
	return false, nil
}

// holderCapabilitiesFor looks up the capabilities of the store backing
// a region-kind lock-holder row. Falls back to the candidate's caps
// when the holder's store is no longer registered (best-effort: the
// row is about to be reaped).
func holderCapabilitiesFor(
	ctx context.Context, args RunArgs, h store.LockHolderRow,
) (store.Capabilities, error) {
	if h.StoreName == nil {
		return store.Capabilities{}, nil
	}
	s, ok := args.StoreRegistry.Get(*h.StoreName)
	if !ok {
		return store.Capabilities{}, nil
	}
	caps, err := s.Capabilities(ctx)
	if err != nil {
		return store.Capabilities{}, fmt.Errorf("holderCapabilitiesFor(%q): %w", *h.StoreName, err)
	}
	return caps, nil
}

// updateLockHolderRegion writes a new region_data to a region-kind
// row, claimant-guarded.
func updateLockHolderRegion(
	ctx context.Context, tx pgx.Tx, id shared.UUID, supervisorID string, region json.RawMessage,
) error {
	_, err := tx.Exec(ctx,
		`UPDATE rimsky_lock_holders
		    SET region_data = $1
		  WHERE id = $2 AND holder_supervisor_id = $3`,
		[]byte(region), id, supervisorID,
	)
	if err != nil {
		return fmt.Errorf("updateLockHolderRegion: %w", err)
	}
	return nil
}

// insertHeldClaimHoldersAtAcquire inserts one rimsky_claim_holders
// row per holding-subgraph member when the alias is held.
func insertHeldClaimHoldersAtAcquire(
	ctx context.Context, args RunArgs, tx pgx.Tx,
	lockHolderID shared.UUID, cand queue.Candidate, alias string,
	heldSubgraphs []node.HoldingSubgraph,
) error {
	subgraph, ok := findHoldingSubgraphForAcquirer(heldSubgraphs, cand.NodeType, alias)
	if !ok || !subgraph.IsHeld() {
		return nil
	}
	nd, err := args.Storage.Nodes().Get(ctx, cand.NodeID, nil)
	if err != nil || nd == nil {
		return fmt.Errorf("insertHeldClaimHoldersAtAcquire: nodes.Get: %w", err)
	}
	siblings, err := args.Storage.Nodes().ListByInstance(ctx, nd.InstanceID, nil)
	if err != nil {
		return fmt.Errorf("insertHeldClaimHoldersAtAcquire: ListByInstance: %w", err)
	}
	memberSet := make(map[string]struct{}, len(subgraph.Members))
	for _, m := range subgraph.Members {
		memberSet[m] = struct{}{}
	}
	stx := pgstorage.WrapPgxTx(tx)
	frameID := cand.FrameID
	for _, sib := range siblings {
		if _, ok := memberSet[sib.NodeType]; !ok {
			continue
		}
		err := args.Storage.ClaimHolders().Insert(ctx, storage.ClaimHolderInsertInput{
			ID:           uuid.New(),
			LockHolderID: lockHolderID,
			HolderNodeID: sib.ID,
			FrameID:      &frameID,
		}, stx)
		if err != nil {
			return fmt.Errorf("insertHeldClaimHoldersAtAcquire: Insert: %w", err)
		}
	}
	return nil
}

// findHoldingSubgraphForAcquirer locates the (acquirerType, alias)
// subgraph in the precomputed list.
func findHoldingSubgraphForAcquirer(subgraphs []node.HoldingSubgraph, acquirerType, alias string) (node.HoldingSubgraph, bool) {
	for _, sg := range subgraphs {
		if sg.AcquirerType == acquirerType && sg.Alias == alias {
			return sg, true
		}
	}
	return node.HoldingSubgraph{}, false
}

// verifyBeforeRun is the separate-read guard.
func verifyBeforeRun(ctx context.Context, args RunArgs, acq acquisition) bool {
	ownership, err := args.Queue.GetClaimedBy(ctx, acq.DispatchID)
	if err != nil {
		args.Logger.Warn("verifyBeforeRun: GetClaimedBy failed",
			"dispatch_id", acq.DispatchID.String(), "error", err.Error())
		return false
	}
	return ownership.Kind == "claimed_by" && ownership.SupervisorID == args.SupervisorID
}

// handleOrphanedClaim is the race-detection bail path: the supervisor
// has already opened the claim, inserted the lock-holder row, and
// committed the acquisition tx — and then verify-before-run discovered
// that another supervisor stole the dispatch row in the gap between
// commit and the second-read guard. The supervisor knows it just
// opened the substrate state and is now unwinding the in-progress
// acquisition; it owns the cleanup and calls Abandon on the substrate
// to release any partial state, then deletes its own lock-holder row
// claimant-guarded, then emits orphaned_claim_lost_race.
//
// This is NOT the periodic orphan reaper. The periodic reaper at
// `core/scheduler/sweep_locks.go::sweepLockHolders` deletes expired
// lock-holder rows WITHOUT firing Abandon, per v3 spec §7.5: the
// store's own TTL/sweep handles internal state for owners that
// crashed without unwinding. The two paths are deliberately distinct:
// the bail path fires Abandon because the supervisor knows what it
// just did; the reaper does NOT fire Abandon because it can't
// distinguish a crashed-supervisor state from any other.
func handleOrphanedClaim(ctx context.Context, args RunArgs, acq acquisition) {
	for _, lk := range acq.Locks {
		if lk.Store != nil {
			region := claimRegion(lk)
			address := claimAddress(lk)
			claimID := store.ClaimID(lk.LockHolderID.String())
			if err := lk.Store.Abandon(ctx, claimID, region, address); err != nil {
				args.Logger.Warn("handleOrphanedClaim: Abandon failed",
					"store", storeNameForSpec(lk.Spec), "error", err.Error())
			}
		}
		_ = args.Storage.Transaction(ctx, func(ctx context.Context, tx storage.Tx) error {
			return args.Storage.LockHolders().Delete(ctx, lk.LockHolderID, args.SupervisorID, tx)
		})
	}
	_ = args.Storage.Events().Append(ctx, storage.EventAppendInput{
		NodeID: &acq.NodeID, InstanceID: &acq.InstanceID,
		Kind: "orphaned_claim_lost_race",
		Payload: map[string]any{
			"dispatch_id":   acq.DispatchID.String(),
			"supervisor_id": args.SupervisorID,
		},
	}, nil)
}

// transitionToRunning is the short-tx state transition.
func transitionToRunning(ctx context.Context, args RunArgs, acq acquisition) error {
	return args.Storage.Nodes().UpdateState(ctx, acq.NodeID,
		shared.NodeStateRunning, node.ReasonDispatchClaimed, nil)
}

// emitLockAcquired emits the per-spec lock_acquired event.
func emitLockAcquired(ctx context.Context, args RunArgs, acq acquisition, lk AcquiredLock) {
	payload := map[string]any{
		"holder_id":     lk.LockHolderID.String(),
		"supervisor_id": args.SupervisorID,
	}
	switch sp := lk.Spec.(type) {
	case store.NamedLockSpec:
		payload["lock_kind"] = string(store.LockHolderKindNamed)
		payload["lock_name"] = sp.Name
	case store.ClaimSpec:
		payload["lock_kind"] = string(store.LockHolderKindRegion)
		payload["store_name"] = sp.StoreName
		payload["alias"] = sp.Alias
		payload["intent"] = string(sp.Intent)
	}
	_ = args.Storage.Events().Append(ctx, storage.EventAppendInput{
		NodeID: &acq.NodeID, InstanceID: &acq.InstanceID,
		Kind: "lock_acquired", Payload: payload,
	}, nil)
}

// claimRegion returns the substrate's region bytes for a ClaimSpec
// acquisition; nil for NamedLockSpec.
func claimRegion(lk AcquiredLock) []byte {
	if lk.Store == nil {
		return nil
	}
	return []byte(lk.ClaimResult.Region)
}

// claimAddress returns the substrate's address bytes for a ClaimSpec
// acquisition; nil for NamedLockSpec.
func claimAddress(lk AcquiredLock) []byte {
	if lk.Store == nil {
		return nil
	}
	return []byte(lk.ClaimResult.Address)
}
