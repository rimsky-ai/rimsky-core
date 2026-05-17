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
// against rimsky_claim_handles carries `AND holder_supervisor_id =
// supervisor_id`. Stale orphan sweeps cannot null or delete live ownership.
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

	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/foundation/shared"
)

const lockHolderCols = `
  id, lock_kind, lock_name, producer_name, scope_data, address, intent,
  realized_write_semantics,
  holder_supervisor_id, holder_node_id,
  claimed_at, last_heartbeat_at, expires_at, frame_id,
  node_run_id, is_held,
  parent_claim_handle_id, lifetime, held_durable, version_id,
  producer_candidate_handle,
  aggregation_policy, expected_children_count,
  committed_children_count, abandoned_children_count
`

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
		lifetime = "subgraph"
	}
	var candidateHandle any
	if len(in.ProducerCandidateHandle) > 0 {
		candidateHandle = in.ProducerCandidateHandle
	}
	_, err := s.q(tx).Exec(ctx,
		`INSERT INTO rimsky_claim_handles (
		   id, lock_kind, lock_name, producer_name, scope_data, address, intent,
		   realized_write_semantics,
		   holder_supervisor_id, holder_node_id,
		   claimed_at, last_heartbeat_at, expires_at, frame_id,
		   node_run_id, is_held,
		   parent_claim_handle_id, lifetime, producer_candidate_handle,
		   aggregation_policy
		 ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20)`,
		in.ID, string(in.LockKind),
		in.LockName, in.ProducerName,
		nullableJSONB(in.ScopeData), nullableJSONB(in.Address),
		in.Intent,
		rws,
		in.HolderSupervisorID, in.HolderNodeID,
		now, now, in.ExpiresAt, in.FrameID,
		in.NodeRunID, in.IsHeld,
		in.ParentClaimHandleID, lifetime, candidateHandle,
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
		  WHERE id = $2 AND holder_supervisor_id = $3`,
		nullableJSONB(address), id, supervisorID,
	)
	if err != nil {
		return fmt.Errorf("lockholders.UpdateAddress: %w", err)
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
		  WHERE id = $2 AND holder_supervisor_id = $3`,
		v, id, supervisorID,
	)
	if err != nil {
		return fmt.Errorf("lockholders.UpdateRealizedWriteSemantics: %w", err)
	}
	return nil
}

