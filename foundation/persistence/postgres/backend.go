// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

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

// tablesImpl is the persistence.Tables impl. The per-feature *Table methods
// return the same impl pointer downcast to its narrow aspect type — each
// per-feature file (nodes.go, instances.go, ...) defines methods on the
// aspect type. The aspect-type pattern (`type templatesImpl tablesImpl`)
// shares tablesImpl's layout so the helper q() works through the cast.
//
// blob/blobThreshold/blobRetention are the spill-config triple set by
// database.SetBlobBackend at startup. When blob is non-nil and the
// marshalled attribute bytes exceed blobThreshold, NodeAttributes.Upsert
// spills to the configured backend instead of writing inline. Reads
// transparently dereference the handle. See plan §D6/D7.
type tablesImpl struct {
	pool          *pgxpool.Pool
	blob          persistence.BlobBackend
	blobThreshold int
	blobRetention time.Duration
}

func newTables(pool *pgxpool.Pool) *tablesImpl { return &tablesImpl{pool: pool} }

// SetBlobBackend installs (or clears) the spill-config triple on the
// tablesImpl. Called by database.SetBlobBackend at startup; safe to call
// multiple times during construction. Threshold ≤ 0 disables spill;
// retention ≤ 0 falls back to 24h at orphan-insert time.
func (s *tablesImpl) SetBlobBackend(bb persistence.BlobBackend, threshold int, retention time.Duration) {
	s.blob = bb
	s.blobThreshold = threshold
	s.blobRetention = retention
}

// BlobBackend returns the configured backend (or nil when spill is
// disabled). Used by integration code that already had args.Blob; this
// accessor lets callers that only carry a *Tables also read the backend
// without an extra argument.
func (s *tablesImpl) BlobBackend() persistence.BlobBackend { return s.blob }

// BlobSpillThreshold returns the spill threshold in bytes (0 when
// disabled).
func (s *tablesImpl) BlobSpillThreshold() int { return s.blobThreshold }

// BlobRetention returns the orphan-retention window (0 when unset; the
// orphan-insert site falls back to 24h).
func (s *tablesImpl) BlobRetention() time.Duration { return s.blobRetention }

