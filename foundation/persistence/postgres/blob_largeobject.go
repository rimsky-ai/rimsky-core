// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// blob_largeobject.go is the persistence.BlobBackend implementation backed
// by Postgres LARGE OBJECTs (the libpq "lo_*" family / pgx LargeObjects
// API). Suitable for multi-process deployments because the bytes live in
// the same Postgres instance every rimsky process already talks to.
//
// Handles are formatted as "pglo:<oid>" so they are self-describing
// across mixed-backend deployments. Each LO operation runs inside a
// pgx transaction (Postgres LO API is tx-bound).
//
// @blessed-invariant 21: Blob content is inert in Rimsky. The bytes are
// returned to callers verbatim; this file does not log them, format them
// with %v, transform them, or attach them to traces or errors.
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

	"github.com/rimsky-ai/rimsky-core/foundation/persistence"
)

// PgLargeObjectBackend stores blobs in the Postgres pg_largeobject
// catalog table via the LO API. The backend uses the same connection
// pool the persistence Driver uses for everything else.
type PgLargeObjectBackend struct {
	pool *pgxpool.Pool
}

// Compile-time interface check.
var _ persistence.BlobBackend = (*PgLargeObjectBackend)(nil)

// NewPgLargeObjectBackend constructs a backend bound to pool. The pool
// must point at the same database that holds the rimsky tables (LO oids
// are database-scoped).
func NewPgLargeObjectBackend(pool *pgxpool.Pool) *PgLargeObjectBackend {
	return &PgLargeObjectBackend{pool: pool}
}

// Name returns "pg-largeobject".
func (b *PgLargeObjectBackend) Name() string { return "pg-largeobject" }

// Write creates a new LO, writes bytes, and returns "pglo:<oid>".
// The LO is committed before Write returns; callers do not need to
// participate in any larger tx.
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

// Read fetches the LO by oid and returns the full byte stream.
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

// ReadRange fetches a byte range from the LO using LO seek + read.
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
			return nil, io.ErrUnexpectedEOF
		}
		return nil, fmt.Errorf("blob pglo: read range: %w", err)
	}
	return out[:n], nil
}

// Delete unlinks the LO. Idempotent: missing oid returns nil.
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
			// Idempotent: missing -> success.
			_ = tx.Rollback(ctx)
			committed = true // suppress double-rollback
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

// parsePgloHandle parses "pglo:<oid>" → oid, rejecting any other shape.
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

// isMissingLOError detects "large object NNNN does not exist" errors.
// pgx returns these as *pgconn.PgError with code 42704 (undefined_object)
// or via the explicit pgx ErrNoRows path on some operations.
func isMissingLOError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return true
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		// 42704 = undefined_object (LO doesn't exist).
		// 22023 = invalid_parameter_value (older pg returns this for
		// missing oid on lo_open). Either is "not found" semantically.
		if pgErr.Code == "42704" || pgErr.Code == "22023" {
			return true
		}
	}
	// Some pgx versions wrap the missing-LO message in a plain error.
	msg := err.Error()
	if strings.Contains(msg, "large object") && strings.Contains(msg, "does not exist") {
		return true
	}
	return false
}
