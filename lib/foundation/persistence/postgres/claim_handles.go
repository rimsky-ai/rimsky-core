// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// claim_handles.go is the postgres accessor for `rimsky_claim_handles`
// (v3 spec §12). Lifts the SQL from foundation/persistence/postgres/claim_handles.go (which
// the persistence refactor folds away — the lock-holder mechanism lives
// here now).
//
// @blessed-invariant 9a: lock state lives only in the persistence layer.
//
//	No store implementation persists lock state. The question
//	"is anyone holding lock X" is answered exclusively by the rows
//	managed in this file.
//
// @blessed-invariant 4: claimant-guarded release. Every DELETE / UPDATE
// against rimsky_claim_handles carries the claimant guard
// (`holder_supervisor_id = <supervisor>`), rendered exclusively by the
// claimantGuard helper in this file — the predicate is written exactly
// once per driver, so no mutation site can drift to an unguarded form.
// Stale orphan sweeps cannot null or delete live ownership.
//
// FrameID handling: ClaimHandleInsertInput carries an optional FrameID
// that is plumbed through into the postgres row. Per v3 spec §12, frame_id
// on rimsky_claim_handles is observability-only — no algorithm consults
// it; population is the supervisor's contract.

package postgres

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

const lockHolderCols = `
  id, lock_kind, lock_name, producer_name, claim_scope_data, address, payload, intent,
  realized_write_semantics,
  holder_supervisor_id, holder_node_id,
  claimed_at, last_heartbeat_at, expires_at, frame_id,
  node_run_id, is_held,
  parent_claim_handle_id, lifetime, version_id,
  producer_candidate_handle,
  aggregation_policy, expected_children_count,
  committed_children_count, abandoned_children_count,
  state, resolved_at
`

// claimantGuard renders the @blessed-invariant 4 ownership predicate —
// `holder_supervisor_id = $n` — as a SQL fragment. This is the single
// written site of the guard for the postgres driver: every claimant-
// guarded UPDATE / DELETE in this file and in claim_holders.go
// (FailAllActiveByClaimHandle's EXISTS sub-query) splices this fragment
// into its WHERE clause, so a wrong-supervisor mutation can never match
// a row. alias qualifies the column for statements that alias the table
// ("" for unqualified); n is the 1-based placeholder ordinal the caller
// binds the supervisor id at.
//
// Load-bearing property protected: no mutation statement may lose its
// guard — call sites must splice this fragment rather than hand-writing
// (or omitting) the predicate, even where a caller seems to guarantee
// ownership.
func claimantGuard(alias string, n int) string {
	col := "holder_supervisor_id"
	if alias != "" {
		col = alias + "." + col
	}
	return fmt.Sprintf("%s = $%d", col, n)
}

// Insert writes a new lock-holder row inside the caller-provided
// transaction. The dispatch-acquisition transaction (§7.3) holds the tx;
// every per-spec lock-holder row is inserted via this method so the row
// commits atomically with the dispatch claim.
func (s *claimHandlesImpl) Insert(ctx context.Context, in persistence.ClaimHandleInsertInput, tx persistence.Tx) error {
	now := time.Now().UTC()
	var rws *string
	if in.RealizedWriteSemantics != "" {
		v := in.RealizedWriteSemantics
		rws = &v
	}
	lifetime := in.Lifetime
	if lifetime == "" {
		lifetime = spec.ClaimLifetimeSubgraph
	}
	var candidateHandle any
	if len(in.ProducerCandidateHandle) > 0 {
		candidateHandle = in.ProducerCandidateHandle
	}
	_, err := s.q(tx).Exec(ctx,
		`INSERT INTO rimsky_claim_handles (
		   id, lock_kind, lock_name, producer_name, claim_scope_data, address, payload, intent,
		   realized_write_semantics,
		   holder_supervisor_id, holder_node_id,
		   claimed_at, last_heartbeat_at, expires_at, frame_id,
		   node_run_id, is_held,
		   parent_claim_handle_id, lifetime, producer_candidate_handle,
		   aggregation_policy
		 ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21)`,
		in.ID, string(in.LockKind),
		in.LockName, in.ProducerName,
		nullableJSONB(in.ClaimScopeData), nullableJSONB(in.Address), nullableJSONB(in.Payload),
		in.Intent,
		rws,
		in.HolderSupervisorID, in.HolderNodeID,
		now, now, in.ExpiresAt, in.FrameID,
		in.NodeRunID, in.IsHeld,
		in.ParentClaimHandleID, string(lifetime), candidateHandle,
		nullableJSONB(in.AggregationPolicy),
	)
	if err != nil {
		return fmt.Errorf("lockholders.Insert: %w", err)
	}
	return nil
}

