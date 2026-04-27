// Spec §13.3 atomic acquisition + §17.1 steps 1, 4, 4.5.
//
// One pgx.Tx end to end: candidate selection (FOR UPDATE SKIP LOCKED),
// in-Go eligibility, advisory locks + recount per named lock,
// claimant-guarded UPDATE on rimsky_dispatch, region re-evaluation per
// store, store.AcquireLock + lock-holder INSERT in §13.7 sort order.
// COMMIT, then verify-before-run (separate read), then a second short tx
// transitioning the node to running.

package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/queue"
	pgqueue "github.com/fallguy/rimsky/core/queue/postgres"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/storage"
	"github.com/fallguy/rimsky/core/store"
)

// acquisition is the in-memory record of one successful §13.3
// acquisition. Carries the candidate, the lock-holder rows the runner
// inserted, and the resolved per-spec data needed for §17.1 step 2
// onwards. After a successful acquireCandidate call the caller can rely
// on the dispatch row being claimed by this supervisor (modulo
// verify-before-run race) and on every Locks element having both Spec
// and Handle populated.
type acquisition struct {
	DispatchID shared.UUID
	NodeID     shared.UUID
	InstanceID shared.UUID
	NodeType   string
	Executor   string // "" → native (claim-only or pure-cascade)
	// FrameID is the frame the dispatch row carries (per spec §10.2).
	// Propagated to lock_holder / claim_holder inserts (observability)
	// and to cascade message-passes at terminal commit (§4.4).
	FrameID shared.UUID
	Locks   []AcquiredLock
	// NodeDef is the template-derived per-node-type definition for
	// userdata, attributes schema, claim resolutions, error policy
	// chain. Loaded once at acquisition; the dispatch and terminal
	// paths reuse it. May be nil if no matching def was found (the
	// dispatch SELECT does not actually require it; downstream code
	// degrades gracefully).
	NodeDef *node.TemplateNodeDef
	// InstanceParams is the parsed rimsky_instances.params map for the
	// candidate's instance, captured during acquisition for use in
	// substitution and ExecuteRequest assembly.
	InstanceParams map[string]any
}

// acquireCandidate runs §13.3 steps 1–4.5 against the live database.
// Returns (acquisition, true, nil) on a successful claim, (zero,
// false, nil) when no eligible candidate or the verify-before-run /
// state-transition guard fired, or (zero, false, err) on a low-level
// error (DB unreachable, etc.).
//
// Each candidate gets its own pgx.Tx so a failed candidate's partial
// mutations (RebindForResume in step 3a, ClaimDispatchRow in step 3c,
// AcquireLock + LockHolders.Insert in step 3e) roll back instead of
// leaking into the next candidate's commit. Candidate selection runs
// in a separate short read tx so the FOR UPDATE SKIP LOCKED snapshot
// is captured once and per-candidate tries don't repeat the SELECT.
func acquireCandidate(ctx context.Context, args RunArgs, heartbeatInterval time.Duration) (acquisition, bool, error) {
	candidates, err := selectCandidatesShortTx(ctx, args)
	if err != nil {
		return acquisition{}, false, err
	}
	if len(candidates) == 0 {
		return acquisition{}, false, nil
	}

	// Try each candidate under its own tx so failures from candidate i
	// (e.g. region conflict surfaced in 3d, claim pool empty in 3e)
	// roll back cleanly without contaminating candidate i+1.
	for _, cand := range candidates {
		// Defensive: blessed-invariant 19 forbids in-flight dispatch rows
		// without a frame_id. The DB column is NOT NULL so this is "can't
		// happen" — but log loudly and skip if the invariant is violated.
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

		// Step 4 — verify-before-run.
		if !verifyBeforeRun(ctx, args, acq) {
			handleOrphanedClaim(ctx, args, acq)
			return acquisition{}, false, nil
		}

		// Step 4.5 — transition state. Bail to orphan handler on
		// rejection (state moved out of stale between commit and now,
		// e.g. operator invalidate fired).
		if err := transitionToRunning(ctx, args, acq); err != nil {
			handleOrphanedClaim(ctx, args, acq)
			return acquisition{}, false, nil
		}

		// Heartbeat-stamp + work_started event.
		_ = args.Storage.Nodes().UpdateHeartbeat(ctx, acq.NodeID, args.Clock.Now(), args.SupervisorID, nil)
		_ = args.Storage.Events().Append(ctx, storage.EventAppendInput{
			NodeID: &acq.NodeID, InstanceID: &acq.InstanceID,
			Kind: "work_started", Payload: map[string]any{
				"supervisor_id": args.SupervisorID,
				"dispatch_id":   acq.DispatchID.String(),
			},
		}, nil)
		// Emit one lock_acquired event per acquired (or rebound) row.
		for _, lk := range acq.Locks {
			emitLockAcquired(ctx, args, acq, lk)
		}
		return acq, true, nil
	}
	// No candidate eligible.
	return acquisition{}, false, nil
}

