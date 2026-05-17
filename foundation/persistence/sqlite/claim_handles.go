// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// claim_handles.go — SQLite-backed persistence.ClaimHandleTable.
//
// @blessed-invariant 9a: lock state lives only in the persistence layer.
// @blessed-invariant 4: claimant-guarded release.
package sqlite

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

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

func (s *claimHandlesImpl) Insert(ctx context.Context, in persistence.ClaimHandleInsertInput, tx persistence.Tx) error {
	now := nowUTC()
	var rws *string
	if in.RealizedWriteSemantics != "" {
		v := in.RealizedWriteSemantics
		rws = &v
	}
	isHeldInt := 0
	if in.IsHeld {
		isHeldInt = 1
	}
	lifetime := in.Lifetime
	if lifetime == "" {
		lifetime = "subgraph"
	}
	var candidateHandle any
	if len(in.ProducerCandidateHandle) > 0 {
		candidateHandle = in.ProducerCandidateHandle
	}
	var aggPolicy any
	if len(in.AggregationPolicy) > 0 {
		aggPolicy = string(in.AggregationPolicy)
	}
	_, err := s.q(tx).ExecContext(ctx,
		`INSERT INTO rimsky_claim_handles (
		   id, lock_kind, lock_name, producer_name, scope_data, address, intent,
		   realized_write_semantics,
		   holder_supervisor_id, holder_node_id,
		   claimed_at, last_heartbeat_at, expires_at, frame_id,
		   node_run_id, is_held,
		   parent_claim_handle_id, lifetime, producer_candidate_handle,
		   aggregation_policy
		 ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		in.ID.String(), string(in.LockKind),
		in.LockName, in.ProducerName,
		nullableJSONB(in.ScopeData), nullableJSONB(in.Address),
		in.Intent,
		rws,
		in.HolderSupervisorID, in.HolderNodeID.String(),
		now, now, formatTime(in.ExpiresAt), nullableUUID(in.FrameID),
		nullableUUID(in.NodeRunID), isHeldInt,
		nullableUUID(in.ParentClaimHandleID), lifetime, candidateHandle,
		aggPolicy,
	)
	if err != nil {
		return fmt.Errorf("lockholders.Insert: %w", err)
	}
	return nil
}

func (s *claimHandlesImpl) UpdateRealizedWriteSemantics(
	ctx context.Context, id shared.UUID, supervisorID string, ws string, tx persistence.Tx,
) error {
	var v *string
	if ws != "" {
		s := ws
		v = &s
	}
	_, err := s.q(tx).ExecContext(ctx,
		`UPDATE rimsky_claim_handles
		    SET realized_write_semantics = ?
		  WHERE id = ? AND holder_supervisor_id = ?`,
		v, id.String(), supervisorID,
	)
	if err != nil {
		return fmt.Errorf("lockholders.UpdateRealizedWriteSemantics: %w", err)
	}
	return nil
}

func (s *claimHandlesImpl) UpdateAddress(
	ctx context.Context, id shared.UUID, supervisorID string, address json.RawMessage, tx persistence.Tx,
) error {
	_, err := s.q(tx).ExecContext(ctx,
		`UPDATE rimsky_claim_handles
		    SET address = ?
		  WHERE id = ? AND holder_supervisor_id = ?`,
		nullableJSONB(address), id.String(), supervisorID,
	)
	if err != nil {
		return fmt.Errorf("lockholders.UpdateAddress: %w", err)
	}
	return nil
}

func (s *claimHandlesImpl) UpdateScope(
	ctx context.Context, id shared.UUID, supervisorID string, scope json.RawMessage, tx persistence.Tx,
) error {
	_, err := s.q(tx).ExecContext(ctx,
		`UPDATE rimsky_claim_handles
		    SET scope_data = ?
		  WHERE id = ? AND holder_supervisor_id = ?`,
		nullableJSONB(scope), id.String(), supervisorID,
	)
	if err != nil {
		return fmt.Errorf("lockholders.UpdateScope: %w", err)
	}
	return nil
}

func (s *claimHandlesImpl) Get(ctx context.Context, id shared.UUID, tx persistence.Tx) (*persistence.ClaimHandleRow, error) {
	row := s.q(tx).QueryRowContext(ctx,
		`SELECT `+lockHolderCols+` FROM rimsky_claim_handles WHERE id = ?`, id.String(),
	)
	out, err := scanClaimHandle(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &out, nil
}

// LockForUpdate omits FOR UPDATE under SQLite; the surrounding
// BEGIN IMMEDIATE writer-slot hold subsumes per-row locking.
func (s *claimHandlesImpl) LockForUpdate(ctx context.Context, id shared.UUID, tx persistence.Tx) (*persistence.ClaimHandleRow, error) {
	row := s.q(tx).QueryRowContext(ctx,
		`SELECT `+lockHolderCols+` FROM rimsky_claim_handles WHERE id = ?`, id.String(),
	)
	out, err := scanClaimHandle(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("lockholders.LockForUpdate: %w", err)
	}
	return &out, nil
}

func (s *claimHandlesImpl) ListByHolderNode(ctx context.Context, holderNodeID shared.UUID, tx persistence.Tx) ([]persistence.ClaimHandleRow, error) {
	rows, err := s.q(tx).QueryContext(ctx,
		`SELECT `+lockHolderCols+` FROM rimsky_claim_handles
		 WHERE holder_node_id = ?
		 ORDER BY claimed_at ASC`, holderNodeID.String(),
	)
	if err != nil {
		return nil, fmt.Errorf("lockholders.ListByHolderNode: %w", err)
	}
	defer rows.Close()
	return collectClaimHandles(rows)
}

// GetByFrameAndNode returns the lock-holder row for (nodeID, frameID),
// or (nil, nil) when no matching row exists.
func (s *claimHandlesImpl) GetByFrameAndNode(ctx context.Context, nodeID shared.UUID, frameID shared.UUID, tx persistence.Tx) (*persistence.ClaimHandleRow, error) {
	row := s.q(tx).QueryRowContext(ctx,
		`SELECT `+lockHolderCols+` FROM rimsky_claim_handles
		 WHERE holder_node_id = ? AND frame_id = ?
		 LIMIT 1`,
		nodeID.String(), frameID.String(),
	)
	out, err := scanClaimHandle(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("lockholders.GetByFrameAndNode: %w", err)
	}
	return &out, nil
}

// ListChildClaimHandles returns claim_handles rows whose
// parent_claim_handle_id equals parentID. Spec §Recursive claim-tree
// resolution.
func (s *claimHandlesImpl) ListChildClaimHandles(ctx context.Context, parentID shared.UUID, tx persistence.Tx) ([]persistence.ClaimHandleRow, error) {
	rows, err := s.q(tx).QueryContext(ctx,
		`SELECT `+lockHolderCols+` FROM rimsky_claim_handles
		 WHERE parent_claim_handle_id = ?`,
		parentID.String())
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
	val := 0
	if heldDurable {
		val = 1
	}
	_, err := s.q(tx).ExecContext(ctx,
		`UPDATE rimsky_claim_handles SET held_durable = ?
		 WHERE id = ? AND holder_supervisor_id = ?`,
		val, id.String(), supervisorID)
	if err != nil {
		return fmt.Errorf("lockholders.SetHeldDurable: %w", err)
	}
	return nil
}

// SetVersionID persists the producer-returned canonical version_id
// claimant-guarded. Inert in rimsky.
func (s *claimHandlesImpl) SetVersionID(
	ctx context.Context, id shared.UUID, supervisorID string, versionID string, tx persistence.Tx,
) error {
	var v any
	if versionID != "" {
		v = versionID
	}
	_, err := s.q(tx).ExecContext(ctx,
		`UPDATE rimsky_claim_handles SET version_id = ?
		 WHERE id = ? AND holder_supervisor_id = ?`,
		v, id.String(), supervisorID)
	if err != nil {
		return fmt.Errorf("lockholders.SetVersionID: %w", err)
	}
	return nil
}

// SetAggregationPolicy writes the parent-claim aggregation policy
// snapshot on a claim_handle row. Mirrors the postgres impl.
func (s *claimHandlesImpl) SetAggregationPolicy(
	ctx context.Context, id shared.UUID, supervisorID string, policy json.RawMessage, tx persistence.Tx,
) error {
	var v any
	if len(policy) > 0 {
		v = string(policy)
	}
	_, err := s.q(tx).ExecContext(ctx,
		`UPDATE rimsky_claim_handles
		    SET aggregation_policy = ?
		  WHERE id = ? AND holder_supervisor_id = ?`,
		v, id.String(), supervisorID,
	)
	if err != nil {
		return fmt.Errorf("lockholders.SetAggregationPolicy: %w", err)
	}
	return nil
}

// BumpExpectedChildrenCount adds delta to the parent's
// expected_children_count. Mirrors the postgres impl.
func (s *claimHandlesImpl) BumpExpectedChildrenCount(
	ctx context.Context, id shared.UUID, supervisorID string, delta int, tx persistence.Tx,
) error {
	_, err := s.q(tx).ExecContext(ctx,
		`UPDATE rimsky_claim_handles
		    SET expected_children_count = expected_children_count + ?
		  WHERE id = ? AND holder_supervisor_id = ?`,
		delta, id.String(), supervisorID,
	)
	if err != nil {
		return fmt.Errorf("lockholders.BumpExpectedChildrenCount: %w", err)
	}
	return nil
}

// BumpChildOutcomeCount adds delta to either committed_children_count
// (outcome="commit") or abandoned_children_count (outcome="abandon").
// Mirrors the postgres impl.
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
	_, err := s.q(tx).ExecContext(ctx,
		`UPDATE rimsky_claim_handles
		    SET `+column+` = `+column+` + ?
		  WHERE id = ? AND holder_supervisor_id = ?`,
		delta, id.String(), supervisorID,
	)
	if err != nil {
		return fmt.Errorf("lockholders.BumpChildOutcomeCount: %w", err)
	}
	return nil
}

// ListHeldDurableByInstance returns claim_handles rows held-durable for
// the given instance. Column qualification mirrors the postgres impl —
// the JOIN against `rimsky_nodes n` introduces a second `id` column,
// so every selected column is `ch.`-prefixed to avoid ambiguous
// references at execution time.
func (s *claimHandlesImpl) ListHeldDurableByInstance(
	ctx context.Context, instanceID shared.UUID, tx persistence.Tx,
) ([]persistence.ClaimHandleRow, error) {
	rows, err := s.q(tx).QueryContext(ctx,
		`SELECT `+qualifiedLockHolderCols("ch")+`
		   FROM rimsky_claim_handles ch
		   JOIN rimsky_nodes n ON n.id = ch.holder_node_id
		  WHERE ch.held_durable = 1 AND n.instance_id = ?`,
		instanceID.String())
	if err != nil {
		return nil, fmt.Errorf("lockholders.ListHeldDurableByInstance: %w", err)
	}
	defer rows.Close()
	return collectClaimHandles(rows)
}

// qualifiedLockHolderCols returns the lock-holder column list prefixed
// with the given alias. Mirrors the postgres helper.
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

func (s *claimHandlesImpl) ListBySupervisor(ctx context.Context, supervisorID string, tx persistence.Tx) ([]persistence.ClaimHandleRow, error) {
	rows, err := s.q(tx).QueryContext(ctx,
		`SELECT `+lockHolderCols+` FROM rimsky_claim_handles
		 WHERE holder_supervisor_id = ?
		 ORDER BY claimed_at ASC`, supervisorID,
	)
	if err != nil {
		return nil, fmt.Errorf("lockholders.ListBySupervisor: %w", err)
	}
	defer rows.Close()
	return collectClaimHandles(rows)
}

// ExtendHeartbeat updates last_heartbeat_at and expires_at for every row
// owned by supervisorID whose lifetime should currently be active.
// Post-stage-3 / stage-5 the predicate sources from rimsky_node_runs;
// see the postgres mirror for the full rationale.
func (s *claimHandlesImpl) ExtendHeartbeat(ctx context.Context, supervisorID string, expiresAt time.Time, tx persistence.Tx) error {
	now := nowUTC()
	// Mirror the postgres exclusion: held_durable rows are outside the
	// orphan-reaper feedback loop, so do not heartbeat them.
	_, err := s.q(tx).ExecContext(ctx,
		`UPDATE rimsky_claim_handles
		   SET last_heartbeat_at = ?,
		       expires_at = ?
		 WHERE holder_supervisor_id = ?
		   AND held_durable = 0
		   AND (
		        EXISTS (
		            SELECT 1 FROM rimsky_node_runs r
		             WHERE r.node_id = rimsky_claim_handles.holder_node_id
		               AND r.claimed_by = ?
		               AND r.state = 'running'
		        )
		        OR EXISTS (
		            SELECT 1 FROM rimsky_claim_holders ch
		              JOIN rimsky_node_runs r ON r.id = ch.holder_run_id
		             WHERE ch.claim_handle_id = rimsky_claim_handles.id
		               AND ch.state = 'active'
		               AND r.state = 'running'
		        )
		   )`,
		now, formatTime(expiresAt), supervisorID, supervisorID,
	)
	if err != nil {
		return fmt.Errorf("lockholders.ExtendHeartbeat: %w", err)
	}
	return nil
}

// ListExpired excludes held_durable rows; see postgres mirror for the
// blessed-invariant rationale.
func (s *claimHandlesImpl) ListExpired(ctx context.Context, tx persistence.Tx) ([]persistence.ClaimHandleRow, error) {
	rows, err := s.q(tx).QueryContext(ctx,
		`SELECT `+lockHolderCols+` FROM rimsky_claim_handles
		 WHERE expires_at < ? AND held_durable = 0
		 ORDER BY expires_at ASC`, nowUTC(),
	)
	if err != nil {
		return nil, fmt.Errorf("lockholders.ListExpired: %w", err)
	}
	defer rows.Close()
	return collectClaimHandles(rows)
}

func (s *claimHandlesImpl) Delete(ctx context.Context, id shared.UUID, expectedSupervisorID string, tx persistence.Tx) error {
	_, err := s.q(tx).ExecContext(ctx,
		`DELETE FROM rimsky_claim_handles
		 WHERE id = ? AND holder_supervisor_id = ?`,
		id.String(), expectedSupervisorID,
	)
	if err != nil {
		return fmt.Errorf("lockholders.Delete: %w", err)
	}
	return nil
}

func (s *claimHandlesImpl) CountByNamedLock(ctx context.Context, lockName string, tx persistence.Tx) (int, error) {
	var n int
	err := s.q(tx).QueryRowContext(ctx,
		`SELECT count(*) FROM rimsky_claim_handles
		 WHERE lock_kind = 'named' AND lock_name = ?
		   AND expires_at > ?`,
		lockName, nowUTC(),
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("lockholders.CountByNamedLock: %w", err)
	}
	return n, nil
}

// ListByProducerScope returns every scope-kind row for producerName
// that is either unexpired OR held-durable. Mirrors the postgres
// `OR held_durable = TRUE` predicate so held-durable rows participate
// in scope-conflict detection (@blessed-invariant 4b single-writer-
// per-scope). See the postgres mirror for the rationale.
func (s *claimHandlesImpl) ListByProducerScope(ctx context.Context, producerName string, tx persistence.Tx) ([]persistence.ClaimHandleRow, error) {
	rows, err := s.q(tx).QueryContext(ctx,
		`SELECT `+lockHolderCols+` FROM rimsky_claim_handles
		 WHERE lock_kind = 'scope' AND producer_name = ?
		   AND (expires_at > ? OR held_durable = 1)
		 ORDER BY claimed_at ASC`,
		producerName, nowUTC(),
	)
	if err != nil {
		return nil, fmt.Errorf("lockholders.ListByProducerScope: %w", err)
	}
	defer rows.Close()
	return collectClaimHandles(rows)
}

// DeleteIfExpired mirrors the postgres impl: claimant-guarded delete
// of expired rows, additionally gated on `held_durable = 0` so a
// concurrent `SetHeldDurable` (fired by the auto-terminal Commit path)
// cannot race the orphan reaper. See the postgres mirror for the
// blessed-invariant rationale.
func (s *claimHandlesImpl) DeleteIfExpired(ctx context.Context, id shared.UUID, supervisorID string, tx persistence.Tx) (bool, error) {
	res, err := s.q(tx).ExecContext(ctx,
		`DELETE FROM rimsky_claim_handles
		 WHERE id = ?
		   AND holder_supervisor_id = ?
		   AND expires_at < ?
		   AND held_durable = 0`,
		id.String(), supervisorID, nowUTC(),
	)
	if err != nil {
		return false, fmt.Errorf("lockholders.DeleteIfExpired: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("lockholders.DeleteIfExpired: rows-affected: %w", err)
	}
	return n > 0, nil
}

// ListForObservability returns lock-holder rows matching filter,
// cursor-paginated by claimed_at DESC. Used by the observability
// /v1/observability/lock-holders endpoint (spec §1.2.4).
func (s *claimHandlesImpl) ListForObservability(ctx context.Context, filter persistence.LockHolderListFilter, pag persistence.ListPagination, tx persistence.Tx) (persistence.PaginatedListResult[persistence.ClaimHandleRow], error) {
	limit := pag.Limit
	if limit <= 0 {
		limit = 50
	}
	var producerArg, supArg, nodeArg, instArg, nodeTypeArg any
	if filter.ProducerName != "" {
		producerArg = filter.ProducerName
	}
	if filter.HolderSupervisor != "" {
		supArg = filter.HolderSupervisor
	}
	if filter.HolderNodeID != nil {
		nodeArg = filter.HolderNodeID.String()
	}
	if filter.InstanceID != nil {
		instArg = filter.InstanceID.String()
	}
	if filter.NodeType != "" {
		nodeTypeArg = filter.NodeType
	}
	var cursorClaimed, cursorID any
	if pag.Cursor != "" {
		c, id, err := decodeLockHolderCursor(pag.Cursor)
		if err != nil {
			return persistence.PaginatedListResult[persistence.ClaimHandleRow]{}, fmt.Errorf("lockholders.list: bad cursor: %w", err)
		}
		cursorClaimed = formatTime(c)
		cursorID = id.String()
	}
	rows, err := s.q(tx).QueryContext(ctx,
		`SELECT `+lockHolderCols+`
		   FROM rimsky_claim_handles lh
		  WHERE (? IS NULL OR lh.producer_name = ?)
		    AND (? IS NULL OR lh.holder_supervisor_id = ?)
		    AND (? IS NULL OR lh.holder_node_id = ?)
		    AND (
		         ? IS NULL OR EXISTS (
		           SELECT 1 FROM rimsky_nodes n
		            WHERE n.id = lh.holder_node_id AND n.instance_id = ?
		         )
		    )
		    AND (
		         ? IS NULL OR EXISTS (
		           SELECT 1 FROM rimsky_nodes n
		            WHERE n.id = lh.holder_node_id AND n.node_type = ?
		         )
		    )
		    AND (? IS NULL OR (lh.claimed_at, lh.id) < (?, ?))
		  ORDER BY lh.claimed_at DESC, lh.id DESC
		  LIMIT ?`,
		producerArg, producerArg, supArg, supArg, nodeArg, nodeArg,
		instArg, instArg, nodeTypeArg, nodeTypeArg,
		cursorClaimed, cursorClaimed, cursorID, limit,
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

func scanClaimHandle(sc scannable) (persistence.ClaimHandleRow, error) {
	var (
		r                  persistence.ClaimHandleRow
		idStr              string
		kind               string
		lockName           sql.NullString
		producerName       sql.NullString
		scopeData          sql.NullString
		address            sql.NullString
		intent             sql.NullString
		rws                sql.NullString
		holderSupervisorID string
		holderNodeIDStr    string
		claimedAtStr       string
		lastHeartbeatAtStr string
		expiresAtStr       string
		frameIDStr         sql.NullString
		workerRequestIDStr sql.NullString
		isHeldInt          int
		parentClaimIDStr   sql.NullString
		lifetime           sql.NullString
		heldDurableInt     int
		versionID          sql.NullString
		candidateHandle    []byte
		aggregation        sql.NullString
		expectedChildren   int
		committed          int
		abandoned          int
	)
	if err := sc.Scan(
		&idStr, &kind,
		&lockName, &producerName, &scopeData, &address, &intent,
		&rws,
		&holderSupervisorID, &holderNodeIDStr,
		&claimedAtStr, &lastHeartbeatAtStr, &expiresAtStr, &frameIDStr,
		&workerRequestIDStr, &isHeldInt,
		&parentClaimIDStr, &lifetime, &heldDurableInt, &versionID,
		&candidateHandle,
		&aggregation, &expectedChildren,
		&committed, &abandoned,
	); err != nil {
		return persistence.ClaimHandleRow{}, err
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		return persistence.ClaimHandleRow{}, err
	}
	holderNodeID, err := uuid.Parse(holderNodeIDStr)
	if err != nil {
		return persistence.ClaimHandleRow{}, err
	}
	r.ID = id
	r.LockKind = persistence.LockKind(kind)
	r.HolderSupervisorID = holderSupervisorID
	r.HolderNodeID = holderNodeID
	if lockName.Valid {
		v := lockName.String
		r.LockName = &v
	}
	if producerName.Valid {
		v := producerName.String
		r.ProducerName = &v
	}
	if scopeData.Valid {
		r.ScopeData = json.RawMessage(scopeData.String)
	}
	if address.Valid {
		r.Address = json.RawMessage(address.String)
	}
	if intent.Valid {
		v := intent.String
		r.Intent = &v
	}
	if rws.Valid {
		r.RealizedWriteSemantics = rws.String
	}
	frameID, err := scanNullableUUID(frameIDStr)
	if err != nil {
		return persistence.ClaimHandleRow{}, err
	}
	r.FrameID = frameID
	workerRequestID, err := scanNullableUUID(workerRequestIDStr)
	if err != nil {
		return persistence.ClaimHandleRow{}, err
	}
	r.NodeRunID = workerRequestID
	r.IsHeld = isHeldInt != 0
	parentClaimID, err := scanNullableUUID(parentClaimIDStr)
	if err != nil {
		return persistence.ClaimHandleRow{}, err
	}
	r.ParentClaimHandleID = parentClaimID
	if lifetime.Valid {
		r.Lifetime = lifetime.String
	}
	r.HeldDurable = heldDurableInt != 0
	if versionID.Valid {
		r.VersionID = versionID.String
	}
	r.ProducerCandidateHandle = candidateHandle
	if aggregation.Valid {
		r.AggregationPolicy = json.RawMessage(aggregation.String)
	}
	r.ExpectedChildrenCount = expectedChildren
	r.CommittedChildrenCount = committed
	r.AbandonedChildrenCount = abandoned
	if r.ClaimedAt, err = parseTime(claimedAtStr); err != nil {
		return persistence.ClaimHandleRow{}, err
	}
	if r.LastHeartbeatAt, err = parseTime(lastHeartbeatAtStr); err != nil {
		return persistence.ClaimHandleRow{}, err
	}
	if r.ExpiresAt, err = parseTime(expiresAtStr); err != nil {
		return persistence.ClaimHandleRow{}, err
	}
	return r, nil
}

func collectClaimHandles(rows *sql.Rows) ([]persistence.ClaimHandleRow, error) {
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