// UpdateAddress sets the address column on an existing scope-kind row.
// Claimant-guarded on supervisorID; mismatches are a no-op (returns nil).
func (s *claimHandlesImpl) UpdateAddress(
	ctx context.Context, id shared.UUID, supervisorID string, address json.RawMessage, tx persistence.Tx,
) error {
	_, err := s.q(tx).Exec(ctx,
		`UPDATE rimsky_claim_handles
		    SET address = $1
		  WHERE id = $2 AND `+claimantGuard("", 3),
		nullableJSONB(address), id, supervisorID,
	)
	if err != nil {
		return fmt.Errorf("lockholders.UpdateAddress: %w", err)
	}
	return nil
}

// UpdatePayload sets the payload column on an existing scope-kind row.
// Claimant-guarded on supervisorID; mismatches are a no-op (returns nil).
func (s *claimHandlesImpl) UpdatePayload(
	ctx context.Context, id shared.UUID, supervisorID string, payload json.RawMessage, tx persistence.Tx,
) error {
	_, err := s.q(tx).Exec(ctx,
		`UPDATE rimsky_claim_handles
		    SET payload = $1
		  WHERE id = $2 AND `+claimantGuard("", 3),
		nullableJSONB(payload), id, supervisorID,
	)
	if err != nil {
		return fmt.Errorf("lockholders.UpdatePayload: %w", err)
	}
	return nil
}

// UpdateRealizedWriteSemantics sets the realized_write_semantics column
// on an existing scope-kind row. Claimant-guarded on supervisorID;
// mismatches are a no-op (returns nil). Called by the supervisor's
// acquireClaim path after Open returns its per-claim verdict.
func (s *claimHandlesImpl) UpdateRealizedWriteSemantics(
	ctx context.Context, id shared.UUID, supervisorID string, ws string, tx persistence.Tx,
) error {
	var v *string
	if ws != "" {
		s := ws
		v = &s
	}
	_, err := s.q(tx).Exec(ctx,
		`UPDATE rimsky_claim_handles
		    SET realized_write_semantics = $1
		  WHERE id = $2 AND `+claimantGuard("", 3),
		v, id, supervisorID,
	)
	if err != nil {
		return fmt.Errorf("lockholders.UpdateRealizedWriteSemantics: %w", err)
	}
	return nil
}

// UpdateClaimScope sets the claim_scope_data column on an existing
// claim-scope-kind row. Claimant-guarded on supervisorID; mismatches
// are a no-op (returns nil).
func (s *claimHandlesImpl) UpdateClaimScope(
	ctx context.Context, id shared.UUID, supervisorID string, scope json.RawMessage, tx persistence.Tx,
) error {
	_, err := s.q(tx).Exec(ctx,
		`UPDATE rimsky_claim_handles
		    SET claim_scope_data = $1
		  WHERE id = $2 AND `+claimantGuard("", 3),
		nullableJSONB(scope), id, supervisorID,
	)
	if err != nil {
		return fmt.Errorf("lockholders.UpdateClaimScope: %w", err)
	}
	return nil
}

// UpdateNodeRunID repoints the node_run_id FK on an existing claim_handle
// row. NOT claimant-guarded: the fan-out dispatch path calls it inside the
// same tx as the child-run INSERT (before any other supervisor can observe
// the sub-claim), retargeting the sub-claim from the parent fan-out run to
// its own child leaf run so the leaf can resolve its candidate handle by
// `node_run_id = its own dispatch id` (E4).
func (s *claimHandlesImpl) UpdateNodeRunID(
	ctx context.Context, id shared.UUID, nodeRunID shared.UUID, tx persistence.Tx,
) error {
	_, err := s.q(tx).Exec(ctx,
		`UPDATE rimsky_claim_handles
		    SET node_run_id = $1
		  WHERE id = $2`,
		nodeRunID, id,
	)
	if err != nil {
		return fmt.Errorf("lockholders.UpdateNodeRunID: %w", err)
	}
	return nil
}

// ReassignHolderSupervisor CAS-moves an ACTIVE row's
// holder_supervisor_id from fromSupervisorID to toSupervisorID. The
// cross-supervisor claim-handoff primitive (leaf-acquisition restamp +
// settlement takeover); see the interface doc on
// persistence.ClaimHandleTable for the call sites and the guard
// rationale. Affected-rows = 0 (state not active, or the observed
// holder lost a race) returns spec.ErrIllegalClaimHandleTransition.
func (s *claimHandlesImpl) ReassignHolderSupervisor(
	ctx context.Context, id shared.UUID, fromSupervisorID, toSupervisorID string, tx persistence.Tx,
) error {
	if toSupervisorID == "" {
		// @constraint: active rows must carry a holder (migration-009 CHECK pair); an
		// empty target would be a disguised release.
		return fmt.Errorf("lockholders.ReassignHolderSupervisor: empty toSupervisorID")
	}
	cmd, err := s.q(tx).Exec(ctx,
		`UPDATE rimsky_claim_handles
		    SET holder_supervisor_id = $1
		  WHERE id = $2
		    AND state = 'active'
		    AND `+claimantGuard("", 3),
		toSupervisorID, id, fromSupervisorID,
	)
	if err != nil {
		return fmt.Errorf("lockholders.ReassignHolderSupervisor: %w", err)
	}
	if cmd.RowsAffected() == 0 {
		return spec.ErrIllegalClaimHandleTransition
	}
	return nil
}

