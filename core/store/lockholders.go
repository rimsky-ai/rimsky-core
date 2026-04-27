// LockHolders postgres helpers for `rimsky_lock_holders` (spec §9.9.2).
//
// Lives in core/store/ rather than core/storage/postgres/ because the
// lock-holder table is the unified mechanism that ties stores to the
// supervisor and scheduler subsystems (per spec §16.1). Concrete database
// access lives here; the storage-layer interface (storage.LockHoldersStore)
// is satisfied by a thin adapter in core/storage/postgres/lock_holders.go.
//
// @blessed-invariant 9: lock state lives only in postgres.
//
//	No store implementation persists lock state. The question
//	"is anyone holding lock X" is answered exclusively by the rows
//	managed in this file. (Spec §18 invariant 9; spec §5.3.) A
//	scenario test exercises this invariant; do not add store-side
//	lock-state caches that would violate it.
//
// Imports are pgx/v5 + core/shared only. Importing core/storage from this
// file is forbidden by the package layout rule (the layout rule preserves
// the layering: core/storage may depend on core/store, never the other
// way).
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/fallguy/rimsky/core/shared"
)

// LockHolderKind discriminates a lock-holder row's payload columns. See
// spec §9.9.2.
type LockHolderKind string

// Lock-holder kinds.
const (
	LockHolderKindNamed  LockHolderKind = "named"
	LockHolderKindRegion LockHolderKind = "region"
	LockHolderKindClaim  LockHolderKind = "claim"
)

