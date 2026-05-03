// lock_holders.go — SQLite-backed persistence.LockHoldersStore.
//
// @blessed-invariant 9a: lock state lives only in the persistence layer.
// @blessed-invariant 4: claimant-guarded release.
package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/fallguy/rimsky/core/persistence"
	"github.com/fallguy/rimsky/core/shared"
)

const lockHolderCols = `
  id, lock_kind, lock_name, store_name, region_data, address, intent,
  holder_supervisor_id, holder_node_id,
  claimed_at, last_heartbeat_at, expires_at, frame_id
`

func (s *lockHoldersImpl) Insert(ctx context.Context, in persistence.LockHolderInsertInput, tx persistence.Tx) error {
	if tx == nil {
		return errors.New("lockholders.Insert: persistence.Tx required")
	}
	now := nowUTC()
	_, err := s.q(tx).ExecContext(ctx,
		`INSERT INTO rimsky_lock_holders (
		   id, lock_kind, lock_name, store_name, region_data, address, intent,
		   holder_supervisor_id, holder_node_id,
		   claimed_at, last_heartbeat_at, expires_at, frame_id
		 ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		in.ID.String(), string(in.LockKind),
		in.LockName, in.StoreName,
		nullableJSONB(in.RegionData), nullableJSONB(in.Address),
		in.Intent,
		in.HolderSupervisorID, in.HolderNodeID.String(),
		now, now, formatTime(in.ExpiresAt), nullableUUID(in.FrameID),
	)
	if err != nil {
		return fmt.Errorf("lockholders.Insert: %w", err)
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

func (s *lockHoldersImpl) UpdateRegion(
	ctx context.Context, id shared.UUID, supervisorID string, region json.RawMessage, tx persistence.Tx,
) error {
	if tx == nil {
		return errors.New("lockholders.UpdateRegion: persistence.Tx required")
	}
	_, err := s.q(tx).ExecContext(ctx,
		`UPDATE rimsky_lock_holders
		    SET region_data = ?
		  WHERE id = ? AND holder_supervisor_id = ?`,
		nullableJSONB(region), id.String(), supervisorID,
	)
	if err != nil {
		return fmt.Errorf("lockholders.UpdateRegion: %w", err)
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

func (s *lockHoldersImpl) ListByStoreRegion(ctx context.Context, storeName string, tx persistence.Tx) ([]persistence.LockHolderRow, error) {
	if tx == nil {
		return nil, errors.New("lockholders.ListByStoreRegion: persistence.Tx required")
	}
	rows, err := s.q(tx).QueryContext(ctx,
		`SELECT `+lockHolderCols+` FROM rimsky_lock_holders
		 WHERE lock_kind = 'region' AND store_name = ?
		   AND expires_at > ?
		 ORDER BY claimed_at ASC`,
		storeName, nowUTC(),
	)
	if err != nil {
		return nil, fmt.Errorf("lockholders.ListByStoreRegion: %w", err)
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

func scanLockHolder(sc scannable) (persistence.LockHolderRow, error) {
	var (
		r                  persistence.LockHolderRow
		idStr              string
		kind               string
		lockName           sql.NullString
		storeName          sql.NullString
		regionData         sql.NullString
		address            sql.NullString
		intent             sql.NullString
		holderSupervisorID string
		holderNodeIDStr    string
		claimedAtStr       string
		lastHeartbeatAtStr string
		expiresAtStr       string
		frameIDStr         sql.NullString
	)
	if err := sc.Scan(
		&idStr, &kind,
		&lockName, &storeName, &regionData, &address, &intent,
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
	if regionData.Valid {
		r.RegionData = json.RawMessage(regionData.String)
	}
	if address.Valid {
		r.Address = json.RawMessage(address.String)
	}
	if intent.Valid {
		v := intent.String
		r.Intent = &v
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
