// Package postgres is the Postgres implementation of storage.StorageBackend.
//
// One factory type (PostgresStorageBackend) wraps a *pgxpool.Pool and returns
// the per-table sub-store implementations + a Transaction helper. Each store
// method accepts an optional storage.Tx which, when non-nil, is unwrapped to
// a pgx.Tx via the internal *pgTx carrier; when nil, the store falls back to
// the pool for auto-commit queries.
//
// LockHolders is a special case: the concrete row-level helpers live in
// core/store/lockholders.go (the lock-holder mechanism is part of the
// unified store package's surface — carried forward unchanged in v3 per
// §14). The backend exposes a thin
// adapter (LockHoldersStore) that satisfies storage.LockHoldersStore by
// delegating to *store.LockHoldersClient. Callers that need the helpers not
// represented in the storage interface (RefreshHeartbeat,
// ExtendHeartbeatForRunningNodes, ListByStoreRegion, CountByNamedLock)
// reach into the adapter via its Client() method.
package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/fallguy/rimsky/core/storage"
	"github.com/fallguy/rimsky/core/store"
)

// PostgresStorageBackend is the concrete implementation of
// storage.StorageBackend for Postgres.
type PostgresStorageBackend struct {
	pool *pgxpool.Pool

	templates      *TemplateStore
	templateTags   *TemplateTagsStore
	instances      *InstanceStore
	storeLifecycle *StoreLifecycleStore
	nodes          *NodeStore
	lockHolders    *LockHoldersStore
	nodeAttributes *NodeAttributesStore
	claimHolders   *ClaimHoldersStore
	events         *EventStore
	schedules      *ScheduleStore
	supervisors    *SupervisorStore
}

// New returns a PostgresStorageBackend bound to the given pool. The pool must
// already point at a database where rimsky migrations have been applied.
func New(pool *pgxpool.Pool) *PostgresStorageBackend {
	b := &PostgresStorageBackend{pool: pool}
	b.templates = &TemplateStore{pool: pool}
	b.templateTags = &TemplateTagsStore{pool: pool}
	b.instances = &InstanceStore{pool: pool}
	b.storeLifecycle = &StoreLifecycleStore{pool: pool}
	b.nodes = &NodeStore{pool: pool}
	b.lockHolders = &LockHoldersStore{pool: pool, client: store.NewLockHoldersClient(pool)}
	b.nodeAttributes = &NodeAttributesStore{pool: pool}
	b.claimHolders = &ClaimHoldersStore{pool: pool}
	b.events = &EventStore{pool: pool}
	b.schedules = &ScheduleStore{pool: pool}
	b.supervisors = &SupervisorStore{pool: pool}
	return b
}

var _ storage.StorageBackend = (*PostgresStorageBackend)(nil)

// Templates returns the template accessor.
func (b *PostgresStorageBackend) Templates() storage.TemplateStore { return b.templates }

// TemplateTags returns the template-tags accessor.
func (b *PostgresStorageBackend) TemplateTags() storage.TemplateTagsStore { return b.templateTags }

// Instances returns the instance accessor.
func (b *PostgresStorageBackend) Instances() storage.InstanceStore { return b.instances }

// StoreLifecycle returns the rimsky_store_lifecycle accessor.
func (b *PostgresStorageBackend) StoreLifecycle() storage.StoreLifecycleStore {
	return b.storeLifecycle
}

// Nodes returns the node accessor.
func (b *PostgresStorageBackend) Nodes() storage.NodeStore { return b.nodes }

// LockHolders returns the rimsky_lock_holders accessor (storage-interface
// surface). The concrete row-level helpers live on the underlying
// *store.LockHoldersClient and are reachable via LockHoldersClient().
func (b *PostgresStorageBackend) LockHolders() storage.LockHoldersStore {
	return b.lockHolders
}

// LockHoldersClient returns the core/store layer client. Provided so the
// supervisor's runner and the scheduler's sweepers can use the helpers
// (RefreshHeartbeat, ListByStoreRegion, CountByNamedLock, …) that don't
// fit the storage.LockHoldersStore interface.
func (b *PostgresStorageBackend) LockHoldersClient() *store.LockHoldersClient {
	return b.lockHolders.Client()
}

// NodeAttributes returns the rimsky_node_attributes accessor.
func (b *PostgresStorageBackend) NodeAttributes() storage.NodeAttributesStore {
	return b.nodeAttributes
}

// ClaimHolders returns the rimsky_claim_holders accessor.
func (b *PostgresStorageBackend) ClaimHolders() storage.ClaimHoldersStore {
	return b.claimHolders
}

// Events returns the events accessor.
func (b *PostgresStorageBackend) Events() storage.EventStore { return b.events }

// Schedules returns the schedule accessor.
func (b *PostgresStorageBackend) Schedules() storage.ScheduleStore { return b.schedules }

// Supervisors returns the supervisor accessor.
func (b *PostgresStorageBackend) Supervisors() storage.SupervisorStore { return b.supervisors }

// Transaction runs fn inside a pgx transaction. On return, the transaction
// is committed; on any error (including a panic), it is rolled back.
func (b *PostgresStorageBackend) Transaction(
	ctx context.Context,
	fn func(ctx context.Context, tx storage.Tx) error,
) (retErr error) {
	pgT, err := b.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() {
		if p := recover(); p != nil {
			_ = pgT.Rollback(ctx)
			panic(p)
		}
	}()
	if err := fn(ctx, &pgTx{tx: pgT}); err != nil {
		_ = pgT.Rollback(ctx)
		return err
	}
	return pgT.Commit(ctx)
}

// pgTx is the concrete storage.Tx carrier. It embeds storage.TxMarker so it
// satisfies storage.Tx; stores cast back to *pgTx to extract the underlying
// pgx.Tx.
type pgTx struct {
	storage.TxMarker
	tx pgx.Tx
}

// WrapPgxTx exposes a pgx.Tx as a storage.Tx so callers that already own
// the underlying transaction (for example the supervisor's runner, which
// drives the queue + lock-holder helpers directly on a pgx.Tx) can also
// run storage.* calls inside the same transaction. Returns nil if tx is
// nil so the helper composes with the storage.Tx == nil convention
// (auto-commit on the pool).
func WrapPgxTx(tx pgx.Tx) storage.Tx {
	if tx == nil {
		return nil
	}
	return &pgTx{tx: tx}
}

// PgxTxFromStorage is the inverse of WrapPgxTx: it unwraps a storage.Tx
// to its underlying pgx.Tx. Returns (nil, nil) when stx is nil. Useful
// for callers that hold a storage.Tx but need to run pgx-level helpers
// (e.g. the supervisor runner's per-store SQL) inside the same
// transaction.
func PgxTxFromStorage(stx storage.Tx) (pgx.Tx, error) {
	return pgxTxFromStorage(stx)
}

// querier is the common query surface shared by *pgxpool.Pool and pgx.Tx.
// All store SQL goes through this interface so the same code path serves
// both auto-commit (pool) and transactional (tx) execution.
type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// q returns a querier — pool when tx is nil, otherwise the tx carrier.
// The returned object is the minimum surface each store needs; the actual
// pgx.Exec / pgx.Query / pgx.QueryRow signatures are compatible between
// *pgxpool.Pool and pgx.Tx.
func q(tx storage.Tx, pool *pgxpool.Pool) querier {
	if tx == nil {
		return pool
	}
	return tx.(*pgTx).tx
}
