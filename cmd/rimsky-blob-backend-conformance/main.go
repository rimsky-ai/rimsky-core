// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// rimsky-blob-backend-conformance runs the in-process BlobBackend
// conformance suite against the named backend. Distinct from the
// claim-producer / executor conformance binaries because the backend
// surface is in-process Go (`persistence.BlobBackend`), not a wire
// protocol.
//
// The runner library lives in `pkg:protocols/conformance/blobbackend`;
// this binary is the thin CLI wrapper. It adapts each rimsky-internal
// backend (memory / filesystem / pg-largeobject) to the conformance
// library's minimal `Backend` interface and invokes the suite.
//
// Usage:
//
//	rimsky-blob-backend-conformance --backend filesystem --root /tmp/x
//	rimsky-blob-backend-conformance --backend memory
//	rimsky-blob-backend-conformance --backend pg-largeobject --pg-conn-string postgres://...
//
// Exits 0 on all checks pass; 1 on any failure.

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rimsky-ai/rimsky-core/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/foundation/persistence/postgres"
	"github.com/rimsky-ai/rimsky-core/protocols/conformance/blobbackend"
)

func main() {
	backend := flag.String("backend", "", "blob backend name: memory | filesystem | pg-largeobject")
	root := flag.String("root", "", "filesystem root (filesystem backend only)")
	pgDSN := flag.String("pg-conn-string", "", "Postgres DSN (pg-largeobject backend only)")
	timeout := flag.Duration("timeout", 60*time.Second, "per-check timeout")
	flag.Parse()
	if *backend == "" {
		fmt.Fprintln(os.Stderr, "rimsky-blob-backend-conformance: --backend required")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	be, cleanup, err := openBackend(ctx, *backend, *root, *pgDSN)
	if err != nil {
		fmt.Fprintf(os.Stderr, "rimsky-blob-backend-conformance: open %s: %v\n", *backend, err)
		os.Exit(1)
	}
	defer cleanup()

	results := blobbackend.Run(ctx, &adapter{be: be})
	failed := 0
	for _, r := range results {
		status := "PASS"
		if r.Err != nil {
			status = "FAIL"
			failed++
		}
		fmt.Printf("[%s] %s", status, r.Name)
		if r.Err != nil {
			fmt.Printf(": %v", r.Err)
		}
		fmt.Println()
	}
	if failed > 0 {
		fmt.Fprintf(os.Stderr, "rimsky-blob-backend-conformance: %d failure(s)\n", failed)
		os.Exit(1)
	}
}

// adapter bridges rimsky's persistence.BlobBackend (typed key + opaque
// Handle) to the conformance library's reduced blobbackend.Backend surface. ErrBlobNotFound
// is translated so the conformance suite's errors.Is check matches.
type adapter struct {
	be persistence.BlobBackend
}

func (a *adapter) Write(ctx context.Context, hint string, bytes []byte) (blobbackend.Handle, error) {
	h, err := a.be.Write(ctx, persistence.BlobKey{Hint: hint}, bytes)
	if err != nil {
		return "", err
	}
	return blobbackend.Handle(h), nil
}

func (a *adapter) Read(ctx context.Context, handle blobbackend.Handle) ([]byte, error) {
	b, err := a.be.Read(ctx, persistence.Handle(handle))
	if errors.Is(err, persistence.ErrBlobNotFound) {
		return nil, blobbackend.ErrBlobNotFound
	}
	return b, err
}

func (a *adapter) ReadRange(ctx context.Context, handle blobbackend.Handle, offset, length int64) ([]byte, error) {
	b, err := a.be.ReadRange(ctx, persistence.Handle(handle), offset, length)
	if errors.Is(err, persistence.ErrBlobNotFound) {
		return nil, blobbackend.ErrBlobNotFound
	}
	return b, err
}

func (a *adapter) Delete(ctx context.Context, handle blobbackend.Handle) error {
	return a.be.Delete(ctx, persistence.Handle(handle))
}

// openBackend constructs a BlobBackend by name.
func openBackend(ctx context.Context, name, root, dsn string) (persistence.BlobBackend, func(), error) {
	switch name {
	case "memory":
		// Bypass the unified-only gate; conformance is single-process.
		_ = os.Setenv(persistence.ProcessRoleEnv, "unified")
		return persistence.NewMemoryBackend(), func() {}, nil
	case "filesystem":
		if root == "" {
			return nil, nil, errors.New("--root required for filesystem backend")
		}
		be, err := persistence.NewFilesystemBackend(root)
		return be, func() {}, err
	case "pg-largeobject":
		if dsn == "" {
			return nil, nil, errors.New("--pg-conn-string required for pg-largeobject backend")
		}
		pcfg, err := pgxpool.ParseConfig(dsn)
		if err != nil {
			return nil, nil, fmt.Errorf("parse dsn: %w", err)
		}
		pool, err := pgxpool.NewWithConfig(ctx, pcfg)
		if err != nil {
			return nil, nil, fmt.Errorf("connect: %w", err)
		}
		be := postgres.NewPgLargeObjectBackend(pool)
		return be, func() { pool.Close() }, nil
	default:
		return nil, nil, fmt.Errorf("unknown backend %q (want memory | filesystem | pg-largeobject)", name)
	}
}
