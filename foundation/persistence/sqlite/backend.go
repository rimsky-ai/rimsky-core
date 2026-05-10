// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/fallguy/rimsky/foundation/persistence"
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
//
// blob/blobThreshold/blobRetention carry the spill-config triple set by
// driver.SetBlobBackend at startup; same semantics as the postgres impl
// (see foundation/persistence/postgres/backend.go::storeImpl).
type storeImpl struct {
	db            *sql.DB
	blob          persistence.BlobBackend
	blobThreshold int
	blobRetention time.Duration
}

func newStore(db *sql.DB) *storeImpl { return &storeImpl{db: db} }

// SetBlobBackend installs (or clears) the spill-config triple. See
// postgres/backend.go::storeImpl.SetBlobBackend for the contract.
func (s *storeImpl) SetBlobBackend(bb persistence.BlobBackend, threshold int, retention time.Duration) {
	s.blob = bb
	s.blobThreshold = threshold
	s.blobRetention = retention
}

// BlobBackend returns the configured backend (or nil).
func (s *storeImpl) BlobBackend() persistence.BlobBackend { return s.blob }

// BlobSpillThreshold returns the threshold (0 when disabled).
func (s *storeImpl) BlobSpillThreshold() int { return s.blobThreshold }

// BlobRetention returns the orphan retention window (0 when unset).
func (s *storeImpl) BlobRetention() time.Duration { return s.blobRetention }

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

// q returns the tx-bound querier. Panics on nil tx — every Store
// method must be invoked with an explicit tx (option C / no-nil-tx
// contract; see foundation/persistence/sqlite/deadlock_guard_test.go).
// Callers that do not already hold a tx must open one with
// Store.Transaction first; the previous nil-tx auto-commit code path
// is gone.
func (s *storeImpl) q(tx persistence.Tx) querier {
	if tx == nil {
		panic("persistence: nil tx — every Store method requires an explicit tx; wrap with Store.Transaction")
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
	templatesImpl            storeImpl
	templateTagsImpl         storeImpl
	instancesImpl            storeImpl
	lifecycleIdempotencyImpl storeImpl
	nodesImpl                storeImpl
	claimHandlesImpl         storeImpl
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
	_ persistence.ClaimHandlesStore         = (*claimHandlesImpl)(nil)
	_ persistence.NodeAttributesStore       = (*nodeAttributesImpl)(nil)
	_ persistence.ClaimHoldersStore         = (*claimHoldersImpl)(nil)
	_ persistence.EventStore                = (*eventsImpl)(nil)
	_ persistence.ScheduleStore             = (*schedulesImpl)(nil)
	_ persistence.SupervisorStore           = (*supervisorsImpl)(nil)
	_ persistence.FrameStore                = (*framesImpl)(nil)
)

// Per-feature accessor methods on *storeImpl. Each downcasts to the aspect
// type to expose the per-feature method set.
func (s *storeImpl) Templates() persistence.TemplateStore        { return (*templatesImpl)(s) }
func (s *storeImpl) TemplateTags() persistence.TemplateTagsStore { return (*templateTagsImpl)(s) }
func (s *storeImpl) Instances() persistence.InstanceStore        { return (*instancesImpl)(s) }
func (s *storeImpl) LifecycleIdempotency() persistence.LifecycleIdempotencyStore {
	return (*lifecycleIdempotencyImpl)(s)
}
func (s *storeImpl) Nodes() persistence.NodeStore                    { return (*nodesImpl)(s) }
func (s *storeImpl) ClaimHandles() persistence.ClaimHandlesStore     { return (*claimHandlesImpl)(s) }
func (s *storeImpl) NodeAttributes() persistence.NodeAttributesStore { return (*nodeAttributesImpl)(s) }
func (s *storeImpl) ClaimHolders() persistence.ClaimHoldersStore     { return (*claimHoldersImpl)(s) }
func (s *storeImpl) Events() persistence.EventStore                  { return (*eventsImpl)(s) }
func (s *storeImpl) Schedules() persistence.ScheduleStore            { return (*schedulesImpl)(s) }
func (s *storeImpl) Supervisors() persistence.SupervisorStore        { return (*supervisorsImpl)(s) }
func (s *storeImpl) Frames() persistence.FrameStore                  { return (*framesImpl)(s) }

// Per-feature aspect-type query helpers: each forwards to (*storeImpl).q.
func (b *templatesImpl) q(tx persistence.Tx) querier            { return (*storeImpl)(b).q(tx) }
func (b *templateTagsImpl) q(tx persistence.Tx) querier         { return (*storeImpl)(b).q(tx) }
func (b *instancesImpl) q(tx persistence.Tx) querier            { return (*storeImpl)(b).q(tx) }
func (b *lifecycleIdempotencyImpl) q(tx persistence.Tx) querier { return (*storeImpl)(b).q(tx) }
func (b *nodesImpl) q(tx persistence.Tx) querier                { return (*storeImpl)(b).q(tx) }
func (b *claimHandlesImpl) q(tx persistence.Tx) querier         { return (*storeImpl)(b).q(tx) }
func (b *nodeAttributesImpl) q(tx persistence.Tx) querier       { return (*storeImpl)(b).q(tx) }
func (b *claimHoldersImpl) q(tx persistence.Tx) querier         { return (*storeImpl)(b).q(tx) }
func (b *eventsImpl) q(tx persistence.Tx) querier               { return (*storeImpl)(b).q(tx) }
func (b *schedulesImpl) q(tx persistence.Tx) querier            { return (*storeImpl)(b).q(tx) }
func (b *supervisorsImpl) q(tx persistence.Tx) querier          { return (*storeImpl)(b).q(tx) }
func (b *framesImpl) q(tx persistence.Tx) querier               { return (*storeImpl)(b).q(tx) }
