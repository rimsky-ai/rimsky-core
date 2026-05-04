// lock_holders.go — SQLite-backed persistence.LockHoldersStore.
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
  claimed_at, last_heartbeat_at, expires_at, frame_id
`

func (s *lockHoldersImpl) Insert(ctx context.Context, in persistence.LockHolderInsertInput, tx persistence.Tx) error {
	if tx == nil {
		return errors.New("lockholders.Insert: persistence.Tx required")
	}
	now := nowUTC()
	var rws *string
	if in.RealizedWriteSemantics != "" {
		v := in.RealizedWriteSemantics
		rws = &v
	}
	_, err := s.q(tx).ExecContext(ctx,
		`INSERT INTO rimsky_lock_holders (
		   id, lock_kind, lock_name, store_name, scope_data, address, intent,
		   realized_write_semantics,
		   holder_supervisor_id, holder_node_id,
		   claimed_at, last_heartbeat_at, expires_at, frame_id
		 ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		in.ID.String(), string(in.LockKind),
		in.LockName, in.StoreName,
		nullableJSONB(in.ScopeData), nullableJSONB(in.Address),
		in.Intent,
		rws,
		in.HolderSupervisorID, in.HolderNodeID.String(),
		now, now, formatTime(in.ExpiresAt), nullableUUID(in.FrameID),
	)
	if err != nil {
		return fmt.Errorf("lockholders.Insert: %w", err)
	}
	return nil
}

func (s *lockHoldersImpl) UpdateRealizedWriteSemantics(
	ctx context.Context, id shared.UUID, supervisorID string, ws string, tx persistence.Tx,
) error {
	if tx == nil {
		return errors.New("lockholders.UpdateRealizedWriteSemantics: persistence.Tx required")
	}
	var v *string
	if ws != "" {
		s := ws
		v = &s
	}
	_, err := s.q(tx).ExecContext(ctx,
		`UPDATE rimsky_lock_holders
		    SET realized_write_semantics = ?
		  WHERE id = ? AND holder_supervisor_id = ?`,
		v, id.String(), supervisorID,
	)
	if err != nil {
		return fmt.Errorf("lockholders.UpdateRealizedWriteSemantics: %w", err)
	}
	return nil
}

func (s *lockHoldersImpl) UpdateAddress(
	ctx context.Context, id shared.UUID, supervisorID string, address json.RawMessage, tx persistence.Tx,
) error {
	if tx == nil {
		return errors.New("lockholders.UpdateAddress: persistence.Tx required")
	}
	_, err := s.q(tx).ExecContext(ctx,
		`UPDATE rimsky_lock_holders
		    SET address = ?
		  WHERE id = ? AND holder_supervisor_id = ?`,
		nullableJSONB(address), id.String(), supervisorID,
	)
	if err != nil {
		return fmt.Errorf("lockholders.UpdateAddress: %w", err)
	}
	return nil
}

func (s *lockHoldersImpl) UpdateScope(
	ctx context.Context, id shared.UUID, supervisorID string, scope json.RawMessage, tx persistence.Tx,
) error {
	if tx == nil {
		return errors.New("lockholders.UpdateScope: persistence.Tx required")
	}
	_, err := s.q(tx).ExecContext(ctx,
		`UPDATE rimsky_lock_holders
		    SET scope_data = ?
		  WHERE id = ? AND holder_supervisor_id = ?`,
		nullableJSONB(scope), id.String(), supervisorID,
	)
	if err != nil {
		return fmt.Errorf("lockholders.UpdateScope: %w", err)
	}
	return nil
}