// LockHolderRow mirrors a row of `rimsky_lock_holders` (spec §9.9.2).
//
// Exactly one of (LockName) / (StoreName, RegionData) / (StoreName,
// ClaimID) is populated, keyed by Kind. The §9.9.2 CHECK constraint
// enforces this on the database side.
type LockHolderRow struct {
	ID                 shared.UUID
	Kind               LockHolderKind
	LockName           *string
	StoreName          *string
	RegionData         []byte
	ClaimID            *string
	HolderSupervisorID string
	HolderNodeID       shared.UUID
	ClaimedAt          time.Time
	LastHeartbeatAt    time.Time
	ExpiresAt          time.Time
	// FrameID is observability-only (spec §10.4): it records which
	// frame the dispatch row carried at acquire time. Reads/sweeps and
	// the §5.6.4 resolution algorithm do NOT consult this field.
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
  id, lock_kind, lock_name, store_name, region_data, claim_id,
  holder_supervisor_id, holder_node_id,
  claimed_at, last_heartbeat_at, expires_at, frame_id
`

// Insert writes a new lock-holder row inside the caller-provided
// transaction. The dispatch-acquisition transaction (§13.3) holds the tx;
// every per-spec lock-holder row is inserted via this method so the row
// commits atomically with the dispatch claim.
func (c *LockHoldersClient) Insert(ctx context.Context, tx pgx.Tx, row LockHolderRow) error {
	if tx == nil {
		return errors.New("lockholders.Insert: tx required")
	}
	_, err := tx.Exec(ctx,
		`INSERT INTO rimsky_lock_holders (
		   id, lock_kind, lock_name, store_name, region_data, claim_id,
		   holder_supervisor_id, holder_node_id,
		   claimed_at, last_heartbeat_at, expires_at, frame_id
		 ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		row.ID, string(row.Kind),
		row.LockName, row.StoreName, row.RegionData, row.ClaimID,
		row.HolderSupervisorID, row.HolderNodeID,
		row.ClaimedAt, row.LastHeartbeatAt, row.ExpiresAt, row.FrameID,
	)
	if err != nil {
		return fmt.Errorf("lockholders.Insert: %w", err)
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

// RefreshHeartbeat updates `last_heartbeat_at` and `expires_at` for every
// row owned by `supervisorID` whose `holder_node_id` is currently
// `running` and assigned to the same supervisor. The `holder_node_id IN
// running-nodes` predicate is the §13.4 invariant that prevents
// preserve-for-resume rows from being refreshed (those rows are tied to
// nodes that have transitioned out of `running` to `stale`); without the
// filter the resume-grace cutoff (§13.6) would never fire.
//
// `heartbeatSeconds` is the value of `5 × heartbeat_interval_seconds` per
// spec §13.4.
func (c *LockHoldersClient) RefreshHeartbeat(ctx context.Context, supervisorID string, heartbeatSeconds int) error {
	_, err := c.pool.Exec(ctx,
		`UPDATE rimsky_lock_holders
		   SET last_heartbeat_at = now(),
		       expires_at = now() + ($2 * interval '1 second')
		 WHERE holder_supervisor_id = $1
		   AND holder_node_id IN (
		         SELECT id FROM rimsky_nodes
		          WHERE assigned_supervisor_id = $1
		            AND state = 'running'
		       )`,
		supervisorID, heartbeatSeconds,
	)
	if err != nil {
		return fmt.Errorf("lockholders.RefreshHeartbeat: %w", err)
	}
	return nil
}

// ListExpired returns rows whose `expires_at < now()`. The scheduler's
// lock-holder sweep iterates these (§13.5 step 2), runs store-side
// `ReleaseLock(give_up)` for `claim`-kind rows, then deletes the row
// claimant-guarded on supervisor_id.
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

// ListByNodeAndStore is the §13.3 step 3a rebind probe. For region/claim
// specs the supervisor checks whether a prior unexpired lock-holder row
// exists for the same (holder_node_id, store_name, holder_supervisor_id);
// if so, the acquisition path skips `Store.AcquireLock` and reuses the
// existing row.
//
// Only rows with `expires_at > now()` are returned — expired rows are the
// orphan-reap's responsibility.
func (c *LockHoldersClient) ListByNodeAndStore(
	ctx context.Context, nodeID shared.UUID, storeName, supervisorID string,
) ([]LockHolderRow, error) {
	rows, err := c.pool.Query(ctx,
		`SELECT `+lockHolderCols+` FROM rimsky_lock_holders
		 WHERE holder_node_id = $1
		   AND store_name = $2
		   AND holder_supervisor_id = $3
		   AND expires_at > now()
		 ORDER BY claimed_at ASC`,
		nodeID, storeName, supervisorID,
	)
	if err != nil {
		return nil, fmt.Errorf("lockholders.ListByNodeAndStore: %w", err)
	}
	defer rows.Close()
	return collectLockHolders(rows)
}

// RebindForResume is the §13.3 step 3a UPDATE. The supervisor has already
// found a prior lock-holder row owned by it for the same (node, store);
// this method extends that row's heartbeat + expiry to the running-node
// values, bypassing the §13.4 `holder_node_id IN running-nodes` filter
// (which would not match — at rebind time the node is still `stale`,
// having transitioned out of `running` at the prior commit).
//
// Returns the updated row. When the row vanished between the probe
// (ListByNodeAndStore) and the rebind UPDATE — or the supervisor_id
// no longer matches — the returned error wraps `pgx.ErrNoRows` via
// `fmt.Errorf("...: %w", err)`. Detect with `errors.Is(err, pgx.ErrNoRows)`,
// not `==`. The caller bails to the standard fresh-acquisition path on
// that match.
func (c *LockHoldersClient) RebindForResume(
	ctx context.Context, tx pgx.Tx, existingRowID shared.UUID, supervisorID string, heartbeatSeconds int,
) (LockHolderRow, error) {
	if tx == nil {
		return LockHolderRow{}, errors.New("lockholders.RebindForResume: tx required")
	}
	row := tx.QueryRow(ctx,
		`UPDATE rimsky_lock_holders
		    SET last_heartbeat_at = now(),
		        expires_at = now() + ($3 * interval '1 second')
		  WHERE id = $1 AND holder_supervisor_id = $2
		  RETURNING `+lockHolderCols,
		existingRowID, supervisorID, heartbeatSeconds,
	)
	out, err := scanLockHolder(row)
	if err != nil {
		return LockHolderRow{}, fmt.Errorf("lockholders.RebindForResume: %w", err)
	}
	return out, nil
}

// ListByHolderNode returns every row anchored to `holderNodeID`. Used by
// the supervisor at terminal-release time to walk the per-node lock set
// and by the scheduler's claim-holder GC for the §13.5 step 3 lookup.
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
// `lockName`. Used by the §13.3 step 3b advisory-locked recount: under
// `pg_advisory_xact_lock(hashtext('rimsky_lock:' || lockName))`, the
// supervisor counts current holders to enforce the named-lock limit.
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
// `storeName`. Used by the §13.3 step 3d region-conflict re-check: under
// the dispatch row's FOR UPDATE and the per-named-lock advisory locks,
// the supervisor re-loads existing region holders and re-evaluates
// `RegionsConflict`.
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
// updates inside one transaction). Callers that don't already have a tx
// should use RefreshHeartbeat which manages its own pool query.
func (c *LockHoldersClient) ExtendHeartbeatForRunningNodes(
	ctx context.Context, tx pgx.Tx, supervisorID string, heartbeatSeconds int,
) error {
	if tx == nil {
		return errors.New("lockholders.ExtendHeartbeatForRunningNodes: tx required")
	}
	_, err := tx.Exec(ctx,
		`UPDATE rimsky_lock_holders
		   SET last_heartbeat_at = now(),
		       expires_at = now() + ($2 * interval '1 second')
		 WHERE holder_supervisor_id = $1
		   AND holder_node_id IN (
		         SELECT id FROM rimsky_nodes
		          WHERE assigned_supervisor_id = $1
		            AND state = 'running'
		       )`,
		supervisorID, heartbeatSeconds,
	)
	if err != nil {
		return fmt.Errorf("lockholders.ExtendHeartbeatForRunningNodes: %w", err)
	}
	return nil
}

// PreserveForResume sets a row's `expires_at` to the resume-grace cutoff
// at terminal-release time (§13.6). `last_heartbeat_at` is not touched
// (the heartbeat tick stops refreshing once the node leaves `running`,
// per §13.4). On no row matched (already-deleted, wrong supervisor) the
// caller's outer release transaction proceeds without error.
func (c *LockHoldersClient) PreserveForResume(
	ctx context.Context, tx pgx.Tx, id shared.UUID, supervisorID string, resumeGraceSeconds int,
) error {
	if tx == nil {
		return errors.New("lockholders.PreserveForResume: tx required")
	}
	_, err := tx.Exec(ctx,
		`UPDATE rimsky_lock_holders
		   SET expires_at = now() + ($3 * interval '1 second')
		 WHERE id = $1 AND holder_supervisor_id = $2`,
		id, supervisorID, resumeGraceSeconds,
	)
	if err != nil {
		return fmt.Errorf("lockholders.PreserveForResume: %w", err)
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
		claimID    *string
		frameID    *shared.UUID
	)
	if err := sc.Scan(
		&r.ID, &kind,
		&lockName, &storeName, &regionData, &claimID,
		&r.HolderSupervisorID, &r.HolderNodeID,
		&r.ClaimedAt, &r.LastHeartbeatAt, &r.ExpiresAt, &frameID,
	); err != nil {
		return LockHolderRow{}, err
	}
	r.Kind = LockHolderKind(kind)
	r.LockName = lockName
	r.StoreName = storeName
	r.RegionData = regionData
	r.ClaimID = claimID
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
