// Package postgres is the Postgres implementation of storage.StorageBackend.
//
// Port of rimsky/src/storage/postgres/*.ts. Adapted for pgx/v5 and the Go
// port's cell→node rename (spec §11.1): the cell_* columns become node_*,
// the `kind` discriminator is replaced by a nullable `executor` column plus
// a nullable `schedule_cron` column, and the rimsky_timers table is replaced
// by rimsky_schedules keyed on node_id.
//
// One factory type (PostgresStorageBackend) wraps a *pgxpool.Pool and returns
// eight sub-store implementations + a Transaction helper. Each store method
// accepts an optional storage.Tx which, when non-nil, is unwrapped to a
// pgx.Tx via the internal *pgTx carrier; when nil, the store falls back to
// the pool for auto-commit queries.
package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/fallguy/rimsky/core/storage"
)

// PostgresStorageBackend is the concrete implementation of
// storage.StorageBackend for Postgres.
type PostgresStorageBackend struct {
	pool *pgxpool.Pool

	templates    *TemplateStore
	instances    *InstanceStore
	nodes        *NodeStore
	resources    *ResourceRegistry
	resourceData *ResourceDataStore
	events       *EventStore
	schedules    *ScheduleStore
	supervisors  *SupervisorStore
}

// New returns a PostgresStorageBackend bound to the given pool. The pool must
// already point at a database where rimsky migrations have been applied.
func New(pool *pgxpool.Pool) *PostgresStorageBackend {
	b := &PostgresStorageBackend{pool: pool}
	b.templates = &TemplateStore{pool: pool}
	b.instances = &InstanceStore{pool: pool}
	b.nodes = &NodeStore{pool: pool}
	b.resources = &ResourceRegistry{pool: pool}
	b.resourceData = &ResourceDataStore{pool: pool}
	b.events = &EventStore{pool: pool}
	b.schedules = &ScheduleStore{pool: pool}
	b.supervisors = &SupervisorStore{pool: pool}
	return b
}

var _ storage.StorageBackend = (*PostgresStorageBackend)(nil)

func (b *PostgresStorageBackend) Templates() storage.TemplateStore   { return b.templates }
func (b *PostgresStorageBackend) Instances() storage.InstanceStore   { return b.instances }
func (b *PostgresStorageBackend) Nodes() storage.NodeStore           { return b.nodes }
func (b *PostgresStorageBackend) Resources() storage.ResourceRegistry {
	return b.resources
}
func (b *PostgresStorageBackend) ResourceData() storage.ResourceDataStore {
	return b.resourceData
}
func (b *PostgresStorageBackend) Events() storage.EventStore           { return b.events }
func (b *PostgresStorageBackend) Schedules() storage.ScheduleStore     { return b.schedules }
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