func (s *lockHoldersImpl) Get(ctx context.Context, id shared.UUID, tx persistence.Tx) (*persistence.LockHolderRow, error) {
	row := s.q(tx).QueryRowContext(ctx,
		`SELECT `+lockHolderCols+` FROM rimsky_lock_holders WHERE id = ?`, id.String(),
	)
	out, err := scanLockHolder(row)
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
func (s *lockHoldersImpl) LockForUpdate(ctx context.Context, id shared.UUID, tx persistence.Tx) (*persistence.LockHolderRow, error) {
	if tx == nil {
		return nil, errors.New("lockholders.LockForUpdate: persistence.Tx required")
	}
	row := s.q(tx).QueryRowContext(ctx,
		`SELECT `+lockHolderCols+` FROM rimsky_lock_holders WHERE id = ?`, id.String(),
	)
	out, err := scanLockHolder(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("lockholders.LockForUpdate: %w", err)
	}
	return &out, nil
}

func (s *lockHoldersImpl) ListByHolderNode(ctx context.Context, holderNodeID shared.UUID, tx persistence.Tx) ([]persistence.LockHolderRow, error) {
	rows, err := s.q(tx).QueryContext(ctx,
		`SELECT `+lockHolderCols+` FROM rimsky_lock_holders
		 WHERE holder_node_id = ?
		 ORDER BY claimed_at ASC`, holderNodeID.String(),
	)
	if err != nil {
		return nil, fmt.Errorf("lockholders.ListByHolderNode: %w", err)
	}
	defer rows.Close()
	return collectLockHolders(rows)
}

// GetByFrameAndNode returns the lock-holder row for (nodeID, frameID),
// or (nil, nil) when no matching row exists.
func (s *lockHoldersImpl) GetByFrameAndNode(ctx context.Context, nodeID shared.UUID, frameID shared.UUID, tx persistence.Tx) (*persistence.LockHolderRow, error) {
	row := s.q(tx).QueryRowContext(ctx,
		`SELECT `+lockHolderCols+` FROM rimsky_lock_holders
		 WHERE holder_node_id = ? AND frame_id = ?
		 LIMIT 1`,
		nodeID.String(), frameID.String(),
	)
	out, err := scanLockHolder(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("lockholders.GetByFrameAndNode: %w", err)
	}
	return &out, nil
}

func (s *lockHoldersImpl) ListBySupervisor(ctx context.Context, supervisorID string, tx persistence.Tx) ([]persistence.LockHolderRow, error) {
	rows, err := s.q(tx).QueryContext(ctx,
		`SELECT `+lockHolderCols+` FROM rimsky_lock_holders
		 WHERE holder_supervisor_id = ?
		 ORDER BY claimed_at ASC`, supervisorID,
	)
	if err != nil {
		return nil, fmt.Errorf("lockholders.ListBySupervisor: %w", err)
	}
	defer rows.Close()
	return collectLockHolders(rows)
}

// ExtendHeartbeat updates last_heartbeat_at and expires_at for every row
// owned by supervisorID whose lifetime should currently be active.
func (s *lockHoldersImpl) ExtendHeartbeat(ctx context.Context, supervisorID string, expiresAt time.Time, tx persistence.Tx) error {
	now := nowUTC()
	_, err := s.q(tx).ExecContext(ctx,
		`UPDATE rimsky_lock_holders
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
		             WHERE ch.lock_holder_id = rimsky_lock_holders.id
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

func (s *lockHoldersImpl) ListExpired(ctx context.Context, tx persistence.Tx) ([]persistence.LockHolderRow, error) {
	rows, err := s.q(tx).QueryContext(ctx,
		`SELECT `+lockHolderCols+` FROM rimsky_lock_holders
		 WHERE expires_at < ?
		 ORDER BY expires_at ASC`, nowUTC(),
	)
	if err != nil {
		return nil, fmt.Errorf("lockholders.ListExpired: %w", err)
	}
	defer rows.Close()
	return collectLockHolders(rows)
}

func (s *lockHoldersImpl) Delete(ctx context.Context, id shared.UUID, expectedSupervisorID string, tx persistence.Tx) error {
	if tx == nil {
		return errors.New("lockholders.Delete: persistence.Tx required")
	}
	_, err := s.q(tx).ExecContext(ctx,
		`DELETE FROM rimsky_lock_holders
		 WHERE id = ? AND holder_supervisor_id = ?`,
		id.String(), expectedSupervisorID,
	)
	if err != nil {
		return fmt.Errorf("lockholders.Delete: %w", err)
	}
	return nil
}

func (s *lockHoldersImpl) CountByNamedLock(ctx context.Context, lockName string, tx persistence.Tx) (int, error) {
	if tx == nil {
		return 0, errors.New("lockholders.CountByNamedLock: persistence.Tx required (advisory lock must be held)")
	}
	var n int
	err := s.q(tx).QueryRowContext(ctx,
		`SELECT count(*) FROM rimsky_lock_holders
		 WHERE lock_kind = 'named' AND lock_name = ?
		   AND expires_at > ?`,
		lockName, nowUTC(),
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("lockholders.CountByNamedLock: %w", err)
	}
	return n, nil
}

func (s *lockHoldersImpl) ListByStoreScope(ctx context.Context, storeName string, tx persistence.Tx) ([]persistence.LockHolderRow, error) {
	if tx == nil {
		return nil, errors.New("lockholders.ListByStoreScope: persistence.Tx required")
	}
	rows, err := s.q(tx).QueryContext(ctx,
		`SELECT `+lockHolderCols+` FROM rimsky_lock_holders
		 WHERE lock_kind = 'scope' AND store_name = ?
		   AND expires_at > ?
		 ORDER BY claimed_at ASC`,
		storeName, nowUTC(),
	)
	if err != nil {
		return nil, fmt.Errorf("lockholders.ListByStoreScope: %w", err)
	}
	defer rows.Close()
	return collectLockHolders(rows)
}

func (s *lockHoldersImpl) DeleteIfExpired(ctx context.Context, id shared.UUID, supervisorID string, tx persistence.Tx) (bool, error) {
	if tx == nil {
		return false, errors.New("lockholders.DeleteIfExpired: persistence.Tx required")
	}
	res, err := s.q(tx).ExecContext(ctx,
		`DELETE FROM rimsky_lock_holders
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
func (s *lockHoldersImpl) ListForObservability(ctx context.Context, filter persistence.LockHolderListFilter, pag persistence.ListPagination, tx persistence.Tx) (persistence.PaginatedListResult[persistence.LockHolderRow], error) {
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
			return persistence.PaginatedListResult[persistence.LockHolderRow]{}, fmt.Errorf("lockholders.list: bad cursor: %w", err)
		}
		cursorClaimed = formatTime(c)
		cursorID = id.String()
	}
	rows, err := s.q(tx).QueryContext(ctx,
		`SELECT `+lockHolderCols+`
		   FROM rimsky_lock_holders lh
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
		return persistence.PaginatedListResult[persistence.LockHolderRow]{}, fmt.Errorf("lockholders.list: %w", err)
	}
	defer rows.Close()
	out, err := collectLockHolders(rows)
	if err != nil {
		return persistence.PaginatedListResult[persistence.LockHolderRow]{}, err
	}
	var nextCursor string
	if len(out) == limit && len(out) > 0 {
		last := out[len(out)-1]
		nextCursor = encodeLockHolderCursor(last.ClaimedAt, last.ID)
	}
	return persistence.PaginatedListResult[persistence.LockHolderRow]{Rows: out, NextCursor: nextCursor}, nil
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

func scanLockHolder(sc scannable) (persistence.LockHolderRow, error) {
	var (
		r                  persistence.LockHolderRow
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
	)
	if err := sc.Scan(
		&idStr, &kind,
		&lockName, &storeName, &scopeData, &address, &intent,
		&rws,
		&holderSupervisorID, &holderNodeIDStr,
		&claimedAtStr, &lastHeartbeatAtStr, &expiresAtStr, &frameIDStr,
	); err != nil {
		return persistence.LockHolderRow{}, err
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		return persistence.LockHolderRow{}, err
	}
	holderNodeID, err := uuid.Parse(holderNodeIDStr)
	if err != nil {
		return persistence.LockHolderRow{}, err
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
		return persistence.LockHolderRow{}, err
	}
	r.FrameID = frameID
	if r.ClaimedAt, err = parseTime(claimedAtStr); err != nil {
		return persistence.LockHolderRow{}, err
	}
	if r.LastHeartbeatAt, err = parseTime(lastHeartbeatAtStr); err != nil {
		return persistence.LockHolderRow{}, err
	}
	if r.ExpiresAt, err = parseTime(expiresAtStr); err != nil {
		return persistence.LockHolderRow{}, err
	}
	return r, nil
}

func collectLockHolders(rows *sql.Rows) ([]persistence.LockHolderRow, error) {
	var out []persistence.LockHolderRow
	for rows.Next() {
		r, err := scanLockHolder(rows)
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