// selectCandidatesShortTx runs §13.3 step 1 (candidate selection) in
// its own short read tx. The FOR UPDATE SKIP LOCKED rows are released
// when this tx ends; the per-candidate tx in tryAcquireWithTx
// re-selects the row implicitly via ClaimDispatchRow's claimant-guarded
// UPDATE.
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

// tryAcquireWithTx wraps tryAcquire in its own tx. On a successful
// candidate the tx commits (releasing advisory locks, exposing the
// claim + lock-holder rows). On in-Go ineligibility or in-tx race the
// tx rolls back so partial mutations from steps 3a / 3c / 3e do not
// leak into the next candidate.
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

// tryAcquire runs §13.3 steps 2 + 3 for a single candidate inside the
// open tx. On success returns (acquisition, true, nil). On in-Go
// ineligibility or in-tx race the function returns (zero, false, nil)
// without rolling back the tx — the caller can try the next candidate.
// On low-level error returns (zero, false, err) and the caller rolls
// back.
func tryAcquire(
	ctx context.Context, args RunArgs, tx pgx.Tx,
	cand queue.Candidate, heartbeatInterval time.Duration,
) (acquisition, bool, error) {
	// Pull the node row + instance + template for the candidate.
	nd, err := args.Storage.Nodes().Get(ctx, cand.NodeID, nil)
	if err != nil {
		return acquisition{}, false, fmt.Errorf("tryAcquire: nodes.Get: %w", err)
	}
	if nd == nil {
		// Stale dispatch row pointing at a deleted node.
		return acquisition{}, false, nil
	}
	inst, _ := args.Storage.Instances().Get(ctx, nd.InstanceID, nil)
	tmpl := lookupTemplate(ctx, args, inst)
	nodeDef := lookupNodeDef(tmpl, nd.NodeType)
	specs, err := buildLockSpecs(ctx, args, nd, nodeDef, inst)
	if err != nil {
		// Region/lock-name substitution failed. Treat as ineligibility
		// for now (the candidate can be retried after the operator
		// fixes params); a richer impl would emit
		// template_resolution_failed and route through policy here.
		args.Logger.Warn("tryAcquire: lock-spec substitution failed",
			"node_id", cand.NodeID.String(), "error", err.Error())
		return acquisition{}, false, nil
	}
	sortLockSpecs(specs)

	// Step 2 — in-Go eligibility hints (named-lock count, region
	// conflict, claim availability). Hint-only; the authoritative
	// re-checks live in step 3.
	if ok, err := hintEligibility(ctx, args, cand.NodeID, specs); err != nil {
		return acquisition{}, false, err
	} else if !ok {
		return acquisition{}, false, nil
	}

	// Step 3a — rebind probe per region/claim spec for the same
	// (node_id, store_name, supervisor_id) with expires_at > now().
	// Named locks are NEVER rebound — they always go through the fresh
	// path.
	rebound := make(map[int]store.LockHolderRow)
	for i, sp := range specs {
		storeName := storeNameForSpec(sp)
		if storeName == "" {
			continue // named lock
		}
		existing, err := args.LockHolders.ListByNodeAndStore(ctx, cand.NodeID, storeName, args.SupervisorID)
		if err != nil {
			return acquisition{}, false, fmt.Errorf("tryAcquire: ListByNodeAndStore: %w", err)
		}
		if len(existing) == 0 {
			continue
		}
		// Reuse the first matching unexpired row; refresh its TTL.
		updated, err := args.LockHolders.RebindForResume(
			ctx, tx, existing[0].ID, args.SupervisorID,
			int((5 * heartbeatInterval).Seconds()),
		)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				// Row vanished between probe and update — fall through
				// to the fresh path.
				continue
			}
			return acquisition{}, false, fmt.Errorf("tryAcquire: RebindForResume: %w", err)
		}
		rebound[i] = updated
	}

	// Step 3b — per-named-lock advisory + recount. Sort order applies.
	for _, sp := range specs {
		named, ok := sp.(store.NamedLockSpec)
		if !ok {
			continue
		}
		if err := pgqueue.TakeNamedLockAdvisory(ctx, tx, named.Name); err != nil {
			return acquisition{}, false, fmt.Errorf("tryAcquire: TakeNamedLockAdvisory(%q): %w", named.Name, err)
		}
		count, err := args.LockHolders.CountByNamedLock(ctx, tx, named.Name)
		if err != nil {
			return acquisition{}, false, fmt.Errorf("tryAcquire: CountByNamedLock(%q): %w", named.Name, err)
		}
		limit := named.Limit
		if named.Mode == store.LockModeMutex {
			limit = 1
		}
		if count >= limit {
			// Cap reached under the advisory lock — bail (this candidate
			// not eligible right now). The advisory lock is released on
			// rollback; caller will rollback the tx and try the next
			// candidate via a fresh tx the next claim tick.
			return acquisition{}, false, nil
		}
	}

	// Step 3c — claimant-guarded dispatch UPDATE.
	claimed, err := args.Queue.ClaimDispatchRow(ctx, tx, cand.DispatchID, args.SupervisorID)
	if err != nil {
		return acquisition{}, false, fmt.Errorf("tryAcquire: ClaimDispatchRow: %w", err)
	}
	if !claimed {
		// Defensive: under SKIP LOCKED inside one tx this should not
		// occur. If it does, treat as race-loss for this candidate.
		return acquisition{}, false, nil
	}

	// Step 3d — per-region re-evaluation under tx. Re-load existing
	// holders for the same store and re-run RegionsConflict. If a new
	// conflict surfaced, bail.
	for i, sp := range specs {
		region, ok := sp.(store.RegionLockSpec)
		if !ok {
			continue
		}
		if _, isRebind := rebound[i]; isRebind {
			continue // own row; not a conflict.
		}
		s, ok := args.StoreRegistry.GetStore(region.StoreName)
		if !ok {
			return acquisition{}, false, fmt.Errorf("tryAcquire: unknown store %q", region.StoreName)
		}
		holders, err := args.LockHolders.ListByStoreRegion(ctx, tx, region.StoreName)
		if err != nil {
			return acquisition{}, false, fmt.Errorf("tryAcquire: ListByStoreRegion: %w", err)
		}
		conflicted := false
		for _, h := range holders {
			if h.HolderNodeID == cand.NodeID && h.HolderSupervisorID == args.SupervisorID {
				continue
			}
			existingRegion, err := s.UnmarshalRegion(h.RegionData)
			if err != nil {
				return acquisition{}, false, fmt.Errorf("tryAcquire: UnmarshalRegion: %w", err)
			}
			if s.RegionsConflict(region.Region, existingRegion) {
				conflicted = true
				break
			}
		}
		if conflicted {
			return acquisition{}, false, nil
		}
	}

	// Step 3e — AcquireLock + INSERT per spec, in §13.7 sort order.
	storeCtx := store.WithTx(ctx, tx)
	acquiredLocks := make([]AcquiredLock, 0, len(specs))
	for i, sp := range specs {
		al, ok, err := acquireOneLock(storeCtx, args, tx, sp, cand, rebound[i], heartbeatInterval)
		if err != nil {
			return acquisition{}, false, err
		}
		if !ok {
			return acquisition{}, false, nil
		}
		acquiredLocks = append(acquiredLocks, al)
	}

	out := acquisition{
		DispatchID: cand.DispatchID,
		NodeID:     cand.NodeID,
		InstanceID: nd.InstanceID,
		NodeType:   nd.NodeType,
		Executor:   nd.Executor,
		FrameID:    cand.FrameID,
		Locks:      acquiredLocks,
		NodeDef:    nodeDef,
	}
	if inst != nil {
		out.InstanceParams = inst.Params
	}
	return out, true, nil
}

