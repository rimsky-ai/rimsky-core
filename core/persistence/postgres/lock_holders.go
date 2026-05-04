// lock_holders.go is the postgres accessor for `rimsky_lock_holders`
// (v3 spec §12). Lifts the SQL from core/store/lockholders.go (which
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
// against rimsky_lock_holders carries `AND holder_supervisor_id =
// supervisor_id`. Stale orphan sweeps cannot null or delete live ownership.
//
// FrameID handling: LockHolderInsertInput carries an optional FrameID
// that is plumbed through into the postgres row. Per v3 spec §12, frame_id
// on rimsky_lock_holders is observability-only — no algorithm consults
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

	"github.com/fallguy/rimsky/core/persistence"
	"github.com/fallguy/rimsky/core/shared"
)

const lockHolderCols = `
  id, lock_kind, lock_name, store_name, region_data, address, intent,
  holder_supervisor_id, holder_node_id,
  claimed_at, last_heartbeat_at, expires_at, frame_id
`

// Insert writes a new lock-holder row inside the caller-provided
// transaction. The dispatch-acquisition transaction (§7.3) holds the tx;
// every per-spec lock-holder row is inserted via this method so the row
// commits atomically with the dispatch claim.
func (s *lockHoldersImpl) Insert(ctx context.Context, in persistence.LockHolderInsertInput, tx persistence.Tx) error {
	if tx == nil {
		return errors.New("lockholders.Insert: persistence.Tx required")
	}
	now := time.Now().UTC()
	_, err := s.q(tx).Exec(ctx,
		`INSERT INTO rimsky_lock_holders (
		   id, lock_kind, lock_name, store_name, region_data, address, intent,
		   holder_supervisor_id, holder_node_id,
		   claimed_at, last_heartbeat_at, expires_at, frame_id
		 ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
		in.ID, string(in.LockKind),
		in.LockName, in.StoreName,
		nullableJSONB(in.RegionData), nullableJSONB(in.Address),
		in.Intent,
		in.HolderSupervisorID, in.HolderNodeID,
		now, now, in.ExpiresAt, in.FrameID,
	)
	if err != nil {
		return fmt.Errorf("lockholders.Insert: %w", err)
	}
	return nil
}

// UpdateAddress sets the address column on an existing region-kind row.
// Claimant-guarded on supervisorID; mismatches are a no-op (returns nil).
func (s *lockHoldersImpl) UpdateAddress(
	ctx context.Context, id shared.UUID, supervisorID string, address json.RawMessage, tx persistence.Tx,
) error {
	if tx == nil {
		return errors.New("lockholders.UpdateAddress: persistence.Tx required")
	}
	_, err := s.q(tx).Exec(ctx,
		`UPDATE rimsky_lock_holders
		    SET address = $1
		  WHERE id = $2 AND holder_supervisor_id = $3`,
		nullableJSONB(address), id, supervisorID,
	)
	if err != nil {
		return fmt.Errorf("lockholders.UpdateAddress: %w", err)
	}
	return nil
}

// UpdateRegion sets the region_data column on an existing region-kind
// row. Claimant-guarded on supervisorID; mismatches are a no-op
// (returns nil).
func (s *lockHoldersImpl) UpdateRegion(
	ctx context.Context, id shared.UUID, supervisorID string, region json.RawMessage, tx persistence.Tx,
) error {
	if tx == nil {
		return errors.New("lockholders.UpdateRegion: persistence.Tx required")
	}
	_, err := s.q(tx).Exec(ctx,
		`UPDATE rimsky_lock_holders
		    SET region_data = $1
		  WHERE id = $2 AND holder_supervisor_id = $3`,
		nullableJSONB(region), id, supervisorID,
	)
	if err != nil {
		return fmt.Errorf("lockholders.UpdateRegion: %w", err)
	}
	return nil
}

// Get returns the row identified by id, or (nil, nil) when no row exists.
func (s *lockHoldersImpl) Get(ctx context.Context, id shared.UUID, tx persistence.Tx) (*persistence.LockHolderRow, error) {
	row := s.q(tx).QueryRow(ctx,
		`SELECT `+lockHolderCols+` FROM rimsky_lock_holders WHERE id = $1`, id,
	)
	out, err := scanLockHolder(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &out, nil
}

// LockForUpdate runs SELECT ... FOR UPDATE on the lock-holder row. Used
// by core/supervisor/auto_terminal.go to serialize auto-terminal
// resolution per @blessed-invariant 13.
func (s *lockHoldersImpl) LockForUpdate(ctx context.Context, id shared.UUID, tx persistence.Tx) (*persistence.LockHolderRow, error) {
	if tx == nil {
		return nil, errors.New("lockholders.LockForUpdate: persistence.Tx required")
	}
	row := s.q(tx).QueryRow(ctx,
		`SELECT `+lockHolderCols+` FROM rimsky_lock_holders WHERE id = $1 FOR UPDATE`, id,
	)
	out, err := scanLockHolder(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("lockholders.LockForUpdate: %w", err)
	}
	return &out, nil
}

// ListByHolderNode returns every row anchored to holderNodeID.
func (s *lockHoldersImpl) ListByHolderNode(ctx context.Context, holderNodeID shared.UUID, tx persistence.Tx) ([]persistence.LockHolderRow, error) {
	rows, err := s.q(tx).Query(ctx,
		`SELECT `+lockHolderCols+` FROM rimsky_lock_holders
		 WHERE holder_node_id = $1
		 ORDER BY claimed_at ASC`, holderNodeID,
	)
	if err != nil {
		return nil, fmt.Errorf("lockholders.ListByHolderNode: %w", err)
	}
	defer rows.Close()
	return collectLockHolders(rows)
}

// ListBySupervisor returns every row owned by supervisorID.
func (s *lockHoldersImpl) ListBySupervisor(ctx context.Context, supervisorID string, tx persistence.Tx) ([]persistence.LockHolderRow, error) {
	rows, err := s.q(tx).Query(ctx,
		`SELECT `+lockHolderCols+` FROM rimsky_lock_holders
		 WHERE holder_supervisor_id = $1
		 ORDER BY claimed_at ASC`, supervisorID,
	)
	if err != nil {
		return nil, fmt.Errorf("lockholders.ListBySupervisor: %w", err)
	}
	defer rows.Close()
	return collectLockHolders(rows)
}

// ExtendHeartbeat updates last_heartbeat_at and expires_at for every row
// owned by supervisorID whose lifetime should currently be active. Per
// spec §7.5; the heartbeat keeps lock-holder rows alive against the
// orphan reaper.
func (s *lockHoldersImpl) ExtendHeartbeat(ctx context.Context, supervisorID string, expiresAt time.Time, tx persistence.Tx) error {
	heartbeatSeconds := int(time.Until(expiresAt).Seconds())
	if heartbeatSeconds < 1 {
		heartbeatSeconds = 1
	}
	_, err := s.q(tx).Exec(ctx,
		`UPDATE rimsky_lock_holders lh
		   SET last_heartbeat_at = now(),
		       expires_at = now() + ($2 * interval '1 second')
		 WHERE lh.holder_supervisor_id = $1
		   AND (
		        lh.holder_node_id IN (
		            SELECT id FROM rimsky_nodes
		             WHERE assigned_supervisor_id = $1 AND state = 'running'
		        )
		        OR EXISTS (
		            SELECT 1 FROM rimsky_claim_holders ch
		              JOIN rimsky_nodes n ON n.id = ch.holder_node_id
		             WHERE ch.lock_holder_id = lh.id
		               AND ch.state = 'active'
		               AND n.state = 'running'
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
func (s *lockHoldersImpl) ListExpired(ctx context.Context, tx persistence.Tx) ([]persistence.LockHolderRow, error) {
	rows, err := s.q(tx).Query(ctx,
		`SELECT `+lockHolderCols+` FROM rimsky_lock_holders
		 WHERE expires_at < now()
		 ORDER BY expires_at ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("lockholders.ListExpired: %w", err)
	}
	defer rows.Close()
	return collectLockHolders(rows)
}

// Delete removes the row keyed by id. Claimant-guarded on
// expectedSupervisorID; mismatch is a no-op (returns nil).
func (s *lockHoldersImpl) Delete(ctx context.Context, id shared.UUID, expectedSupervisorID string, tx persistence.Tx) error {
	if tx == nil {
		return errors.New("lockholders.Delete: persistence.Tx required")
	}
	_, err := s.q(tx).Exec(ctx,
		`DELETE FROM rimsky_lock_holders
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
func (s *lockHoldersImpl) CountByNamedLock(ctx context.Context, lockName string, tx persistence.Tx) (int, error) {
	if tx == nil {
		return 0, errors.New("lockholders.CountByNamedLock: persistence.Tx required (advisory lock must be held)")
	}
	var n int
	err := s.q(tx).QueryRow(ctx,
		`SELECT count(*) FROM rimsky_lock_holders
		 WHERE lock_kind = 'named' AND lock_name = $1
		   AND expires_at > now()`,
		lockName,
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("lockholders.CountByNamedLock: %w", err)
	}
	return n, nil
}

// ListByStoreRegion returns every unexpired region-kind row for
// storeName. Used by the supervisor's region-conflict re-check (§7.3
// step 4a/4b).
func (s *lockHoldersImpl) ListByStoreRegion(ctx context.Context, storeName string, tx persistence.Tx) ([]persistence.LockHolderRow, error) {
	if tx == nil {
		return nil, errors.New("lockholders.ListByStoreRegion: persistence.Tx required")
	}
	rows, err := s.q(tx).Query(ctx,
		`SELECT `+lockHolderCols+` FROM rimsky_lock_holders
		 WHERE lock_kind = 'region' AND store_name = $1
		   AND expires_at > now()
		 ORDER BY claimed_at ASC`,
		storeName,
	)
	if err != nil {
		return nil, fmt.Errorf("lockholders.ListByStoreRegion: %w", err)
	}
	defer rows.Close()
	return collectLockHolders(rows)
}

// DeleteIfExpired removes the row keyed by id, claimant-guarded on
// supervisor_id AND only when expires_at is still in the past. Used by
// the orphan reaper. Returns deleted=true on success; false on no-op
// (claimant mismatch or fresh heartbeat extended the row).
func (s *lockHoldersImpl) DeleteIfExpired(ctx context.Context, id shared.UUID, supervisorID string, tx persistence.Tx) (bool, error) {
	if tx == nil {
		return false, errors.New("lockholders.DeleteIfExpired: persistence.Tx required")
	}
	tag, err := s.q(tx).Exec(ctx,
		`DELETE FROM rimsky_lock_holders
		 WHERE id = $1
		   AND holder_supervisor_id = $2
		   AND expires_at < now()`,
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
func (s *lockHoldersImpl) ListForObservability(ctx context.Context, filter persistence.LockHolderListFilter, pag persistence.ListPagination, tx persistence.Tx) (persistence.PaginatedListResult[persistence.LockHolderRow], error) {
	limit := pag.Limit
	if limit <= 0 {
		limit = 50
	}
	var storeArg, supArg, nodeTypeArg any
	var nodeArg, instArg any
	if filter.StoreName != "" {
		storeArg = filter.StoreName
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
			return persistence.PaginatedListResult[persistence.LockHolderRow]{}, fmt.Errorf("lockholders.list: bad cursor: %w", err)
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
		   FROM rimsky_lock_holders lh
		  WHERE ($1::text IS NULL OR lh.store_name = $1)
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
		storeArg, supArg, nodeArg, instArg, nodeTypeArg, cArg, cIDArg, limit,
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

// ---- helpers ----

func scanLockHolder(sc scannable) (persistence.LockHolderRow, error) {
	var (
		r          persistence.LockHolderRow
		kind       string
		lockName   *string
		storeName  *string
		regionData []byte
		address    []byte
		intent     *string
		frameID    *shared.UUID
	)
	if err := sc.Scan(
		&r.ID, &kind,
		&lockName, &storeName, &regionData, &address, &intent,
		&r.HolderSupervisorID, &r.HolderNodeID,
		&r.ClaimedAt, &r.LastHeartbeatAt, &r.ExpiresAt, &frameID,
	); err != nil {
		return persistence.LockHolderRow{}, err
	}
	r.LockKind = persistence.LockKind(kind)
	r.LockName = lockName
	r.StoreName = storeName
	r.RegionData = regionData
	r.Address = address
	r.Intent = intent
	r.FrameID = frameID
	return r, nil
}

func collectLockHolders(rows pgx.Rows) ([]persistence.LockHolderRow, error) {
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

// nullableJSONB returns nil for an empty/nil RawMessage so the JSONB
// column gets a SQL NULL rather than the literal string "null".
func nullableJSONB(b json.RawMessage) any {
	if len(b) == 0 {
		return nil
	}
	return []byte(b)
}
