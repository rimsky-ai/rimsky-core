// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package postgres

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

type PgLargeObjectBackend struct {
	pool *pgxpool.Pool
}

var _ persistence.BlobBackend = (*PgLargeObjectBackend)(nil)
var _ persistence.TxBlobBackend = (*PgLargeObjectBackend)(nil)

func NewPgLargeObjectBackend(pool *pgxpool.Pool) *PgLargeObjectBackend {
	return &PgLargeObjectBackend{pool: pool}
}

func (b *PgLargeObjectBackend) Name() string { return "pg-largeobject" }

func (b *PgLargeObjectBackend) Write(ctx context.Context, _ persistence.BlobKey, bytes []byte) (persistence.Handle, error) {
	tx, err := b.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return "", fmt.Errorf("blob pglo: begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()
	los := tx.LargeObjects()
	oid, err := los.Create(ctx, 0)
	if err != nil {
		return "", fmt.Errorf("blob pglo: create: %w", err)
	}
	lo, err := los.Open(ctx, oid, pgx.LargeObjectModeWrite)
	if err != nil {
		return "", fmt.Errorf("blob pglo: open(write): %w", err)
	}
	if _, err := lo.Write(bytes); err != nil {
		_ = lo.Close()
		return "", fmt.Errorf("blob pglo: write: %w", err)
	}
	if err := lo.Close(); err != nil {
		return "", fmt.Errorf("blob pglo: close: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("blob pglo: commit: %w", err)
	}
	committed = true
	return persistence.Handle(fmt.Sprintf("pglo:%d", oid)), nil
}

func (b *PgLargeObjectBackend) WriteInTx(ctx context.Context, _ persistence.BlobKey, bytes []byte, tx persistence.Tx) (persistence.Handle, error) {
	pgT, err := unwrapTx(tx)
	if err != nil {
		return "", fmt.Errorf("blob pglo: WriteInTx: %w", err)
	}
	los := pgT.LargeObjects()
	oid, err := los.Create(ctx, 0)
	if err != nil {
		return "", fmt.Errorf("blob pglo: create: %w", err)
	}
	lo, err := los.Open(ctx, oid, pgx.LargeObjectModeWrite)
	if err != nil {
		return "", fmt.Errorf("blob pglo: open(write): %w", err)
	}
	if _, err := lo.Write(bytes); err != nil {
		_ = lo.Close()
		return "", fmt.Errorf("blob pglo: write: %w", err)
	}
	if err := lo.Close(); err != nil {
		return "", fmt.Errorf("blob pglo: close: %w", err)
	}
	return persistence.Handle(fmt.Sprintf("pglo:%d", oid)), nil
}

func (b *PgLargeObjectBackend) ReadInTx(ctx context.Context, handle persistence.Handle, tx persistence.Tx) ([]byte, error) {
	oid, err := parsePgloHandle(handle)
	if err != nil {
		return nil, err
	}
	pgT, err := unwrapTx(tx)
	if err != nil {
		return nil, fmt.Errorf("blob pglo: ReadInTx: %w", err)
	}
	los := pgT.LargeObjects()
	lo, err := los.Open(ctx, oid, pgx.LargeObjectModeRead)
	if err != nil {
		if isMissingLOError(err) {
			return nil, persistence.ErrBlobNotFound
		}
		return nil, fmt.Errorf("blob pglo: open(read, in-tx): %w", err)
	}
	defer func() { _ = lo.Close() }()
	out, err := io.ReadAll(lo)
	if err != nil {
		return nil, fmt.Errorf("blob pglo: read(in-tx): %w", err)
	}
	return out, nil
}

func (b *PgLargeObjectBackend) Read(ctx context.Context, handle persistence.Handle) ([]byte, error) {
	oid, err := parsePgloHandle(handle)
	if err != nil {
		return nil, err
	}
	tx, err := b.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, fmt.Errorf("blob pglo: begin(read): %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	los := tx.LargeObjects()
	lo, err := los.Open(ctx, oid, pgx.LargeObjectModeRead)
	if err != nil {
		if isMissingLOError(err) {
			return nil, persistence.ErrBlobNotFound
		}
		return nil, fmt.Errorf("blob pglo: open(read): %w", err)
	}
	defer func() { _ = lo.Close() }()
	out, err := io.ReadAll(lo)
	if err != nil {
		return nil, fmt.Errorf("blob pglo: read: %w", err)
	}
	return out, nil
}

func (b *PgLargeObjectBackend) ReadRange(ctx context.Context, handle persistence.Handle, offset, length int64) ([]byte, error) {
	if offset < 0 || length < 0 {
		return nil, fmt.Errorf("blob pglo: ReadRange: negative offset=%d length=%d", offset, length)
	}
	oid, err := parsePgloHandle(handle)
	if err != nil {
		return nil, err
	}
	tx, err := b.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, fmt.Errorf("blob pglo: begin(read-range): %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	los := tx.LargeObjects()
	lo, err := los.Open(ctx, oid, pgx.LargeObjectModeRead)
	if err != nil {
		if isMissingLOError(err) {
			return nil, persistence.ErrBlobNotFound
		}
		return nil, fmt.Errorf("blob pglo: open(read-range): %w", err)
	}
	defer func() { _ = lo.Close() }()
	if _, err := lo.Seek(offset, io.SeekStart); err != nil {
		return nil, fmt.Errorf("blob pglo: seek: %w", err)
	}
	out := make([]byte, length)
	n, err := io.ReadFull(lo, out)
	if err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, fmt.Errorf("blob pglo: read range: handle=%s offset=%d length=%d: %w", handle, offset, length, io.ErrUnexpectedEOF)
		}
		return nil, fmt.Errorf("blob pglo: read range: %w", err)
	}
	return out[:n], nil
}

func (b *PgLargeObjectBackend) Delete(ctx context.Context, handle persistence.Handle) error {
	oid, err := parsePgloHandle(handle)
	if err != nil {
		return err
	}
	tx, err := b.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("blob pglo: begin(delete): %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()
	los := tx.LargeObjects()
	if err := los.Unlink(ctx, oid); err != nil {
		if isMissingLOError(err) {
			_ = tx.Rollback(ctx)
			committed = true
			return nil
		}
		return fmt.Errorf("blob pglo: unlink: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("blob pglo: commit: %w", err)
	}
	committed = true
	return nil
}

func parsePgloHandle(h persistence.Handle) (uint32, error) {
	s := string(h)
	if !strings.HasPrefix(s, "pglo:") {
		return 0, fmt.Errorf("blob pglo: handle %q is not a pglo handle", h)
	}
	rest := strings.TrimPrefix(s, "pglo:")
	n, err := strconv.ParseUint(rest, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("blob pglo: handle %q has malformed oid: %w", h, err)
	}
	return uint32(n), nil
}

func isMissingLOError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return true
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		if pgErr.Code == "42704" {
			return true
		}
	}
	msg := err.Error()
	if strings.Contains(msg, "large object") && strings.Contains(msg, "does not exist") {
		return true
	}
	return false
}