// acquireOneLock handles one lock spec inside the acquisition tx. For
// rebound specs it skips Store.AcquireLock and reuses the existing
// LockHolderRow's identity. For fresh specs it calls AcquireLock,
// inserts a lock-holder row, and returns the assembled AcquiredLock.
func acquireOneLock(
	storeCtx context.Context, args RunArgs, tx pgx.Tx,
	sp store.LockSpec, cand queue.Candidate,
	rebind store.LockHolderRow, heartbeatInterval time.Duration,
) (AcquiredLock, bool, error) {
	storeName := storeNameForSpec(sp)
	var s store.Store
	if storeName != "" {
		var ok bool
		s, ok = args.StoreRegistry.GetStore(storeName)
		if !ok {
			return AcquiredLock{}, false, fmt.Errorf("acquireOneLock: unknown store %q", storeName)
		}
	}
	if rebind.ID != (shared.UUID{}) {
		// Rebound — reuse the existing row's identity. ClaimResult is
		// not preserved across runs in v1 (see tasks 6/7 deviations),
		// so for claim-mode rebinds we re-derive the claim ID from the
		// row.
		al := AcquiredLock{
			Spec: sp,
			Handle: store.LockHandle{
				ID:           rebind.ID.String(),
				Kind:         string(rebind.Kind),
				StoreName:    storeName,
				HolderNodeID: rebind.HolderNodeID.String(),
				SupervisorID: args.SupervisorID,
				AcquiredAt:   rebind.ClaimedAt,
				ExpiresAt:    rebind.ExpiresAt,
			},
			Resumed: true,
			Store:   s,
		}
		if rebind.ClaimID != nil {
			al.ClaimResult.ClaimID = *rebind.ClaimID
		}
		return al, true, nil
	}

	// Fresh acquire.
	rowID := uuid.New()
	expiresAt := args.Clock.Now().Add(5 * heartbeatInterval)
	frameIDPtr := cand.FrameID
	insertInput := store.LockHolderRow{
		ID:                 rowID,
		HolderSupervisorID: args.SupervisorID,
		HolderNodeID:       cand.NodeID,
		ClaimedAt:          args.Clock.Now(),
		LastHeartbeatAt:    args.Clock.Now(),
		ExpiresAt:          expiresAt,
		FrameID:            &frameIDPtr,
	}
	handle := store.LockHandle{
		ID:           rowID.String(),
		HolderNodeID: cand.NodeID.String(),
		SupervisorID: args.SupervisorID,
		AcquiredAt:   insertInput.ClaimedAt,
		ExpiresAt:    expiresAt,
	}

	var claimResult store.ClaimResult
	switch v := sp.(type) {
	case store.NamedLockSpec:
		insertInput.Kind = store.LockHolderKindNamed
		nameCopy := v.Name
		insertInput.LockName = &nameCopy
		handle.Kind = string(store.LockHolderKindNamed)
	case store.RegionLockSpec:
		insertInput.Kind = store.LockHolderKindRegion
		nameCopy := v.StoreName
		insertInput.StoreName = &nameCopy
		regionBytes, err := json.Marshal(v.Region)
		if err != nil {
			return AcquiredLock{}, false, fmt.Errorf("acquireOneLock: marshal region: %w", err)
		}
		insertInput.RegionData = regionBytes
		handle.Kind = string(store.LockHolderKindRegion)
		handle.StoreName = v.StoreName
		// Run AcquireLock for completeness; direct-mode is a no-op.
		if _, _, err := s.AcquireLock(storeCtx, sp); err != nil {
			return AcquiredLock{}, false, fmt.Errorf("acquireOneLock: store.AcquireLock(region): %w", err)
		}
	case store.ClaimLockSpec:
		insertInput.Kind = store.LockHolderKindClaim
		nameCopy := v.StoreName
		insertInput.StoreName = &nameCopy
		handle.Kind = string(store.LockHolderKindClaim)
		handle.StoreName = v.StoreName
		_, cr, err := s.AcquireLock(storeCtx, sp)
		if err != nil {
			return AcquiredLock{}, false, fmt.Errorf("acquireOneLock: store.AcquireLock(claim): %w", err)
		}
		if cr.ClaimID == "" {
			// Pool empty by the time AcquireLock ran. Bail this candidate.
			return AcquiredLock{}, false, nil
		}
		claimResult = cr
		idCopy := cr.ClaimID
		insertInput.ClaimID = &idCopy
	default:
		return AcquiredLock{}, false, fmt.Errorf("acquireOneLock: unknown spec kind %T", sp)
	}

	if err := args.LockHolders.Insert(storeCtx, tx, insertInput); err != nil {
		return AcquiredLock{}, false, fmt.Errorf("acquireOneLock: Insert: %w", err)
	}
	return AcquiredLock{
		Spec:        sp,
		Handle:      handle,
		ClaimResult: claimResult,
		Resumed:     false,
		Store:       s,
	}, true, nil
}

