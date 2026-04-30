// LockHolders postgres helpers for `rimsky_lock_holders` (v3 spec §12).
//
// Lives in core/store/ rather than core/storage/postgres/ because the
// lock-holder table is the unified mechanism that ties stores to the
// supervisor and scheduler subsystems. Concrete database access lives
// here; the storage-layer interface (storage.LockHoldersStore) is
// satisfied by a thin adapter in core/storage/postgres/lock_holders.go.
//
// @blessed-invariant 9a: lock state lives only in postgres.
//
//	No store implementation persists lock state. The question
//	"is anyone holding lock X" is answered exclusively by the rows
//	managed in this file. (Spec §4.10 — carries forward unchanged
//	from v2 §4.10 invariant 9a.)
//
// Imports are pgx/v5 + core/shared only.

package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/fallguy/rimsky/core/shared"
)

// LockHolderKind discriminates a lock-holder row's payload columns.
// Two kinds: 'named' (named-lock primitive) and 'region' (claim primitive).
// The prior 'claim' kind dissolved — pick-policy claims are 'region' rows
// with substrate-chosen region_data.
type LockHolderKind string

// Lock-holder kinds.
const (
	LockHolderKindNamed  LockHolderKind = "named"
	LockHolderKindRegion LockHolderKind = "region"
)

// LockHolderRow mirrors a row of `rimsky_lock_holders`.
//
// Exactly one of (LockName) / (StoreName + RegionData + Intent) is
// populated, keyed by Kind. A CHECK constraint enforces this on the
// database side.
//
// Address is substrate-supplied bytes from Open(); written into the row
// after Open returns successfully (within the same acquisition tx).
// May be nil for region rows mid-acquisition; populated by terminal time.
//
// ID is generated client-side by the supervisor before Insert — same
// UUID is passed to Store.Open as the claim_id (spec §4.2). The column
// default `gen_random_uuid()` stays as a safety net but the supervisor
// MUST supply the id explicitly.
type LockHolderRow struct {
	ID                 shared.UUID
	Kind               LockHolderKind
	LockName           *string
	StoreName          *string
	RegionData         json.RawMessage // opaque bytes; substrate's region identifier
	Address            json.RawMessage // opaque bytes; substrate-supplied from Open
	Intent             *string         // 'r' | 'rw' for region rows; nil for named
	HolderSupervisorID string
	HolderNodeID       shared.UUID
	ClaimedAt          time.Time
	LastHeartbeatAt    time.Time
	ExpiresAt          time.Time
	// FrameID is observability-only (v3 spec §12): records which frame
	// the dispatch row carried at acquire time. Reads/sweeps and the
	// auto-terminal algorithm do NOT consult this field.
	FrameID *shared.UUID
}

// LockHoldersClient is the database-facing helper for `rimsky_lock_holders`.
//
// Construct one via NewLockHoldersClient(pool); helpers either accept an
// open pgx.Tx (for in-acquisition or in-release writes that must
// participate in the supervisor's outer transaction) or take the pool
// directly via the embedded *pgxpool.Pool (for sweep-style reads and the
// heartbeat tick that owns its own transaction).
type LockHoldersClient struct {
	pool *pgxpool.Pool
}

// NewLockHoldersClient returns a LockHoldersClient bound to the given
// pool. The pool must already point at a database where rimsky migrations
// have been applied.
func NewLockHoldersClient(pool *pgxpool.Pool) *LockHoldersClient {
	return &LockHoldersClient{pool: pool}
}

// Pool returns the underlying connection pool. Callers that need to run
// pool-scoped queries (e.g. heartbeat ticks, sweeps) use this rather than
// reaching into the unexported field.
func (c *LockHoldersClient) Pool() *pgxpool.Pool { return c.pool }

const lockHolderCols = `
  id, lock_kind, lock_name, store_name, region_data, address, intent,
  holder_supervisor_id, holder_node_id,
  claimed_at, last_heartbeat_at, expires_at, frame_id
`