// UpdateScope sets the scope_data column on an existing scope-kind
// row. Claimant-guarded on supervisorID; mismatches are a no-op
// (returns nil).
func (s *claimHandlesImpl) UpdateScope(
	ctx context.Context, id shared.UUID, supervisorID string, scope json.RawMessage, tx persistence.Tx,
) error {
	_, err := s.q(tx).Exec(ctx,
		`UPDATE rimsky_claim_handles
		    SET scope_data = $1
		  WHERE id = $2 AND holder_supervisor_id = $3`,
		nullableJSONB(scope), id, supervisorID,
	)
	if err != nil {
		return fmt.Errorf("lockholders.UpdateScope: %w", err)
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
// by foundation/integration/auto_terminal.go to serialize auto-terminal
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

// SetHeldDurable flips the held_durable column claimant-guarded.
func (s *claimHandlesImpl) SetHeldDurable(
	ctx context.Context, id shared.UUID, supervisorID string, heldDurable bool, tx persistence.Tx,
) error {
	_, err := s.q(tx).Exec(ctx,
		`UPDATE rimsky_claim_handles SET held_durable = $1
		 WHERE id = $2 AND holder_supervisor_id = $3`,
		heldDurable, id, supervisorID)
	if err != nil {
		return fmt.Errorf("lockholders.SetHeldDurable: %w", err)
	}
	return nil
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
		 WHERE id = $2 AND holder_supervisor_id = $3`,
		v, id, supervisorID)
	if err != nil {
		return fmt.Errorf("lockholders.SetVersionID: %w", err)
	}
	return nil
}

// ListHeldDurableByInstance returns claim_handles rows held-durable for
// the given instance. Used by the instance-termination cleanup path
// (`runtime/instance_termination.go`).
//
// Column qualification: every column selected MUST be prefixed `ch.`
// because the JOIN against `rimsky_nodes n` introduces the `id` column
// from both tables; an unqualified `id` in the SELECT raises
// "column reference id is ambiguous" at execution time.
func (s *claimHandlesImpl) ListHeldDurableByInstance(
	ctx context.Context, instanceID shared.UUID, tx persistence.Tx,
) ([]persistence.ClaimHandleRow, error) {
	rows, err := s.q(tx).Query(ctx,
		`SELECT `+qualifiedLockHolderCols("ch")+`
		   FROM rimsky_claim_handles ch
		   JOIN rimsky_nodes n ON n.id = ch.holder_node_id
		  WHERE ch.held_durable = TRUE AND n.instance_id = $1`,
		instanceID)
	if err != nil {
		return nil, fmt.Errorf("lockholders.ListHeldDurableByInstance: %w", err)
	}
	defer rows.Close()
	return collectClaimHandles(rows)
}

// qualifiedLockHolderCols returns the lock-holder column list prefixed
// with the given alias. Used by JOIN queries (e.g.,
// `ListHeldDurableByInstance`) where unqualified column names would be
// ambiguous against the joined table's columns.
func qualifiedLockHolderCols(alias string) string {
	return alias + `.id, ` + alias + `.lock_kind, ` + alias + `.lock_name, ` +
		alias + `.producer_name, ` + alias + `.scope_data, ` + alias + `.address, ` +
		alias + `.intent, ` + alias + `.realized_write_semantics, ` +
		alias + `.holder_supervisor_id, ` + alias + `.holder_node_id, ` +
		alias + `.claimed_at, ` + alias + `.last_heartbeat_at, ` + alias + `.expires_at, ` +
		alias + `.frame_id, ` + alias + `.node_run_id, ` + alias + `.is_held, ` +
		alias + `.parent_claim_handle_id, ` + alias + `.lifetime, ` +
		alias + `.held_durable, ` + alias + `.version_id, ` +
		alias + `.producer_candidate_handle, ` +
		alias + `.aggregation_policy, ` + alias + `.expected_children_count, ` +
		alias + `.committed_children_count, ` + alias + `.abandoned_children_count`
}

// ListBySupervisor returns every row owned by supervisorID.
func (s *claimHandlesImpl) ListBySupervisor(ctx context.Context, supervisorID string, tx persistence.Tx) ([]persistence.ClaimHandleRow, error) {
	rows, err := s.q(tx).Query(ctx,
		`SELECT `+lockHolderCols+` FROM rimsky_claim_handles
		 WHERE holder_supervisor_id = $1
		 ORDER BY claimed_at ASC`, supervisorID,
	)
	if err != nil {
		return nil, fmt.Errorf("lockholders.ListBySupervisor: %w", err)
	}
	defer rows.Close()
	return collectClaimHandles(rows)
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
	// held_durable rows are excluded from the heartbeat: their lifetime
	// is unbounded (released only by instance termination via
	// `ReleaseHeldDurableClaims`), so they do not need expires_at
	// refresh. Coupled with the matching exclusion in `ListExpired` /
	// `DeleteIfExpired`, this keeps held-durable rows entirely outside
	// the orphan-reaper feedback loop.
	_, err := s.q(tx).Exec(ctx,
		`UPDATE rimsky_claim_handles lh
		   SET last_heartbeat_at = now(),
		       expires_at = now() + ($2 * interval '1 second')
		 WHERE lh.holder_supervisor_id = $1
		   AND lh.held_durable = FALSE
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

// ListExpired returns rows whose expires_at < now(). The scheduler's
// orphan-reap sweep iterates these.
//
// @blessed-invariant held-durable-persistence: rows with
// held_durable = TRUE are skipped — these claims survive past their
// holding subgraph's terminal and are released only by instance
// termination via ReleaseHeldDurableClaims. The orphan-reaper does
// not heartbeat them, so they MUST NOT be reaped on age alone (plan E9 step 3).
func (s *claimHandlesImpl) ListExpired(ctx context.Context, tx persistence.Tx) ([]persistence.ClaimHandleRow, error) {
	rows, err := s.q(tx).Query(ctx,
		`SELECT `+lockHolderCols+` FROM rimsky_claim_handles
		 WHERE expires_at < now() AND held_durable = FALSE
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
		 WHERE id = $1 AND holder_supervisor_id = $2`,
		id, expectedSupervisorID,
	)
	if err != nil {
		return fmt.Errorf("lockholders.Delete: %w", err)
	}
	return nil
}

// CountByNamedLock returns the number of unexpired rows held against
// lockName. Used inside the supervisor's acquisition tx after taking
// pg_advisory_xact_lock(hashtext('rimsky_lock:'||lockName)) to enforce
// the named-lock counting-mode limit.
func (s *claimHandlesImpl) CountByNamedLock(ctx context.Context, lockName string, tx persistence.Tx) (int, error) {
	var n int
	err := s.q(tx).QueryRow(ctx,
		`SELECT count(*) FROM rimsky_claim_handles
		 WHERE lock_kind = 'named' AND lock_name = $1
		   AND expires_at > now()`,
		lockName,
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("lockholders.CountByNamedLock: %w", err)
	}
	return n, nil
}

// ListByProducerScope returns every scope-kind row for producerName
// that is either unexpired OR held-durable. Used by the supervisor's
// scope-conflict re-check (§7.3 step 4a/4b).
//
// Held-durable inclusion: rows with `held_durable = TRUE` carry a stale
// `expires_at` because `ExtendHeartbeat` and `DeleteIfExpired` both
// skip them (they live unbounded until instance termination via
// `ReleaseHeldDurableClaims`). Filtering them out by `expires_at` alone
// would let a new acquirer take the same byte-equal scope while a
// durable holder is still live, breaking @blessed-invariant 4b
// (single-writer-per-scope). The `OR held_durable = TRUE` clause keeps
// durable rows in the conflict-detection set. Mirrored in SQLite.
func (s *claimHandlesImpl) ListByProducerScope(ctx context.Context, producerName string, tx persistence.Tx) ([]persistence.ClaimHandleRow, error) {
	rows, err := s.q(tx).Query(ctx,
		`SELECT `+lockHolderCols+` FROM rimsky_claim_handles
		 WHERE lock_kind = 'scope' AND producer_name = $1
		   AND (expires_at > now() OR held_durable = TRUE)
		 ORDER BY claimed_at ASC`,
		producerName,
	)
	if err != nil {
		return nil, fmt.Errorf("lockholders.ListByProducerScope: %w", err)
	}
	defer rows.Close()
	return collectClaimHandles(rows)
}

// DeleteIfExpired removes the row keyed by id, claimant-guarded on
// supervisor_id AND only when expires_at is still in the past AND only
// when held_durable = FALSE. Used by the orphan reaper. Returns
// deleted=true on success; false on no-op (claimant mismatch, fresh
// heartbeat extended the row, or the row flipped held_durable=TRUE
// between ListExpired and this DELETE — racing with `SetHeldDurable`
// fired by the auto-terminal Commit path).
//
// The `held_durable = FALSE` belt-and-suspenders mirrors the
// `ListExpired` predicate (`@blessed-invariant held-durable-
// persistence`): held-durable claims survive past their holding
// subgraph's terminal and are released only by instance termination
// via `ReleaseHeldDurableClaims`. Re-checking the column inside the
// DELETE eliminates the LISTexpired → race → DELETE window where a
// fresh `SetHeldDurable` could otherwise be undone.
func (s *claimHandlesImpl) DeleteIfExpired(ctx context.Context, id shared.UUID, supervisorID string, tx persistence.Tx) (bool, error) {
	tag, err := s.q(tx).Exec(ctx,
		`DELETE FROM rimsky_claim_handles
		 WHERE id = $1
		   AND holder_supervisor_id = $2
		   AND expires_at < now()
		   AND held_durable = FALSE`,
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
		  WHERE id = $2 AND holder_supervisor_id = $3`,
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
		  WHERE id = $2 AND holder_supervisor_id = $3`,
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
// walker (`runtime/auto_terminal.go::resolveParentClaimChain`) before
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
		  WHERE id = $2 AND holder_supervisor_id = $3`,
		delta, id, supervisorID,
	)
	if err != nil {
		return fmt.Errorf("lockholders.BumpChildOutcomeCount: %w", err)
	}
	return nil
}

// ---- helpers ----

func scanClaimHandle(sc scannable) (persistence.ClaimHandleRow, error) {
	var (
		r                persistence.ClaimHandleRow
		kind             string
		lockName         *string
		producerName     *string
		scopeData        []byte
		address          []byte
		intent           *string
		rws              *string
		frameID          *shared.UUID
		workerRequestID  *shared.UUID
		isHeld           bool
		parentClaimID    *shared.UUID
		lifetime         *string
		heldDurable      bool
		versionID        *string
		candidateHandle  []byte
		aggregation      []byte
		expectedChildren int
		committed        int
		abandoned        int
	)
	if err := sc.Scan(
		&r.ID, &kind,
		&lockName, &producerName, &scopeData, &address, &intent,
		&rws,
		&r.HolderSupervisorID, &r.HolderNodeID,
		&r.ClaimedAt, &r.LastHeartbeatAt, &r.ExpiresAt, &frameID,
		&workerRequestID, &isHeld,
		&parentClaimID, &lifetime, &heldDurable, &versionID,
		&candidateHandle,
		&aggregation, &expectedChildren,
		&committed, &abandoned,
	); err != nil {
		return persistence.ClaimHandleRow{}, err
	}
	r.LockKind = persistence.LockKind(kind)
	r.LockName = lockName
	r.ProducerName = producerName
	r.ScopeData = scopeData
	r.Address = address
	r.Intent = intent
	if rws != nil {
		r.RealizedWriteSemantics = *rws
	}
	r.FrameID = frameID
	r.NodeRunID = workerRequestID
	r.IsHeld = isHeld
	r.ParentClaimHandleID = parentClaimID
	if lifetime != nil {
		r.Lifetime = *lifetime
	}
	r.HeldDurable = heldDurable
	if versionID != nil {
		r.VersionID = *versionID
	}
	r.ProducerCandidateHandle = candidateHandle
	r.AggregationPolicy = aggregation
	r.ExpectedChildrenCount = expectedChildren
	r.CommittedChildrenCount = committed
	r.AbandonedChildrenCount = abandoned
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