// verifyBeforeRun is the §13.3 step 4 separate-read guard. The spec
// places this AFTER the acquisition tx commit so the read sees the
// post-commit `claimed_by`. If ownership has moved (orphan reaper hit
// us between commit and now, or another supervisor stole the row via
// some out-of-band path), bail.
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
// store.ReleaseLock(give_up) for each acquired lock outside any tx,
// then claimant-guarded DELETE per inserted lock-holder row, then an
// orphaned_claim_lost_race event.
func handleOrphanedClaim(ctx context.Context, args RunArgs, acq acquisition) {
	for _, lk := range acq.Locks {
		if lk.Resumed {
			continue
		}
		if lk.Store != nil {
			if err := lk.Store.ReleaseLock(ctx, lk.Handle, store.ReleaseGiveUp); err != nil {
				args.Logger.Warn("handleOrphanedClaim: ReleaseLock failed",
					"store", lk.Handle.StoreName, "error", err.Error())
			}
		}
		// Claimant-guarded delete in a fresh tx.
		_ = args.Storage.Transaction(ctx, func(ctx context.Context, tx storage.Tx) error {
			return args.Storage.LockHolders().Delete(ctx, mustParseUUID(lk.Handle.ID), args.SupervisorID, tx)
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
// Uses the storage-side state machine, which rejects non-stale starts
// (the spec's "fresh" terminology maps to this codebase's "stale" —
// the state machine's stale → running edge under reason
// dispatch_claimed). On rejection (state moved, e.g. operator
// invalidate fired) the function returns the wrapped error and the
// caller falls into the orphan handler.
func transitionToRunning(ctx context.Context, args RunArgs, acq acquisition) error {
	return args.Storage.Nodes().UpdateState(ctx, acq.NodeID,
		shared.NodeStateRunning, node.ReasonDispatchClaimed, nil)
}

// hintEligibility runs the §13.2 in-Go pre-checks. Returns false on the
// first ineligible spec; the caller bails to the next candidate.
//
// Authoritative serialisation lives in step 3 (advisory locks + region
// re-evaluation under tx + AcquireLock empty-pool detection); these
// checks are best-effort.
func hintEligibility(ctx context.Context, args RunArgs, nodeID shared.UUID, specs []store.LockSpec) (bool, error) {
	for _, sp := range specs {
		switch v := sp.(type) {
		case store.NamedLockSpec:
			cnt, err := countNamedLockHinted(ctx, args, v.Name)
			if err != nil {
				return false, err
			}
			limit := v.Limit
			if v.Mode == store.LockModeMutex {
				limit = 1
			}
			if cnt >= limit {
				return false, nil
			}
		case store.RegionLockSpec:
			s, ok := args.StoreRegistry.GetStore(v.StoreName)
			if !ok {
				return false, fmt.Errorf("hintEligibility: unknown store %q", v.StoreName)
			}
			holders, err := args.Storage.LockHolders().ListByHolderNode(ctx, nodeID, nil)
			if err != nil {
				return false, err
			}
			for _, h := range holders {
				if h.LockKind != storage.LockKindRegion {
					continue
				}
				// Skip rows owned by us against the same store: they're
				// rebind candidates handled in step 3a, not conflicts.
				if h.HolderSupervisorID == args.SupervisorID && h.StoreName != nil && *h.StoreName == v.StoreName {
					continue
				}
				existing, err := s.UnmarshalRegion(h.RegionData)
				if err != nil {
					return false, err
				}
				if s.RegionsConflict(v.Region, existing) {
					return false, nil
				}
			}
		case store.ClaimLockSpec:
			s, ok := args.StoreRegistry.GetStore(v.StoreName)
			if !ok {
				return false, fmt.Errorf("hintEligibility: unknown store %q", v.StoreName)
			}
			cs, ok := s.(store.ClaimableStore)
			if !ok {
				return false, fmt.Errorf("hintEligibility: store %q is not claimable", v.StoreName)
			}
			has, err := cs.HasClaimableItem(ctx, v.Criteria)
			if err != nil {
				return false, err
			}
			if !has {
				return false, nil
			}
		}
	}
	return true, nil
}

// countNamedLockHinted is the in-Go count read for §13.2. The
// authoritative count happens under the advisory lock in step 3b;
// this is best-effort.
func countNamedLockHinted(ctx context.Context, args RunArgs, lockName string) (int, error) {
	// Open a short read-only tx for the count helper (the helper takes
	// a tx for the locked variant; we run it without an advisory lock
	// here so the read returns instantly).
	tx, err := args.QueuePool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	return args.LockHolders.CountByNamedLock(ctx, tx, lockName)
}

// emitLockAcquired emits the per-spec lock_acquired event. Includes the
// `resumed` flag so observers can distinguish rebound rows from fresh
// ones.
func emitLockAcquired(ctx context.Context, args RunArgs, acq acquisition, lk AcquiredLock) {
	payload := map[string]any{
		"lock_kind":     lk.Handle.Kind,
		"store_name":    lk.Handle.StoreName,
		"holder_id":     lk.Handle.ID,
		"supervisor_id": args.SupervisorID,
		"resumed":       lk.Resumed,
	}
	if named, ok := lk.Spec.(store.NamedLockSpec); ok {
		payload["lock_name"] = named.Name
	}
	if lk.ClaimResult.ClaimID != "" {
		payload["claim_id"] = lk.ClaimResult.ClaimID
	}
	_ = args.Storage.Events().Append(ctx, storage.EventAppendInput{
		NodeID: &acq.NodeID, InstanceID: &acq.InstanceID,
		Kind: "lock_acquired", Payload: payload,
	}, nil)
}
