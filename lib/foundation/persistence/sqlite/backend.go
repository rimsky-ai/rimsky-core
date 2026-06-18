// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @source: lib/foundation/persistence/postgres/backend.go
// @diverged: true
// @reason: parallel driver — SQLite dialect (positional ? params, database/sql) vs Postgres (pgx, $-params)

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

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

type tablesImpl struct {
	db            *sql.DB
	blob          persistence.BlobBackend
	blobThreshold int
	blobRetention time.Duration
}

func newTables(db *sql.DB) *tablesImpl { return &tablesImpl{db: db} }

func (s *tablesImpl) SetBlobBackend(bb persistence.BlobBackend, threshold int, retention time.Duration) {
	s.blob = bb
	s.blobThreshold = threshold
	s.blobRetention = retention
}

func (s *tablesImpl) BlobBackend() persistence.BlobBackend { return s.blob }

func (s *tablesImpl) BlobSpillThreshold() int { return s.blobThreshold }

func (s *tablesImpl) BlobRetention() time.Duration { return s.blobRetention }

func (s *tablesImpl) Transaction(ctx context.Context, fn func(ctx context.Context, tx persistence.Tx) error) error {
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

type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func (s *tablesImpl) q(tx persistence.Tx) querier {
	if tx == nil {
		panic("persistence: nil tx — every Table method requires an explicit tx; wrap with Tables.Transaction")
	}
	t, ok := tx.(*sqliteTx)
	if !ok {
		panic(fmt.Sprintf("sqlite.q: persistence.Tx is not a sqlite tx: %T", tx))
	}
	return t.tx
}

type scannable interface {
	Scan(dst ...any) error
}

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
func (s *tablesImpl) Frames() persistence.FrameTable                 { return (*framesImpl)(s) }

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
