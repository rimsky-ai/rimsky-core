package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/fallguy/rimsky/foundation/persistence"
)

// pgTx is the persistence.Tx carrier for this driver. Embeds
// persistence.TxMarker so it satisfies the interface; the persistence
// package's Tx is the only Tx callers see.
type pgTx struct {
	persistence.TxMarker
	tx pgx.Tx
}

// init wires unwrapTx (declared in coordinator.go) so coordinator and
// every per-feature impl can extract the underlying pgx.Tx from a
// persistence.Tx without making the symbol public.
func init() {
	unwrapTx = func(tx persistence.Tx) (pgx.Tx, error) {
		if tx == nil {
			return nil, errors.New("nil persistence.Tx")
		}
		t, ok := tx.(*pgTx)
		if !ok {
			return nil, fmt.Errorf("persistence.Tx is not a postgres tx: %T", tx)
		}
		return t.tx, nil
	}
}

// storeImpl is the persistence.Store impl. The per-feature *Store methods
// return the same impl pointer downcast to its narrow aspect type — each
// per-feature file (nodes.go, instances.go, ...) defines methods on the
// aspect type. The aspect-type pattern (`type templatesImpl storeImpl`)
// shares storeImpl's layout so the helper q() works through the cast.
type storeImpl struct {
	pool *pgxpool.Pool
}

func newStore(pool *pgxpool.Pool) *storeImpl { return &storeImpl{pool: pool} }

// Transaction begins a tx, runs fn, and commits/rolls back. If fn returns
// an error or panics, the tx rolls back. The tx passed to fn is a *pgTx
// wrapped in persistence.Tx; unwrap via the package-private unwrapTx.
func (s *storeImpl) Transaction(ctx context.Context, fn func(ctx context.Context, tx persistence.Tx) error) error {
	pgT, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("postgres.Transaction: begin: %w", err)
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

// querier is the common surface shared by *pgxpool.Pool and pgx.Tx.
type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// q returns a querier — pool when tx is nil, otherwise the tx carrier.
// Per-feature impls call this at the top of every method.
func (s *storeImpl) q(tx persistence.Tx) querier {
	if tx == nil {
		return s.pool
	}
	t, ok := tx.(*pgTx)
	if !ok {
		// Programmer error; a Tx that isn't a *pgTx came from another
		// driver. Panic so the misuse surfaces immediately.
		panic(fmt.Sprintf("postgres.q: persistence.Tx is not a postgres tx: %T", tx))
	}
	return t.tx
}

// Per-feature aspect types — empty wrappers so each *Store has a distinct
// method set. Defined here so other files can attach methods.
type (
	templatesImpl            storeImpl
	templateTagsImpl         storeImpl
	instancesImpl            storeImpl
	lifecycleIdempotencyImpl storeImpl
	nodesImpl                storeImpl
	lockHoldersImpl          storeImpl
	nodeAttributesImpl       storeImpl
	claimHoldersImpl         storeImpl
	eventsImpl               storeImpl
	schedulesImpl            storeImpl
	supervisorsImpl          storeImpl
	framesImpl               storeImpl
)

// Compile-time assertions that each aspect type satisfies its interface.
var (
	_ persistence.Store                     = (*storeImpl)(nil)
	_ persistence.TemplateStore             = (*templatesImpl)(nil)
	_ persistence.TemplateTagsStore         = (*templateTagsImpl)(nil)
	_ persistence.InstanceStore             = (*instancesImpl)(nil)
	_ persistence.LifecycleIdempotencyStore = (*lifecycleIdempotencyImpl)(nil)
	_ persistence.NodeStore                 = (*nodesImpl)(nil)
	_ persistence.LockHoldersStore          = (*lockHoldersImpl)(nil)
	_ persistence.NodeAttributesStore       = (*nodeAttributesImpl)(nil)
	_ persistence.ClaimHoldersStore         = (*claimHoldersImpl)(nil)
	_ persistence.EventStore                = (*eventsImpl)(nil)
	_ persistence.ScheduleStore             = (*schedulesImpl)(nil)
	_ persistence.SupervisorStore           = (*supervisorsImpl)(nil)
	_ persistence.FrameStore                = (*framesImpl)(nil)
)

// Per-feature accessor methods on *storeImpl. Each downcasts to the
// aspect type to expose the per-feature method set.
func (s *storeImpl) Templates() persistence.TemplateStore        { return (*templatesImpl)(s) }
func (s *storeImpl) TemplateTags() persistence.TemplateTagsStore { return (*templateTagsImpl)(s) }
func (s *storeImpl) Instances() persistence.InstanceStore        { return (*instancesImpl)(s) }
func (s *storeImpl) LifecycleIdempotency() persistence.LifecycleIdempotencyStore {
	return (*lifecycleIdempotencyImpl)(s)
}
func (s *storeImpl) Nodes() persistence.NodeStore                    { return (*nodesImpl)(s) }
func (s *storeImpl) LockHolders() persistence.LockHoldersStore       { return (*lockHoldersImpl)(s) }
func (s *storeImpl) NodeAttributes() persistence.NodeAttributesStore { return (*nodeAttributesImpl)(s) }
func (s *storeImpl) ClaimHolders() persistence.ClaimHoldersStore     { return (*claimHoldersImpl)(s) }
func (s *storeImpl) Events() persistence.EventStore                  { return (*eventsImpl)(s) }
func (s *storeImpl) Schedules() persistence.ScheduleStore            { return (*schedulesImpl)(s) }
func (s *storeImpl) Supervisors() persistence.SupervisorStore        { return (*supervisorsImpl)(s) }

func (s *storeImpl) Frames() persistence.FrameStore { return (*framesImpl)(s) }

// Per-feature aspect-type query helpers: each forwards to (*storeImpl).q.
func (b *templatesImpl) q(tx persistence.Tx) querier            { return (*storeImpl)(b).q(tx) }
func (b *templateTagsImpl) q(tx persistence.Tx) querier         { return (*storeImpl)(b).q(tx) }
func (b *instancesImpl) q(tx persistence.Tx) querier            { return (*storeImpl)(b).q(tx) }
func (b *lifecycleIdempotencyImpl) q(tx persistence.Tx) querier { return (*storeImpl)(b).q(tx) }
func (b *nodesImpl) q(tx persistence.Tx) querier                { return (*storeImpl)(b).q(tx) }
func (b *lockHoldersImpl) q(tx persistence.Tx) querier          { return (*storeImpl)(b).q(tx) }
func (b *nodeAttributesImpl) q(tx persistence.Tx) querier       { return (*storeImpl)(b).q(tx) }
func (b *claimHoldersImpl) q(tx persistence.Tx) querier         { return (*storeImpl)(b).q(tx) }
func (b *eventsImpl) q(tx persistence.Tx) querier               { return (*storeImpl)(b).q(tx) }
func (b *schedulesImpl) q(tx persistence.Tx) querier            { return (*storeImpl)(b).q(tx) }
func (b *supervisorsImpl) q(tx persistence.Tx) querier          { return (*storeImpl)(b).q(tx) }
func (b *framesImpl) q(tx persistence.Tx) querier               { return (*storeImpl)(b).q(tx) }