// Insert writes a new lock-holder row inside the caller-provided
// transaction. The dispatch-acquisition transaction (§7.3) holds the tx;
// every per-spec lock-holder row is inserted via this method so the row
// commits atomically with the dispatch claim.
//
// For region rows the supervisor inserts with Address = nil; the §7.3
// step-4e UpdateAddress call writes the address after Open returns.
func (c *LockHoldersClient) Insert(ctx context.Context, tx pgx.Tx, row LockHolderRow) error {
	if tx == nil {
		return errors.New("lockholders.Insert: tx required")
	}
	_, err := tx.Exec(ctx,
		`INSERT INTO rimsky_lock_holders (
		   id, lock_kind, lock_name, store_name, region_data, address, intent,
		   holder_supervisor_id, holder_node_id,
		   claimed_at, last_heartbeat_at, expires_at, frame_id
		 ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
		row.ID, string(row.Kind),
		row.LockName, row.StoreName,
		nullableJSONB(row.RegionData), nullableJSONB(row.Address),
		row.Intent,
		row.HolderSupervisorID, row.HolderNodeID,
		row.ClaimedAt, row.LastHeartbeatAt, row.ExpiresAt, row.FrameID,
	)
	if err != nil {
		return fmt.Errorf("lockholders.Insert: %w", err)
	}
	return nil
}

// UpdateAddress sets the address column on an existing region-kind row.
// Used by the supervisor's §7.3 step-4e flow: after Store.Open returns
// successfully, the supervisor writes the returned address to the row
// inside the same acquisition tx. Claimant-guarded on supervisorID;
// mismatches are a no-op (returns nil, no error).
func (c *LockHoldersClient) UpdateAddress(
	ctx context.Context, tx pgx.Tx, id shared.UUID, supervisorID string, address json.RawMessage,
) error {
	if tx == nil {
		return errors.New("lockholders.UpdateAddress: tx required")
	}
	_, err := tx.Exec(ctx,
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

// Get returns the row identified by id, or (nil, nil) when no row exists.
func (c *LockHoldersClient) Get(ctx context.Context, id shared.UUID) (*LockHolderRow, error) {
	row := c.pool.QueryRow(ctx,
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

// DeleteByID removes the row keyed by id, claimant-guarded on
// supervisor_id. A mismatch is a no-op (returns nil) — the orphan-reap
// sweep cannot null out a live claim owned by a different supervisor.
//
// Cascade FK on rimsky_claim_holders.lock_holder_id cleans up any
// claim-holder rows for held claims when the lock-holder row is deleted.
//
// This unconditional variant is for the supervisor's terminal flow,
// where the row is being released by its owning supervisor as part of
// normal completion — the expires_at may be in the future and that's
// fine. The orphan reaper uses DeleteIfExpired instead so a fresh
// heartbeat refresh in the reap window cannot lose a live row.
func (c *LockHoldersClient) DeleteByID(ctx context.Context, tx pgx.Tx, id shared.UUID, supervisorID string) error {
	if tx == nil {
		return errors.New("lockholders.DeleteByID: tx required")
	}
	_, err := tx.Exec(ctx,
		`DELETE FROM rimsky_lock_holders
		 WHERE id = $1 AND holder_supervisor_id = $2`,
		id, supervisorID,
	)
	if err != nil {
		return fmt.Errorf("lockholders.DeleteByID: %w", err)
	}
	return nil
}

// DeleteIfExpired removes the row keyed by id, claimant-guarded on
// supervisor_id AND only when expires_at is still in the past. The
// expires_at predicate closes the window where a fresh heartbeat tick
// refreshes a row between the reaper's ListExpired and DeleteByID
// calls — without it, a live (newly heartbeat-extended) row could be
// reaped.
//
// Used by the orphan reaper. Returns (deleted=false, nil) on no-op
// (claimant mismatch or row was freshly heartbeat-extended). Callers
// use the boolean to suppress false-positive `lock_orphan_reaped`
// telemetry when the reaper lost the race.
func (c *LockHoldersClient) DeleteIfExpired(ctx context.Context, tx pgx.Tx, id shared.UUID, supervisorID string) (bool, error) {
	if tx == nil {
		return false, errors.New("lockholders.DeleteIfExpired: tx required")
	}
	tag, err := tx.Exec(ctx,
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

// RefreshHeartbeat updates `last_heartbeat_at` and `expires_at` for every
// row owned by `supervisorID` whose lifetime should currently be active.
// Two cases (spec §7.5 — the heartbeat keeps lock-holder rows alive
// against the orphan reaper):
//
//   - Standard: holder_node_id is currently 'running' on this supervisor.
//   - Held-claim: an active rimsky_claim_holders row referencing this
//     lock-holder has its node currently 'running' on any supervisor.
//
// `heartbeatSeconds` is the value of `5 × heartbeat_interval_seconds`.
func (c *LockHoldersClient) RefreshHeartbeat(ctx context.Context, supervisorID string, heartbeatSeconds int) error {
	_, err := c.pool.Exec(ctx,
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
		return fmt.Errorf("lockholders.RefreshHeartbeat: %w", err)
	}
	return nil
}

// ListExpired returns rows whose `expires_at < now()`. The scheduler's
// orphan-reap sweep (§7.5) iterates these and deletes each row
// claimant-guarded on supervisor_id (no substrate verb fired in v3 —
// the store's TTL handles its internal state).
func (c *LockHoldersClient) ListExpired(ctx context.Context) ([]LockHolderRow, error) {
	rows, err := c.pool.Query(ctx,
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

// ListByHolderNode returns every row anchored to `holderNodeID`. Used by
// the supervisor at terminal-release time to walk the per-node lock set.
func (c *LockHoldersClient) ListByHolderNode(ctx context.Context, holderNodeID shared.UUID) ([]LockHolderRow, error) {
	rows, err := c.pool.Query(ctx,
		`SELECT `+lockHolderCols+` FROM rimsky_lock_holders
		 WHERE holder_node_id = $1
		 ORDER BY claimed_at ASC`,
		holderNodeID,
	)
	if err != nil {
		return nil, fmt.Errorf("lockholders.ListByHolderNode: %w", err)
	}
	defer rows.Close()
	return collectLockHolders(rows)
}

// ListBySupervisor returns every row owned by `supervisorID`. Used by the
// supervisor's startup-recovery path to surface still-held locks at
// process restart.
func (c *LockHoldersClient) ListBySupervisor(ctx context.Context, supervisorID string) ([]LockHolderRow, error) {
	rows, err := c.pool.Query(ctx,
		`SELECT `+lockHolderCols+` FROM rimsky_lock_holders
		 WHERE holder_supervisor_id = $1
		 ORDER BY claimed_at ASC`,
		supervisorID,
	)
	if err != nil {
		return nil, fmt.Errorf("lockholders.ListBySupervisor: %w", err)
	}
	defer rows.Close()
	return collectLockHolders(rows)
}

// CountByNamedLock returns the number of unexpired rows held against
// `lockName`. Used under `pg_advisory_xact_lock(hashtext('rimsky_lock:'
// || lockName))` to enforce the named-lock limit.
func (c *LockHoldersClient) CountByNamedLock(ctx context.Context, tx pgx.Tx, lockName string) (int, error) {
	if tx == nil {
		return 0, errors.New("lockholders.CountByNamedLock: tx required (advisory lock must be held)")
	}
	var n int
	err := tx.QueryRow(ctx,
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
// `storeName`. Used by the supervisor's region-conflict re-check
// (§7.3 step 4a/4b) and by the queue's eligibility predicate.
func (c *LockHoldersClient) ListByStoreRegion(ctx context.Context, tx pgx.Tx, storeName string) ([]LockHolderRow, error) {
	if tx == nil {
		return nil, errors.New("lockholders.ListByStoreRegion: tx required")
	}
	rows, err := tx.Query(ctx,
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

// ExtendHeartbeatForRunningNodes is an alias path used by code that
// already has its own pgx.Tx (e.g. the heartbeat tick driving multiple
// updates inside one transaction). Same predicate as RefreshHeartbeat
// (per spec §7.5 — keeps lock-holder rows alive against the orphan
// reaper while the holder node is still `running`).
func (c *LockHoldersClient) ExtendHeartbeatForRunningNodes(
	ctx context.Context, tx pgx.Tx, supervisorID string, heartbeatSeconds int,
) error {
	if tx == nil {
		return errors.New("lockholders.ExtendHeartbeatForRunningNodes: tx required")
	}
	_, err := tx.Exec(ctx,
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
		return fmt.Errorf("lockholders.ExtendHeartbeatForRunningNodes: %w", err)
	}
	return nil
}

// ---- helpers ----

// scannableLockHolder narrows pgx.Row / pgx.Rows down to the Scan signature
// shared by both. Defined locally because core/store cannot depend on
// core/storage's helper.
type scannableLockHolder interface {
	Scan(dst ...any) error
}

func scanLockHolder(sc scannableLockHolder) (LockHolderRow, error) {
	var (
		r          LockHolderRow
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
		return LockHolderRow{}, err
	}
	r.Kind = LockHolderKind(kind)
	r.LockName = lockName
	r.StoreName = storeName
	r.RegionData = regionData
	r.Address = address
	r.Intent = intent
	r.FrameID = frameID
	return r, nil
}

func collectLockHolders(rows pgx.Rows) ([]LockHolderRow, error) {
	var out []LockHolderRow
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
