// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package postgres_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/internal/pgtest"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	pgpersist "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/postgres"
)

func TestPgLargeObjectBackend(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	pool, _ := pgtest.StartPostgres(ctx, t)
	be := pgpersist.NewPgLargeObjectBackend(pool)
	if be.Name() != "pg-largeobject" {
		t.Fatalf("Name: got %q, want pg-largeobject", be.Name())
	}

	payload := bytes.Repeat([]byte("0123456789abcdef"), 65536)
	h, err := be.Write(ctx, persistence.BlobKey{NodeID: "n1", AttributeName: "value"}, payload)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if !strings.HasPrefix(string(h), "pglo:") {
		t.Fatalf("handle prefix: got %q", h)
	}

	got, err := be.Read(ctx, h)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("Read: bytes mismatch (got %d, want %d)", len(got), len(payload))
	}

	gotRange, err := be.ReadRange(ctx, h, 16, 16)
	if err != nil {
		t.Fatalf("ReadRange: %v", err)
	}
	if !bytes.Equal(gotRange, []byte("0123456789abcdef")) {
		t.Fatalf("ReadRange: got %q, want %q", gotRange, "0123456789abcdef")
	}

	if err := be.Delete(ctx, h); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := be.Delete(ctx, h); err != nil {
		t.Fatalf("Delete (idempotent): %v", err)
	}
	if _, err := be.Read(ctx, h); !errors.Is(err, persistence.ErrBlobNotFound) {
		t.Fatalf("post-delete Read: want ErrBlobNotFound, got %v", err)
	}
}

func TestPgLargeObjectBackendReadRangeOutOfBounds(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	pool, _ := pgtest.StartPostgres(ctx, t)
	be := pgpersist.NewPgLargeObjectBackend(pool)
	h, err := be.Write(ctx, persistence.BlobKey{}, []byte("short"))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := be.ReadRange(ctx, h, 0, 1024); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("ReadRange out-of-bounds: want io.ErrUnexpectedEOF, got %v", err)
	}
}

func TestPgLargeObjectBackendReadRangeNotFound(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	pool, _ := pgtest.StartPostgres(ctx, t)
	be := pgpersist.NewPgLargeObjectBackend(pool)
	if _, err := be.ReadRange(ctx, persistence.Handle("pglo:999999999"), 0, 1); !errors.Is(err, persistence.ErrBlobNotFound) {
		t.Fatalf("ReadRange on missing handle: want ErrBlobNotFound, got %v", err)
	}
}

func TestPgLargeObjectBackendWriteInTxRollsBackWithCallerTx(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	pool, _ := pgtest.StartPostgres(ctx, t)
	be := pgpersist.NewPgLargeObjectBackend(pool)

	callerTx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatalf("begin caller tx: %v", err)
	}
	h, err := be.WriteInTx(ctx, pgpersist.WrapPgxTxForTest(callerTx), persistence.BlobKey{}, []byte("leak-me-not"))
	if err != nil {
		t.Fatalf("WriteInTx: %v", err)
	}
	if err := callerTx.Rollback(ctx); err != nil {
		t.Fatalf("rollback caller tx: %v", err)
	}

	if _, err := be.Read(ctx, h); !errors.Is(err, persistence.ErrBlobNotFound) {
		t.Fatalf("WriteInTx large object survived caller tx rollback: Read = %v, want ErrBlobNotFound", err)
	}
}

func TestPgLargeObjectBackendWriteInTxCommitsWithCallerTx(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	pool, _ := pgtest.StartPostgres(ctx, t)
	be := pgpersist.NewPgLargeObjectBackend(pool)

	callerTx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatalf("begin caller tx: %v", err)
	}
	wrapped := pgpersist.WrapPgxTxForTest(callerTx)
	h, err := be.WriteInTx(ctx, wrapped, persistence.BlobKey{}, []byte("payload"))
	if err != nil {
		t.Fatalf("WriteInTx: %v", err)
	}
	gotInTx, err := be.ReadInTx(ctx, wrapped, h)
	if err != nil {
		t.Fatalf("ReadInTx: %v", err)
	}
	if string(gotInTx) != "payload" {
		t.Fatalf("ReadInTx: got %q, want %q", gotInTx, "payload")
	}
	if err := callerTx.Commit(ctx); err != nil {
		t.Fatalf("commit caller tx: %v", err)
	}

	got, err := be.Read(ctx, h)
	if err != nil {
		t.Fatalf("Read after commit: %v", err)
	}
	if string(got) != "payload" {
		t.Fatalf("Read after commit: got %q, want %q", got, "payload")
	}
}

func TestPgLargeObjectBackendWriteInTxDoesNotAcquireSecondConnection(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	dsn, _ := pgtest.StartFreshPostgresDSN(ctx, t)
	pcfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	pcfg.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(ctx, pcfg)
	if err != nil {
		t.Fatalf("new pool: %v", err)
	}
	t.Cleanup(pool.Close)
	be := pgpersist.NewPgLargeObjectBackend(pool)

	callerTx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatalf("begin caller tx (pins the pool's only connection): %v", err)
	}
	defer func() { _ = callerTx.Rollback(ctx) }()
	wrapped := pgpersist.WrapPgxTxForTest(callerTx)

	h, err := be.WriteInTx(ctx, wrapped, persistence.BlobKey{}, []byte("no-deadlock"))
	if err != nil {
		t.Fatalf("WriteInTx under a single-connection pool with the connection already pinned: %v", err)
	}
	got, err := be.ReadInTx(ctx, wrapped, h)
	if err != nil {
		t.Fatalf("ReadInTx under a single-connection pool with the connection already pinned: %v", err)
	}
	if string(got) != "no-deadlock" {
		t.Fatalf("ReadInTx: got %q, want %q", got, "no-deadlock")
	}
}

func TestPgLargeObjectBackendRejectsBadHandle(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	pool, _ := pgtest.StartPostgres(ctx, t)
	be := pgpersist.NewPgLargeObjectBackend(pool)
	if _, err := be.Read(ctx, persistence.Handle("fs:/etc/passwd")); err == nil {
		t.Fatalf("expected error on non-pglo handle, got nil")
	}
	if _, err := be.Read(ctx, persistence.Handle("pglo:not-a-number")); err == nil {
		t.Fatalf("expected error on malformed oid, got nil")
	}
}
