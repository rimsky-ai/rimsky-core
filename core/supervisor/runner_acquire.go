// Spec §13.3 atomic acquisition under stores-redesign-v2.
//
// One pgx.Tx end to end:
//   - candidate selection (FOR UPDATE SKIP LOCKED) in a short read tx;
//   - per-candidate try tx: in-Go eligibility, advisory locks for
//     named locks, claimant-guarded UPDATE on rimsky_dispatch, region
//     re-evaluation per store via ModeCoexists + RegionsConflict,
//     per-spec lock acquisition (Insert + Open + UpdateAddress for
//     ClaimSpec; Insert only for NamedLockSpec), held-claim
//     rimsky_claim_holders inserts when the alias is in a held
//     subgraph (size > 1).
//   - COMMIT, then verify-before-run (separate read), then a second
//     short tx transitioning the node to running.
//
// Two primitives, two types: store.NamedLockSpec and store.ClaimSpec.
// The acquisition slice is []any; per-step type-switches dispatch.

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

// acquisition is the in-memory record of one successful §13.3
// acquisition. After a successful acquireCandidate the dispatch row
// is claimed by this supervisor (modulo verify-before-run race) and
// every Locks element has its LockHolderID + (for ClaimSpec)
// ClaimResult populated.
type acquisition struct {
	DispatchID shared.UUID
	NodeID     shared.UUID
	InstanceID shared.UUID
	NodeType   string
	Executor   string // "" → native (pure-cascade or claim-only)
	FrameID    shared.UUID
	Locks      []AcquiredLock
	NodeDef    *node.TemplateNodeDef
	// HeldSubgraphs is the template's holding-subgraph metadata,
	// computed once at acquisition. Drives the §5.6.3 claim-holder
	// inserts at acquisition (held: subgraph size > 1) and the §13.6
	// release-path branch (held vs. non-held alias).
	HeldSubgraphs []node.HoldingSubgraph
	// InstanceParams is the raw rimsky_instances.params for the
	// candidate, captured for substitution and ExecuteRequest assembly.
	InstanceParams map[string]any
}

// acquireCandidate runs §13.3 against the live database. Returns
// (acquisition, true, nil) on a successful claim, (zero, false, nil)
// when no eligible candidate or the verify-before-run / state-
// transition guard fired, or (zero, false, err) on a low-level error.
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

// selectCandidatesShortTx runs §13.3 step 1 in its own short read tx.
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

