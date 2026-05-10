// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// claim_handles.go — SQLite-backed persistence.ClaimHandlesStore.
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
	"github.com/fallguy/rimsky/modeling/shared"
)

const lockHolderCols = `
  id, lock_kind, lock_name, store_name, scope_data, address, intent,
  realized_write_semantics,
  holder_supervisor_id, holder_node_id,
  claimed_at, last_heartbeat_at, expires_at, frame_id,
  worker_request_id, is_held
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
	_, err := s.q(tx).ExecContext(ctx,
		`INSERT INTO rimsky_claim_handle (
		   id, lock_kind, lock_name, store_name, scope_data, address, intent,
		   realized_write_semantics,
		   holder_supervisor_id, holder_node_id,
		   claimed_at, last_heartbeat_at, expires_at, frame_id,
		   worker_request_id, is_held
		 ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		in.ID.String(), string(in.LockKind),
		in.LockName, in.StoreName,
		nullableJSONB(in.ScopeData), nullableJSONB(in.Address),
		in.Intent,
		rws,
		in.HolderSupervisorID, in.HolderNodeID.String(),
		now, now, formatTime(in.ExpiresAt), nullableUUID(in.FrameID),
		nullableUUID(in.WorkerRequestID), isHeldInt,
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
		`UPDATE rimsky_claim_handle
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
		`UPDATE rimsky_claim_handle
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
		`UPDATE rimsky_claim_handle
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
		`SELECT `+lockHolderCols+` FROM rimsky_claim_handle WHERE id = ?`, id.String(),
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
		`SELECT `+lockHolderCols+` FROM rimsky_claim_handle WHERE id = ?`, id.String(),
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
		`SELECT `+lockHolderCols+` FROM rimsky_claim_handle
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
		`SELECT `+lockHolderCols+` FROM rimsky_claim_handle
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

func (s *claimHandlesImpl) ListBySupervisor(ctx context.Context, supervisorID string, tx persistence.Tx) ([]persistence.ClaimHandleRow, error) {
	rows, err := s.q(tx).QueryContext(ctx,
		`SELECT `+lockHolderCols+` FROM rimsky_claim_handle
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
func (s *claimHandlesImpl) ExtendHeartbeat(ctx context.Context, supervisorID string, expiresAt time.Time, tx persistence.Tx) error {
	now := nowUTC()
	_, err := s.q(tx).ExecContext(ctx,
		`UPDATE rimsky_claim_handle
		   SET last_heartbeat_at = ?,
		       expires_at = ?
		 WHERE holder_supervisor_id = ?
		   AND (
		        holder_node_id IN (
		            SELECT id FROM rimsky_nodes
		             WHERE assigned_supervisor_id = ? AND state = 'running'
		        )
		        OR EXISTS (
		            SELECT 1 FROM rimsky_claim_holders ch
		              JOIN rimsky_nodes n ON n.id = ch.holder_node_id
		             WHERE ch.claim_handle_id = rimsky_claim_handle.id
		               AND ch.state = 'active'
		               AND n.state = 'running'
		        )
		   )`,
		now, formatTime(expiresAt), supervisorID, supervisorID,
	)
	if err != nil {
		return fmt.Errorf("lockholders.ExtendHeartbeat: %w", err)
	}
	return nil
}

func (s *claimHandlesImpl) ListExpired(ctx context.Context, tx persistence.Tx) ([]persistence.ClaimHandleRow, error) {
	rows, err := s.q(tx).QueryContext(ctx,
		`SELECT `+lockHolderCols+` FROM rimsky_claim_handle
		 WHERE expires_at < ?
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
		`DELETE FROM rimsky_claim_handle
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
		`SELECT count(*) FROM rimsky_claim_handle
		 WHERE lock_kind = 'named' AND lock_name = ?
		   AND expires_at > ?`,
		lockName, nowUTC(),
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("lockholders.CountByNamedLock: %w", err)
	}
	return n, nil
}

func (s *claimHandlesImpl) ListByStoreScope(ctx context.Context, storeName string, tx persistence.Tx) ([]persistence.ClaimHandleRow, error) {
	rows, err := s.q(tx).QueryContext(ctx,
		`SELECT `+lockHolderCols+` FROM rimsky_claim_handle
		 WHERE lock_kind = 'scope' AND store_name = ?
		   AND expires_at > ?
		 ORDER BY claimed_at ASC`,
		storeName, nowUTC(),
	)
	if err != nil {
		return nil, fmt.Errorf("lockholders.ListByStoreScope: %w", err)
	}
	defer rows.Close()
	return collectClaimHandles(rows)
}

func (s *claimHandlesImpl) DeleteIfExpired(ctx context.Context, id shared.UUID, supervisorID string, tx persistence.Tx) (bool, error) {
	res, err := s.q(tx).ExecContext(ctx,
		`DELETE FROM rimsky_claim_handle
		 WHERE id = ?
		   AND holder_supervisor_id = ?
		   AND expires_at < ?`,
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
	var storeArg, supArg, nodeArg, instArg, nodeTypeArg any
	if filter.StoreName != "" {
		storeArg = filter.StoreName
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
		   FROM rimsky_claim_handle lh
		  WHERE (? IS NULL OR lh.store_name = ?)
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
		storeArg, storeArg, supArg, supArg, nodeArg, nodeArg,
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
		storeName          sql.NullString
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
	)
	if err := sc.Scan(
		&idStr, &kind,
		&lockName, &storeName, &scopeData, &address, &intent,
		&rws,
		&holderSupervisorID, &holderNodeIDStr,
		&claimedAtStr, &lastHeartbeatAtStr, &expiresAtStr, &frameIDStr,
		&workerRequestIDStr, &isHeldInt,
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
	if storeName.Valid {
		v := storeName.String
		r.StoreName = &v
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
	r.WorkerRequestID = workerRequestID
	r.IsHeld = isHeldInt != 0
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