// Get returns the row identified by id, or (nil, nil) when no row exists.
func (s *claimHandlesImpl) Get(ctx context.Context, id shared.UUID, tx persistence.Tx) (*persistence.ClaimHandleRow, error) {
	row := s.q(tx).QueryRow(ctx,
		`SELECT `+lockHolderCols+` FROM rimsky_claim_handles WHERE id = $1`, id,
	)
	out, err := scanClaimHandle(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &out, nil
}

// LockForUpdate runs SELECT ... FOR UPDATE on the lock-holder row. Used
// by runtime/auto_terminal.go to serialize auto-terminal
// resolution per @blessed-invariant 13.
func (s *claimHandlesImpl) LockForUpdate(ctx context.Context, id shared.UUID, tx persistence.Tx) (*persistence.ClaimHandleRow, error) {
	row := s.q(tx).QueryRow(ctx,
		`SELECT `+lockHolderCols+` FROM rimsky_claim_handles WHERE id = $1 FOR UPDATE`, id,
	)
	out, err := scanClaimHandle(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("lockholders.LockForUpdate: %w", err)
	}
	return &out, nil
}

// ListByHolderNode returns every row anchored to holderNodeID.
func (s *claimHandlesImpl) ListByHolderNode(ctx context.Context, holderNodeID shared.UUID, tx persistence.Tx) ([]persistence.ClaimHandleRow, error) {
	rows, err := s.q(tx).Query(ctx,
		`SELECT `+lockHolderCols+` FROM rimsky_claim_handles
		 WHERE holder_node_id = $1
		 ORDER BY claimed_at ASC`, holderNodeID,
	)
	if err != nil {
		return nil, fmt.Errorf("lockholders.ListByHolderNode: %w", err)
	}
	defer rows.Close()
	return collectClaimHandles(rows)
}

func (s *claimHandlesImpl) ListByNodeRun(ctx context.Context, nodeRunID shared.UUID, tx persistence.Tx) ([]persistence.ClaimHandleRow, error) {
	rows, err := s.q(tx).Query(ctx,
		`SELECT `+lockHolderCols+` FROM rimsky_claim_handles
		 WHERE node_run_id = $1
		 ORDER BY claimed_at ASC`, nodeRunID,
	)
	if err != nil {
		return nil, fmt.Errorf("lockholders.ListByNodeRun: %w", err)
	}
	defer rows.Close()
	return collectClaimHandles(rows)
}

// GetByFrameAndNode returns the lock-holder row for (nodeID, frameID),
// or (nil, nil) when no matching row exists. Used by the observability
// dispatch-detail endpoint to follow dispatch → claim_id directly.
func (s *claimHandlesImpl) GetByFrameAndNode(ctx context.Context, nodeID shared.UUID, frameID shared.UUID, tx persistence.Tx) (*persistence.ClaimHandleRow, error) {
	row := s.q(tx).QueryRow(ctx,
		`SELECT `+lockHolderCols+` FROM rimsky_claim_handles
		 WHERE holder_node_id = $1 AND frame_id = $2
		 LIMIT 1`,
		nodeID, frameID,
	)
	out, err := scanClaimHandle(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("lockholders.GetByFrameAndNode: %w", err)
	}
	return &out, nil
}

// ListChildClaimHandles returns every claim handle whose
// parent_claim_handle_id equals parentID. Used by the recursive
// claim-tree resolution path (spec §Recursive claim-tree resolution).
func (s *claimHandlesImpl) ListChildClaimHandles(ctx context.Context, parentID shared.UUID, tx persistence.Tx) ([]persistence.ClaimHandleRow, error) {
	rows, err := s.q(tx).Query(ctx,
		`SELECT `+lockHolderCols+` FROM rimsky_claim_handles
		 WHERE parent_claim_handle_id = $1`,
		parentID)
	if err != nil {
		return nil, fmt.Errorf("lockholders.ListChildClaimHandles: %w", err)
	}
	defer rows.Close()
	return collectClaimHandles(rows)
}

// DeleteResolvedOlderThan deletes terminal claim_handle rows past the
// retention cutoff, excluding committed-durable rows (asset surface).
// Absence-guarded; serialized across replicas via the scheduler-tick
// advisory lock at the caller site.
//
// @blessed-invariant 4 (post-refactor): non-active-row deletions are
// guarded by absence + the row-discovery query filter.
// @concept: claim-handle
// @concept: claim-lifetime
func (s *claimHandlesImpl) DeleteResolvedOlderThan(
	ctx context.Context, cutoff time.Time,
) (int, error) {
	tag, err := (*tablesImpl)(s).pool.Exec(ctx,
		`DELETE FROM rimsky_claim_handles
		  WHERE state IN ('committed', 'abandoned')
		    AND (state = 'abandoned' OR lifetime = 'subgraph')
		    AND resolved_at < $1
		    AND holder_supervisor_id IS NULL`,
		cutoff)
	if err != nil {
		return 0, fmt.Errorf("postgres.ClaimHandles.DeleteResolvedOlderThan: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// DeleteResolved deletes a non-active claim_handle row. Absence-
// guarded — the post-Stage-4 CHECK constraint nulls
// `holder_supervisor_id` whenever `state` exits `'active'`, so the
// IS-NULL clause is structurally satisfied for every non-active row.
// Returns spec.ErrIllegalClaimHandleTransition on affected-rows = 0
// (the row was still active, the predicate didn't match).
//
// @blessed-invariant 4 (post-refactor): non-active-row deletions are
// guarded by absence + the row-discovery query filter.
// @concept: claim-handle
func (s *claimHandlesImpl) DeleteResolved(
	ctx context.Context, id shared.UUID, tx persistence.Tx,
) error {
	cmd, err := s.q(tx).Exec(ctx,
		`DELETE FROM rimsky_claim_handles
		  WHERE id = $1
		    AND state IN ('committed', 'abandoned')
		    AND holder_supervisor_id IS NULL`, id)
	if err != nil {
		return fmt.Errorf("lockholders.DeleteResolved: %w", err)
	}
	if cmd.RowsAffected() == 0 {
		return spec.ErrIllegalClaimHandleTransition
	}
	return nil
}

// Promote transitions a claim handle from active to committed or
// abandoned. Claimant-guarded against the supervisor that holds the
// row. Sets state, nulls holder_supervisor_id, and sets resolved_at in
// a single statement. Returns spec.ErrIllegalClaimHandleTransition on
// affected-rows = 0 (the row was not active or the supervisor
// mismatched).
//
// @blessed-invariant 4 (post-refactor): active-row mutations are
// claimant-guarded.
// @concept: claim-handle
func (s *claimHandlesImpl) Promote(
	ctx context.Context, id shared.UUID, supervisorID string,
	newState spec.ClaimHandleState, tx persistence.Tx,
) error {
	cmd, err := s.q(tx).Exec(ctx,
		`UPDATE rimsky_claim_handles
		    SET state = $3,
		        holder_supervisor_id = NULL,
		        resolved_at = now()
		  WHERE id = $1
		    AND state = 'active'
		    AND `+claimantGuard("", 2),
		id, supervisorID, string(newState))
	if err != nil {
		return fmt.Errorf("lockholders.Promote: %w", err)
	}
	if cmd.RowsAffected() == 0 {
		return spec.ErrIllegalClaimHandleTransition
	}
	return nil
}

// ListByState returns claim-handle rows currently in the given state.
// Used by the retention sweep (state ∈ {committed, abandoned}) and by
// readers that need state-filtered listings.
func (s *claimHandlesImpl) ListByState(
	ctx context.Context, state spec.ClaimHandleState, tx persistence.Tx,
) ([]persistence.ClaimHandleRow, error) {
	rows, err := s.q(tx).Query(ctx,
		`SELECT `+lockHolderCols+` FROM rimsky_claim_handles
		 WHERE state = $1
		 ORDER BY claimed_at ASC`, string(state))
	if err != nil {
		return nil, fmt.Errorf("lockholders.ListByState: %w", err)
	}
	defer rows.Close()
	return collectClaimHandles(rows)
}

// ListByInstanceAndState returns claim-handle rows joined through
// holder_node_id → rimsky_nodes filtered by instance + state +
// lifetime. The asset query calls
// `ListByInstanceAndState(instance, committed, durable)`.
//
// Column qualification: every column selected MUST be prefixed `ch.`
// because the JOIN against `rimsky_nodes n` introduces the `id` column
// from both tables.
func (s *claimHandlesImpl) ListByInstanceAndState(
	ctx context.Context, instanceID shared.UUID,
	state spec.ClaimHandleState, lifetime spec.ClaimLifetime, tx persistence.Tx,
) ([]persistence.ClaimHandleRow, error) {
	rows, err := s.q(tx).Query(ctx,
		`SELECT `+qualifiedLockHolderCols("ch")+`
		   FROM rimsky_claim_handles ch
		   JOIN rimsky_nodes n ON n.id = ch.holder_node_id
		  WHERE n.instance_id = $1
		    AND ch.state = $2
		    AND ch.lifetime = $3`,
		instanceID, string(state), string(lifetime))
	if err != nil {
		return nil, fmt.Errorf("lockholders.ListByInstanceAndState: %w", err)
	}
	defer rows.Close()
	return collectClaimHandles(rows)
}

// SetVersionID persists the producer-returned canonical version_id
// claimant-guarded. Inert in rimsky (@blessed-invariant 20-class).
func (s *claimHandlesImpl) SetVersionID(
	ctx context.Context, id shared.UUID, supervisorID string, versionID string, tx persistence.Tx,
) error {
	var v *string
	if versionID != "" {
		v = &versionID
	}
	_, err := s.q(tx).Exec(ctx,
		`UPDATE rimsky_claim_handles SET version_id = $1
		 WHERE id = $2 AND `+claimantGuard("", 3),
		v, id, supervisorID)
	if err != nil {
		return fmt.Errorf("lockholders.SetVersionID: %w", err)
	}
	return nil
}

// qualifiedLockHolderCols returns the lock-holder column list prefixed
// with the given alias. Used by JOIN queries (e.g.,
// `ListByInstanceAndState`) where unqualified column names would be
// ambiguous against the joined table's columns.
func qualifiedLockHolderCols(alias string) string {
	return alias + `.id, ` + alias + `.lock_kind, ` + alias + `.lock_name, ` +
		alias + `.producer_name, ` + alias + `.claim_scope_data, ` + alias + `.address, ` +
		alias + `.payload, ` +
		alias + `.intent, ` + alias + `.realized_write_semantics, ` +
		alias + `.holder_supervisor_id, ` + alias + `.holder_node_id, ` +
		alias + `.claimed_at, ` + alias + `.last_heartbeat_at, ` + alias + `.expires_at, ` +
		alias + `.frame_id, ` + alias + `.node_run_id, ` + alias + `.is_held, ` +
		alias + `.parent_claim_handle_id, ` + alias + `.lifetime, ` +
		alias + `.version_id, ` +
		alias + `.producer_candidate_handle, ` +
		alias + `.aggregation_policy, ` + alias + `.expected_children_count, ` +
		alias + `.committed_children_count, ` + alias + `.abandoned_children_count, ` +
		alias + `.state, ` + alias + `.resolved_at`
}

// ExtendHeartbeat updates last_heartbeat_at and expires_at for every row
// owned by supervisorID whose lifetime should currently be active. Per
// spec §7.5; the heartbeat keeps lock-holder rows alive against the
// orphan reaper. Post-stage-3 / stage-5 the "running for this supervisor"
// predicate sources from `rimsky_node_runs` (state + claimed_by); holder
// membership joins through `rimsky_claim_holders.holder_run_id` →
// `rimsky_node_runs`.
func (s *claimHandlesImpl) ExtendHeartbeat(ctx context.Context, supervisorID string, expiresAt time.Time, tx persistence.Tx) error {
	heartbeatSeconds := int(time.Until(expiresAt).Seconds())
	if heartbeatSeconds < 1 {
		heartbeatSeconds = 1
	}
	// @deliberate: non-active rows are excluded from the heartbeat: committed +
	// abandoned rows have `holder_supervisor_id IS NULL` and unbounded
	// lifetime (released only by the retention sweep or, for committed-
	// durable, by `ReleaseHeldDurableClaims`), so they do not need
	// `expires_at` refresh.
	_, err := s.q(tx).Exec(ctx,
		`UPDATE rimsky_claim_handles lh
		   SET last_heartbeat_at = now(),
		       expires_at = now() + ($2 * interval '1 second')
		 WHERE `+claimantGuard("lh", 1)+`
		   AND lh.state = 'active'
		   AND (
		        EXISTS (
		            SELECT 1 FROM rimsky_node_runs r
		             WHERE r.node_id = lh.holder_node_id
		               AND r.claimed_by = $1
		               AND r.state = 'running'
		        )
		        OR EXISTS (
		            SELECT 1 FROM rimsky_claim_holders ch
		              JOIN rimsky_node_runs r ON r.id = ch.holder_run_id
		             WHERE ch.claim_handle_id = lh.id
		               AND ch.state = 'active'
		               AND r.state = 'running'
		        )
		   )`,
		supervisorID, heartbeatSeconds,
	)
	if err != nil {
		return fmt.Errorf("lockholders.ExtendHeartbeat: %w", err)
	}
	return nil
}

// ListExpired returns active rows whose `expires_at < now()`. The
// scheduler's orphan-reap sweep iterates these.
//
// Predicate: `state = 'active' AND expires_at < now()`. Committed /
// abandoned rows are owned by the retention sweep, not the orphan
// reaper. See @blessed-invariant 22 (held-durable persistence) for
// the higher-level discipline; the column-level predicate just enforces
// it.
//
// @concept: orphan-reaper
func (s *claimHandlesImpl) ListExpired(ctx context.Context, tx persistence.Tx) ([]persistence.ClaimHandleRow, error) {
	rows, err := s.q(tx).Query(ctx,
		`SELECT `+lockHolderCols+` FROM rimsky_claim_handles
		 WHERE state = 'active' AND expires_at < now()
		 ORDER BY expires_at ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("lockholders.ListExpired: %w", err)
	}
	defer rows.Close()
	return collectClaimHandles(rows)
}

// Delete removes the row keyed by id. Claimant-guarded on
// expectedSupervisorID; mismatch is a no-op (returns nil).
func (s *claimHandlesImpl) Delete(ctx context.Context, id shared.UUID, expectedSupervisorID string, tx persistence.Tx) error {
	_, err := s.q(tx).Exec(ctx,
		`DELETE FROM rimsky_claim_handles
		 WHERE id = $1 AND `+claimantGuard("", 2),
		id, expectedSupervisorID,
	)
	if err != nil {
		return fmt.Errorf("lockholders.Delete: %w", err)
	}
	return nil
}

// CountByNamedLock returns the number of currently-held named-lock
// rows. Used inside the supervisor's acquisition tx after taking
// pg_advisory_xact_lock(hashtext('rimsky_lock:'||lockName)) to enforce
// the named-lock counting-mode limit.
//
// Post-Stage-2 of the claim-handle state-column refactor: counts
// state='active' rows only. Committed / abandoned named-lock rows are
// no longer held; the retention sweep reaps them in due course but
// they MUST NOT count against the named-lock limit (the lock was
// released at terminal).
func (s *claimHandlesImpl) CountByNamedLock(ctx context.Context, lockName string, tx persistence.Tx) (int, error) {
	var n int
	err := s.q(tx).QueryRow(ctx,
		`SELECT count(*) FROM rimsky_claim_handles
		 WHERE lock_kind = 'named' AND lock_name = $1
		   AND state = 'active'`,
		lockName,
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("lockholders.CountByNamedLock: %w", err)
	}
	return n, nil
}

// ListByProducerClaimScope returns every claim-scope-kind row for producerName
// that is currently in conflict-detection scope: active OR
// committed-durable (the asset surface; the durable holder still
// occupies the claim-scope until producer Release). Used by the
// supervisor's claim-scope-conflict re-check (§7.3 step 4a/4b).
//
// Predicate: `state = 'active' OR (state = 'committed' AND lifetime =
// 'durable')`. Subgraph rows transition to state='committed' at
// terminal but no longer occupy the claim-scope (the producer released its
// hold on Commit; only the rimsky-side ledger row lingers for forensics
// until the retention sweep reaps it). Abandoned rows are not in the
// conflict set either — the producer Abandon released the claim-scope.
//
// @blessed-invariant 4b (single-writer-per-claim-scope)
// @concept: claim-handle
// @concept: asset
func (s *claimHandlesImpl) ListByProducerClaimScope(ctx context.Context, producerName string, tx persistence.Tx) ([]persistence.ClaimHandleRow, error) {
	rows, err := s.q(tx).Query(ctx,
		`SELECT `+lockHolderCols+` FROM rimsky_claim_handles
		 WHERE lock_kind = 'claim_scope' AND producer_name = $1
		   AND (state = 'active' OR (state = 'committed' AND lifetime = 'durable'))
		 ORDER BY claimed_at ASC`,
		producerName,
	)
	if err != nil {
		return nil, fmt.Errorf("lockholders.ListByProducerClaimScope: %w", err)
	}
	defer rows.Close()
	return collectClaimHandles(rows)
}

// DeleteIfExpired removes the row keyed by id, claimant-guarded on
// supervisor_id AND only when expires_at is still in the past AND only
// when state = 'active'. Used by the orphan reaper. Returns
// deleted=true on success; false on no-op (claimant mismatch, fresh
// heartbeat extended the row, or the row promoted to a terminal state
// between ListExpired and this DELETE — racing with `Promote` fired
// by the auto-terminal Commit/Abandon path).
//
// The `state = 'active'` clause is defense in depth: `Promote` nulls
// `holder_supervisor_id` atomically with the state transition (the
// post-Stage-4 CHECK constraint enforces this), so the claimant-guard
// alone would reject the DELETE on a freshly-promoted row; the
// explicit state gate closes the ListExpired → race → DELETE window
// regardless.
//
// @blessed-invariant 4 (post-refactor): active-row mutations are
// claimant-guarded.
// @concept: orphan-reaper
func (s *claimHandlesImpl) DeleteIfExpired(ctx context.Context, id shared.UUID, supervisorID string, tx persistence.Tx) (bool, error) {
	tag, err := s.q(tx).Exec(ctx,
		`DELETE FROM rimsky_claim_handles
		 WHERE id = $1
		   AND `+claimantGuard("", 2)+`
		   AND expires_at < now()
		   AND state = 'active'`,
		id, supervisorID,
	)
	if err != nil {
		return false, fmt.Errorf("lockholders.DeleteIfExpired: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// ListForObservability returns lock-holder rows matching filter,
// cursor-paginated by claimed_at DESC. Used by the observability
// /v1/observability/lock-holders endpoint (spec §1.2.4). The filter
// supports the spec's documented surface plus the previously
// per-method options (holder_node, holder_supervisor) so a single
// generic browse endpoint can replace the prior per-anchor methods.
func (s *claimHandlesImpl) ListForObservability(ctx context.Context, filter persistence.LockHolderListFilter, pag persistence.ListPagination, tx persistence.Tx) (persistence.PaginatedListResult[persistence.ClaimHandleRow], error) {
	limit := pag.Limit
	if limit <= 0 {
		limit = 50
	}
	var producerArg, supArg, nodeTypeArg any
	var nodeArg, instArg any
	if filter.ProducerName != "" {
		producerArg = filter.ProducerName
	}
	if filter.HolderSupervisor != "" {
		supArg = filter.HolderSupervisor
	}
	if filter.HolderNodeID != nil {
		nodeArg = *filter.HolderNodeID
	}
	if filter.InstanceID != nil {
		instArg = *filter.InstanceID
	}
	if filter.NodeType != "" {
		nodeTypeArg = filter.NodeType
	}
	var cursorClaimed *time.Time
	var cursorID *shared.UUID
	if pag.Cursor != "" {
		c, id, err := decodeLockHolderCursor(pag.Cursor)
		if err != nil {
			return persistence.PaginatedListResult[persistence.ClaimHandleRow]{}, fmt.Errorf("lockholders.list: bad cursor: %w", err)
		}
		cursorClaimed = &c
		cursorID = &id
	}
	var cArg, cIDArg any
	if cursorClaimed != nil {
		cArg = *cursorClaimed
		cIDArg = *cursorID
	}
	rows, err := s.q(tx).Query(ctx,
		`SELECT `+lockHolderCols+`
		   FROM rimsky_claim_handles lh
		  WHERE ($1::text IS NULL OR lh.producer_name = $1)
		    AND ($2::text IS NULL OR lh.holder_supervisor_id = $2)
		    AND ($3::uuid IS NULL OR lh.holder_node_id = $3)
		    AND (
		         $4::uuid IS NULL OR EXISTS (
		           SELECT 1 FROM rimsky_nodes n
		            WHERE n.id = lh.holder_node_id
		              AND n.instance_id = $4
		         )
		    )
		    AND (
		         $5::text IS NULL OR EXISTS (
		           SELECT 1 FROM rimsky_nodes n
		            WHERE n.id = lh.holder_node_id
		              AND n.node_type = $5
		         )
		    )
		    AND ($6::timestamptz IS NULL OR (lh.claimed_at, lh.id) < ($6, $7))
		  ORDER BY lh.claimed_at DESC, lh.id DESC
		  LIMIT $8`,
		producerArg, supArg, nodeArg, instArg, nodeTypeArg, cArg, cIDArg, limit,
	)
	if err != nil {
		return persistence.PaginatedListResult[persistence.ClaimHandleRow]{}, fmt.Errorf("lockholders.list: %w", err)
	}
	defer rows.Close()
	out, err := collectClaimHandles(rows)
	if err != nil {
		return persistence.PaginatedListResult[persistence.ClaimHandleRow]{}, err
	}
	var nextCursor string
	if len(out) == limit && len(out) > 0 {
		last := out[len(out)-1]
		nextCursor = encodeLockHolderCursor(last.ClaimedAt, last.ID)
	}
	return persistence.PaginatedListResult[persistence.ClaimHandleRow]{Rows: out, NextCursor: nextCursor}, nil
}

type lockHolderCursor struct {
	C time.Time   `json:"c"`
	I shared.UUID `json:"i"`
}

func encodeLockHolderCursor(claimed time.Time, id shared.UUID) string {
	b, _ := json.Marshal(lockHolderCursor{C: claimed, I: id})
	return base64.StdEncoding.EncodeToString(b)
}

func decodeLockHolderCursor(s string) (time.Time, shared.UUID, error) {
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return time.Time{}, shared.UUID{}, err
	}
	var c lockHolderCursor
	if err := json.Unmarshal(raw, &c); err != nil {
		return time.Time{}, shared.UUID{}, err
	}
	return c.C, c.I, nil
}

// SetAggregationPolicy writes the parent-claim aggregation policy
// snapshot on a claim_handle row. Claimant-guarded on supervisorID;
// mismatches no-op.
func (s *claimHandlesImpl) SetAggregationPolicy(
	ctx context.Context, id shared.UUID, supervisorID string, policy json.RawMessage, tx persistence.Tx,
) error {
	_, err := s.q(tx).Exec(ctx,
		`UPDATE rimsky_claim_handles
		    SET aggregation_policy = $1
		  WHERE id = $2 AND `+claimantGuard("", 3),
		nullableJSONB(policy), id, supervisorID,
	)
	if err != nil {
		return fmt.Errorf("lockholders.SetAggregationPolicy: %w", err)
	}
	return nil
}

// BumpExpectedChildrenCount adds delta to the parent's
// `expected_children_count`. Used by `runtime/runner_subclaim.go` per
// sub-claim INSERT. Claimant-guarded on supervisorID; mismatches no-op.
func (s *claimHandlesImpl) BumpExpectedChildrenCount(
	ctx context.Context, id shared.UUID, supervisorID string, delta int, tx persistence.Tx,
) error {
	_, err := s.q(tx).Exec(ctx,
		`UPDATE rimsky_claim_handles
		    SET expected_children_count = expected_children_count + $1
		  WHERE id = $2 AND `+claimantGuard("", 3),
		delta, id, supervisorID,
	)
	if err != nil {
		return fmt.Errorf("lockholders.BumpExpectedChildrenCount: %w", err)
	}
	return nil
}

// BumpChildOutcomeCount adds delta to either
// `committed_children_count` (outcome="commit") or
// `abandoned_children_count` (outcome="abandon"). Used by the recursive
// walker (`runtime/child_execution.go::SettleChildren`) before
// firing the parent's terminal verb. Claimant-guarded on supervisorID.
func (s *claimHandlesImpl) BumpChildOutcomeCount(
	ctx context.Context, id shared.UUID, supervisorID string, outcome string, delta int, tx persistence.Tx,
) error {
	var column string
	switch outcome {
	case "commit":
		column = "committed_children_count"
	case "abandon":
		column = "abandoned_children_count"
	default:
		return fmt.Errorf("lockholders.BumpChildOutcomeCount: unknown outcome %q (want commit|abandon)", outcome)
	}
	_, err := s.q(tx).Exec(ctx,
		`UPDATE rimsky_claim_handles
		    SET `+column+` = `+column+` + $1
		  WHERE id = $2 AND `+claimantGuard("", 3),
		delta, id, supervisorID,
	)
	if err != nil {
		return fmt.Errorf("lockholders.BumpChildOutcomeCount: %w", err)
	}
	return nil
}

func scanClaimHandle(sc scannable) (persistence.ClaimHandleRow, error) {
	var (
		r                  persistence.ClaimHandleRow
		kind               string
		lockName           *string
		producerName       *string
		scopeData          []byte
		address            []byte
		payload            []byte
		intent             *string
		rws                *string
		holderSupervisorID *string
		frameID            *shared.UUID
		workerRequestID    *shared.UUID
		isHeld             bool
		parentClaimID      *shared.UUID
		lifetime           *string
		versionID          *string
		candidateHandle    []byte
		aggregation        []byte
		expectedChildren   int
		committed          int
		abandoned          int
		stateStr           string
		resolvedAt         *time.Time
	)
	if err := sc.Scan(
		&r.ID, &kind,
		&lockName, &producerName, &scopeData, &address, &payload, &intent,
		&rws,
		&holderSupervisorID, &r.HolderNodeID,
		&r.ClaimedAt, &r.LastHeartbeatAt, &r.ExpiresAt, &frameID,
		&workerRequestID, &isHeld,
		&parentClaimID, &lifetime, &versionID,
		&candidateHandle,
		&aggregation, &expectedChildren,
		&committed, &abandoned,
		&stateStr, &resolvedAt,
	); err != nil {
		return persistence.ClaimHandleRow{}, err
	}
	r.LockKind = persistence.LockKind(kind)
	r.LockName = lockName
	r.ProducerName = producerName
	r.ClaimScopeData = scopeData
	r.Address = address
	r.Payload = payload
	r.Intent = intent
	if rws != nil {
		r.RealizedWriteSemantics = *rws
	}
	// @constraint: HolderSupervisorID is nullable: non-active rows always carry NULL
	// per the `rimsky_claim_handles_inactive_has_no_holder` CHECK. The
	// `*string` mirrors the column nullability so callers cannot
	// inadvertently compare against the zero value (empty string) and
	// match a NULL row that should never participate in the
	// claimant-guarded delete (`@blessed-invariant 4`).
	r.HolderSupervisorID = holderSupervisorID
	r.FrameID = frameID
	r.NodeRunID = workerRequestID
	r.IsHeld = isHeld
	r.ParentClaimHandleID = parentClaimID
	if lifetime != nil {
		r.Lifetime = spec.ClaimLifetime(*lifetime)
	}
	if versionID != nil {
		r.VersionID = *versionID
	}
	r.ProducerCandidateHandle = candidateHandle
	r.AggregationPolicy = aggregation
	r.ExpectedChildrenCount = expectedChildren
	r.CommittedChildrenCount = committed
	r.AbandonedChildrenCount = abandoned
	r.State = spec.ClaimHandleState(stateStr)
	r.ResolvedAt = resolvedAt
	return r, nil
}

func collectClaimHandles(rows pgx.Rows) ([]persistence.ClaimHandleRow, error) {
	var out []persistence.ClaimHandleRow
	for rows.Next() {
		r, err := scanClaimHandle(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// nullableJSONB returns nil for an empty/nil RawMessage so the JSONB
// column gets a SQL NULL rather than the literal string "null".
func nullableJSONB(b json.RawMessage) any {
	if len(b) == 0 {
		return nil
	}
	return []byte(b)
}

// nullableBytes is the []byte twin of nullableJSONB — callers that
// already hold a []byte slice (e.g. json.Marshal output) avoid an
// extra conversion to RawMessage.
func nullableBytes(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}