// tryAcquire runs §13.3 steps 2-5 for a single candidate inside the
// open tx. On success returns (acquisition, true, nil). In-Go
// ineligibility / in-tx race returns (zero, false, nil). On low-level
// error returns (zero, false, err) and the caller rolls back.
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

	storeCtx := store.WithTx(ctx, tx)
	acquiredLocks := make([]AcquiredLock, 0, len(specs))
	for _, sp := range specs {
		al, ok, err := acquireOneLock(storeCtx, args, tx, sp, cand, heartbeatInterval, heldSubgraphs)
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
// advisory lock per NamedLockSpec, in §13.7 sort order. Released on
// COMMIT/ROLLBACK of tx.
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

// acquireOneLock handles one spec inside the acquisition tx. For
// NamedLockSpec: insert the row only (limit enforcement is operator-
// config-driven; in v2 the supervisor honors operator-named-locks
// limits via the queue eligibility predicate, not here). For
// ClaimSpec: re-load existing region holders, re-check conflict +
// ModeCoexists, insert lock-holder row with address=NULL, call
// Store.Open inside the tx (so substrate writes participate),
// UPDATE the row's address column, and (if alias is in a held
// subgraph) insert one rimsky_claim_holders row per subgraph member.
func acquireOneLock(
	storeCtx context.Context, args RunArgs, tx pgx.Tx,
	sp any, cand queue.Candidate, heartbeatInterval time.Duration,
	heldSubgraphs []node.HoldingSubgraph,
) (AcquiredLock, bool, error) {
	switch spec := sp.(type) {
	case store.NamedLockSpec:
		return acquireNamedLock(storeCtx, args, tx, spec, cand, heartbeatInterval)
	case store.ClaimSpec:
		return acquireClaim(storeCtx, args, tx, spec, cand, heartbeatInterval, heldSubgraphs)
	}
	return AcquiredLock{}, false, fmt.Errorf("acquireOneLock: unknown spec kind %T", sp)
}

// acquireNamedLock inserts the named lock-holder row.
func acquireNamedLock(
	ctx context.Context, args RunArgs, tx pgx.Tx,
	spec store.NamedLockSpec, cand queue.Candidate, heartbeatInterval time.Duration,
) (AcquiredLock, bool, error) {
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

// acquireClaim runs §13.3 step 4 for one ClaimSpec.
func acquireClaim(
	ctx context.Context, args RunArgs, tx pgx.Tx,
	spec store.ClaimSpec, cand queue.Candidate, heartbeatInterval time.Duration,
	heldSubgraphs []node.HoldingSubgraph,
) (AcquiredLock, bool, error) {
	s, ok := args.StoreRegistry.GetStore(spec.StoreName)
	if !ok {
		return AcquiredLock{}, false, fmt.Errorf("acquireClaim: unknown store %q", spec.StoreName)
	}
	conflicted, err := evaluateRegionConflict(ctx, args, tx, s, spec, cand)
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
	regionInitial, err := json.Marshal(spec.Selector)
	if err != nil {
		return AcquiredLock{}, false, fmt.Errorf("acquireClaim: marshal selector: %w", err)
	}
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

	cr, err := s.Open(ctx, spec)
	if err != nil {
		return AcquiredLock{}, false, fmt.Errorf("acquireClaim: Open(%s): %w", spec.StoreName, err)
	}
	// Pick policies signal "pool empty" by returning a zero ClaimResult
	// without an error (per core/store/postgres/store.go::openPickPolicy).
	if len(cr.Address) == 0 && len(cr.Region) == 0 && len(cr.Payload) == 0 {
		return AcquiredLock{}, false, nil
	}

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
// store and runs RegionsConflict ∧ ModeCoexists against the candidate
// spec. Skips own-node rows. Returns true if any holder conflicts
// AND the modes don't coexist.
func evaluateRegionConflict(
	ctx context.Context, args RunArgs, tx pgx.Tx,
	s store.Store, spec store.ClaimSpec, cand queue.Candidate,
) (bool, error) {
	holders, err := args.LockHolders.ListByStoreRegion(ctx, tx, spec.StoreName)
	if err != nil {
		return false, fmt.Errorf("evaluateRegionConflict: ListByStoreRegion: %w", err)
	}
	candidateRegion, err := json.Marshal(spec.Selector)
	if err != nil {
		return false, err
	}
	myCaps := s.Capabilities()
	for _, h := range holders {
		if h.HolderNodeID == cand.NodeID && h.HolderSupervisorID == args.SupervisorID {
			continue
		}
		existingRegion, err := s.UnmarshalRegion(h.RegionData)
		if err != nil {
			return false, fmt.Errorf("evaluateRegionConflict: UnmarshalRegion: %w", err)
		}
		if !s.RegionsConflict(candidateRegion, existingRegion) {
			continue
		}
		var holderIntent store.Intent
		if h.Intent != nil {
			holderIntent = store.Intent(*h.Intent)
		}
		// Both holders are on the same store, so semantics match.
		if !store.ModeCoexists(spec.Intent, myCaps.WriteSemantics, holderIntent, myCaps.WriteSemantics) {
			return true, nil
		}
	}
	return false, nil
}

// updateLockHolderRegion writes a new region_data to a region-kind
// row, claimant-guarded. Used when Store.Open returns a substrate-
// chosen region (pick policies) different from the substituted
// selector the supervisor wrote at insert time.
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
// row per holding-subgraph member when the alias is held (size > 1).
// Resolves member node-types to in-instance node IDs via
// Nodes().ListByInstance.
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
// subgraph in the precomputed list. Returns ok=false when the alias
// has no entry (the acquirer's own claim never inherited).
func findHoldingSubgraphForAcquirer(subgraphs []node.HoldingSubgraph, acquirerType, alias string) (node.HoldingSubgraph, bool) {
	for _, sg := range subgraphs {
		if sg.AcquirerType == acquirerType && sg.Alias == alias {
			return sg, true
		}
	}
	return node.HoldingSubgraph{}, false
}

// verifyBeforeRun is the §13.3 step 4 separate-read guard.
func verifyBeforeRun(ctx context.Context, args RunArgs, acq acquisition) bool {
	ownership, err := args.Queue.GetClaimedBy(ctx, acq.DispatchID)
	if err != nil {
		args.Logger.Warn("verifyBeforeRun: GetClaimedBy failed",
			"dispatch_id", acq.DispatchID.String(), "error", err.Error())
		return false
	}
	return ownership.Kind == "claimed_by" && ownership.SupervisorID == args.SupervisorID
}

// handleOrphanedClaim is the §13.3 step 4 bail handler. Best-effort
// substrate Abandon for each ClaimSpec, then claimant-guarded DELETE
// per inserted lock-holder row, then orphaned_claim_lost_race event.
// Non-tx; the acquisition tx already committed.
func handleOrphanedClaim(ctx context.Context, args RunArgs, acq acquisition) {
	for _, lk := range acq.Locks {
		if lk.Store != nil {
			region := claimRegion(lk)
			address := claimAddress(lk)
			if err := lk.Store.Abandon(ctx, region, address, ""); err != nil {
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

// transitionToRunning is the §13.3 step 4.5 short-tx state transition.
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