// Transaction begins a tx, runs fn, and commits/rolls back. If fn returns
// an error or panics, the tx rolls back. The tx passed to fn is a *pgTx
// wrapped in persistence.Tx; unwrap via the package-private unwrapTx.
func (s *tablesImpl) Transaction(ctx context.Context, fn func(ctx context.Context, tx persistence.Tx) error) error {
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

// q returns the tx-bound querier. Panics on nil tx — every Table
// method must be invoked with an explicit tx (option C / no-nil-tx
// contract; see foundation/persistence/sqlite/deadlock_guard_test.go).
// Callers that do not already hold a tx must open one with
// Tables.Transaction first; the previous nil-tx pool-driven
// auto-commit code path is gone. The deadlock that motivated the
// rule is SQLite-specific (MaxOpenConns=1), but the contract is
// uniform across drivers so a successful postgres run can't mask a
// SQLite-only regression.
func (s *tablesImpl) q(tx persistence.Tx) querier {
	if tx == nil {
		panic("persistence: nil tx — every Table method requires an explicit tx; wrap with Tables.Transaction")
	}
	t, ok := tx.(*pgTx)
	if !ok {
		// Programmer error; a Tx that isn't a *pgTx came from another
		// driver. Panic so the misuse surfaces immediately.
		panic(fmt.Sprintf("postgres.q: persistence.Tx is not a postgres tx: %T", tx))
	}
	return t.tx
}

// Per-feature aspect types — empty wrappers so each *Table has a distinct
// method set. Defined here so other files can attach methods.
type (
	templatesImpl            tablesImpl
	templateTagsImpl         tablesImpl
	instancesImpl            tablesImpl
	lifecycleIdempotencyImpl tablesImpl
	nodesImpl                tablesImpl
	claimHandlesImpl         tablesImpl
	nodeAttributesImpl       tablesImpl
	claimHoldersImpl         tablesImpl
	eventsImpl               tablesImpl
	supervisorsImpl          tablesImpl
	framesImpl               tablesImpl
)

// Compile-time assertions that each aspect type satisfies its interface.
var (
	_ persistence.Tables                    = (*tablesImpl)(nil)
	_ persistence.TemplateTable             = (*templatesImpl)(nil)
	_ persistence.TemplateTagTable          = (*templateTagsImpl)(nil)
	_ persistence.InstanceTable             = (*instancesImpl)(nil)
	_ persistence.LifecycleIdempotencyTable = (*lifecycleIdempotencyImpl)(nil)
	_ persistence.NodeTable                 = (*nodesImpl)(nil)
	_ persistence.ClaimHandleTable          = (*claimHandlesImpl)(nil)
	_ persistence.NodeAttributeTable        = (*nodeAttributesImpl)(nil)
	_ persistence.ClaimHolderTable          = (*claimHoldersImpl)(nil)
	_ persistence.EventTable                = (*eventsImpl)(nil)
	_ persistence.SupervisorTable           = (*supervisorsImpl)(nil)
	_ persistence.FrameTable                = (*framesImpl)(nil)
)

// Per-feature accessor methods on *tablesImpl. Each downcasts to the
// aspect type to expose the per-feature method set.
func (s *tablesImpl) Templates() persistence.TemplateTable       { return (*templatesImpl)(s) }
func (s *tablesImpl) TemplateTags() persistence.TemplateTagTable { return (*templateTagsImpl)(s) }
func (s *tablesImpl) Instances() persistence.InstanceTable       { return (*instancesImpl)(s) }
func (s *tablesImpl) LifecycleIdempotency() persistence.LifecycleIdempotencyTable {
	return (*lifecycleIdempotencyImpl)(s)
}
func (s *tablesImpl) Nodes() persistence.NodeTable                   { return (*nodesImpl)(s) }
func (s *tablesImpl) ClaimHandles() persistence.ClaimHandleTable     { return (*claimHandlesImpl)(s) }
func (s *tablesImpl) NodeAttributes() persistence.NodeAttributeTable { return (*nodeAttributesImpl)(s) }
func (s *tablesImpl) ClaimHolders() persistence.ClaimHolderTable     { return (*claimHoldersImpl)(s) }
func (s *tablesImpl) Events() persistence.EventTable                 { return (*eventsImpl)(s) }
func (s *tablesImpl) Supervisors() persistence.SupervisorTable       { return (*supervisorsImpl)(s) }

func (s *tablesImpl) Frames() persistence.FrameTable { return (*framesImpl)(s) }

// Per-feature aspect-type query helpers: each forwards to (*tablesImpl).q.
func (b *templatesImpl) q(tx persistence.Tx) querier            { return (*tablesImpl)(b).q(tx) }
func (b *templateTagsImpl) q(tx persistence.Tx) querier         { return (*tablesImpl)(b).q(tx) }
func (b *instancesImpl) q(tx persistence.Tx) querier            { return (*tablesImpl)(b).q(tx) }
func (b *lifecycleIdempotencyImpl) q(tx persistence.Tx) querier { return (*tablesImpl)(b).q(tx) }
func (b *nodesImpl) q(tx persistence.Tx) querier                { return (*tablesImpl)(b).q(tx) }
func (b *claimHandlesImpl) q(tx persistence.Tx) querier         { return (*tablesImpl)(b).q(tx) }
func (b *nodeAttributesImpl) q(tx persistence.Tx) querier       { return (*tablesImpl)(b).q(tx) }
func (b *claimHoldersImpl) q(tx persistence.Tx) querier         { return (*tablesImpl)(b).q(tx) }
func (b *eventsImpl) q(tx persistence.Tx) querier               { return (*tablesImpl)(b).q(tx) }
func (b *supervisorsImpl) q(tx persistence.Tx) querier          { return (*tablesImpl)(b).q(tx) }
func (b *framesImpl) q(tx persistence.Tx) querier               { return (*tablesImpl)(b).q(tx) }
