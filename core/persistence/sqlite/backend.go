package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/fallguy/rimsky/core/persistence"
)

// sqliteTx is the persistence.Tx carrier for SQLite. Embeds
// persistence.TxMarker so the type satisfies the interface; the underlying
// *sql.Tx is reachable via unwrapTx.
type sqliteTx struct {
	persistence.TxMarker
	tx *sql.Tx
}

func unwrapTx(tx persistence.Tx) (*sql.Tx, error) {
	if tx == nil {
		return nil, errors.New("nil persistence.Tx")
	}
	t, ok := tx.(*sqliteTx)
	if !ok {
		return nil, fmt.Errorf("persistence.Tx is not a sqlite tx: %T", tx)
	}
	return t.tx, nil
}

// storeImpl is the per-feature umbrella the SQLite driver returns for
// Store(). Aspect types below downcast *storeImpl to expose per-feature
// method sets, mirroring the postgres impl in postgres/backend.go.
type storeImpl struct {
	db *sql.DB
}

func newStore(db *sql.DB) *storeImpl { return &storeImpl{db: db} }

// Transaction runs fn inside a sql.Tx. Rolls back on error; commits on
// success.
func (s *storeImpl) Transaction(ctx context.Context, fn func(ctx context.Context, tx persistence.Tx) error) error {
	sTx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlite.Transaction: begin: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = sTx.Rollback()
			panic(p)
		}
	}()
	if err := fn(ctx, &sqliteTx{tx: sTx}); err != nil {
		_ = sTx.Rollback()
		return err
	}
	return sTx.Commit()
}

// querier abstracts the common Exec/Query/QueryRow surface shared by
// *sql.DB and *sql.Tx. All store SQL goes through this so the same code
// path serves both auto-commit and transactional execution.
type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// q returns a querier — the *sql.DB when tx is nil, otherwise the tx.
func (s *storeImpl) q(tx persistence.Tx) querier {
	if tx == nil {
		return s.db
	}
	t, ok := tx.(*sqliteTx)
	if !ok {
		panic(fmt.Sprintf("sqlite.q: persistence.Tx is not a sqlite tx: %T", tx))
	}
	return t.tx
}

// scannable is implemented by both *sql.Row and *sql.Rows.
type scannable interface {
	Scan(dst ...any) error
}

// Per-feature aspect types — empty wrappers so each *Store has a distinct
// method set. Mirrors the postgres pattern in postgres/backend.go.
type (
	templatesImpl      storeImpl
	templateTagsImpl   storeImpl
	instancesImpl      storeImpl
	storeLifecycleImpl storeImpl
	nodesImpl          storeImpl
	lockHoldersImpl    storeImpl
	nodeAttributesImpl storeImpl
	claimHoldersImpl   storeImpl
	eventsImpl         storeImpl
	schedulesImpl      storeImpl
	supervisorsImpl    storeImpl
	framesImpl         storeImpl
)

// Compile-time assertions that each aspect type satisfies its interface.
var (
	_ persistence.Store               = (*storeImpl)(nil)
	_ persistence.TemplateStore       = (*templatesImpl)(nil)
	_ persistence.TemplateTagsStore   = (*templateTagsImpl)(nil)
	_ persistence.InstanceStore       = (*instancesImpl)(nil)
	_ persistence.StoreLifecycleStore = (*storeLifecycleImpl)(nil)
	_ persistence.NodeStore           = (*nodesImpl)(nil)
	_ persistence.LockHoldersStore    = (*lockHoldersImpl)(nil)
	_ persistence.NodeAttributesStore = (*nodeAttributesImpl)(nil)
	_ persistence.ClaimHoldersStore   = (*claimHoldersImpl)(nil)
	_ persistence.EventStore          = (*eventsImpl)(nil)
	_ persistence.ScheduleStore       = (*schedulesImpl)(nil)
	_ persistence.SupervisorStore     = (*supervisorsImpl)(nil)
	_ persistence.FrameStore          = (*framesImpl)(nil)
)

// Per-feature accessor methods on *storeImpl. Each downcasts to the aspect
// type to expose the per-feature method set.
func (s *storeImpl) Templates() persistence.TemplateStore            { return (*templatesImpl)(s) }
func (s *storeImpl) TemplateTags() persistence.TemplateTagsStore     { return (*templateTagsImpl)(s) }
func (s *storeImpl) Instances() persistence.InstanceStore            { return (*instancesImpl)(s) }
func (s *storeImpl) StoreLifecycle() persistence.StoreLifecycleStore { return (*storeLifecycleImpl)(s) }
func (s *storeImpl) Nodes() persistence.NodeStore                    { return (*nodesImpl)(s) }
func (s *storeImpl) LockHolders() persistence.LockHoldersStore       { return (*lockHoldersImpl)(s) }
func (s *storeImpl) NodeAttributes() persistence.NodeAttributesStore { return (*nodeAttributesImpl)(s) }
func (s *storeImpl) ClaimHolders() persistence.ClaimHoldersStore     { return (*claimHoldersImpl)(s) }
func (s *storeImpl) Events() persistence.EventStore                  { return (*eventsImpl)(s) }
func (s *storeImpl) Schedules() persistence.ScheduleStore            { return (*schedulesImpl)(s) }
func (s *storeImpl) Supervisors() persistence.SupervisorStore        { return (*supervisorsImpl)(s) }
func (s *storeImpl) Frames() persistence.FrameStore                  { return (*framesImpl)(s) }

// Per-feature aspect-type query helpers: each forwards to (*storeImpl).q.
func (b *templatesImpl) q(tx persistence.Tx) querier      { return (*storeImpl)(b).q(tx) }
func (b *templateTagsImpl) q(tx persistence.Tx) querier   { return (*storeImpl)(b).q(tx) }
func (b *instancesImpl) q(tx persistence.Tx) querier      { return (*storeImpl)(b).q(tx) }
func (b *storeLifecycleImpl) q(tx persistence.Tx) querier { return (*storeImpl)(b).q(tx) }
func (b *nodesImpl) q(tx persistence.Tx) querier          { return (*storeImpl)(b).q(tx) }
func (b *lockHoldersImpl) q(tx persistence.Tx) querier    { return (*storeImpl)(b).q(tx) }
func (b *nodeAttributesImpl) q(tx persistence.Tx) querier { return (*storeImpl)(b).q(tx) }
func (b *claimHoldersImpl) q(tx persistence.Tx) querier   { return (*storeImpl)(b).q(tx) }
func (b *eventsImpl) q(tx persistence.Tx) querier         { return (*storeImpl)(b).q(tx) }
func (b *schedulesImpl) q(tx persistence.Tx) querier      { return (*storeImpl)(b).q(tx) }
func (b *supervisorsImpl) q(tx persistence.Tx) querier    { return (*storeImpl)(b).q(tx) }
func (b *framesImpl) q(tx persistence.Tx) querier         { return (*storeImpl)(b).q(tx) }
