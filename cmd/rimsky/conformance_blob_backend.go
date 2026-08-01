// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/postgres"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/conformance/blobbackend"
)

func runConformanceBlobBackend(args []string) int {
	fs := flag.NewFlagSet("rimsky conformance blob-backend", flag.ContinueOnError)
	backend := fs.String("backend", "", "blob backend name: memory | filesystem | pg-largeobject")
	root := fs.String("root", "", "filesystem root (filesystem backend only)")
	pgDSN := fs.String("pg-conn-string", "", "Postgres DSN (pg-largeobject backend only)")
	timeout := fs.Duration("timeout", 60*time.Second, "per-check timeout")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *backend == "" {
		fmt.Fprintln(os.Stderr, "rimsky conformance blob-backend: --backend required")
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	be, cleanup, err := openBlobBackend(ctx, *backend, *root, *pgDSN)
	if err != nil {
		fmt.Fprintf(os.Stderr, "rimsky conformance blob-backend: open %s: %v\n", *backend, err)
		return 1
	}
	defer cleanup()

	results := blobbackend.Run(ctx, &blobBackendAdapter{be: be})
	return reportConformanceResults("rimsky conformance blob-backend", results,
		func(r blobbackend.CheckResult) string { return r.Name },
		func(r blobbackend.CheckResult) error { return r.Err })
}

type blobBackendAdapter struct {
	be persistence.BlobBackend
}

func (a *blobBackendAdapter) Write(ctx context.Context, hint string, bytes []byte) (blobbackend.Handle, error) {
	h, err := a.be.Write(ctx, persistence.BlobKey{Hint: hint}, bytes)
	if err != nil {
		return "", err
	}
	return blobbackend.Handle(h), nil
}

func (a *blobBackendAdapter) Read(ctx context.Context, handle blobbackend.Handle) ([]byte, error) {
	b, err := a.be.Read(ctx, persistence.Handle(handle))
	if errors.Is(err, persistence.ErrBlobNotFound) {
		return nil, blobbackend.ErrBlobNotFound
	}
	return b, err
}

func (a *blobBackendAdapter) ReadRange(ctx context.Context, handle blobbackend.Handle, offset, length int64) ([]byte, error) {
	b, err := a.be.ReadRange(ctx, persistence.Handle(handle), offset, length)
	if errors.Is(err, persistence.ErrBlobNotFound) {
		return nil, blobbackend.ErrBlobNotFound
	}
	return b, err
}

func (a *blobBackendAdapter) Delete(ctx context.Context, handle blobbackend.Handle) error {
	return a.be.Delete(ctx, persistence.Handle(handle))
}

func openBlobBackend(ctx context.Context, name, root, dsn string) (persistence.BlobBackend, func(), error) {
	switch name {
	case "memory":
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
