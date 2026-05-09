// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// rimsky-blob-backend-conformance runs the in-process BlobBackend
// conformance suite against the named backend. Distinct from the
// claim-producer / executor conformance binaries because the backend
// surface is in-process Go (`persistence.BlobBackend`), not a wire
// protocol.
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
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/foundation/persistence/postgres"
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

	results := runChecks(ctx, be)
	failed := 0
	for _, r := range results {
		status := "PASS"
		if r.err != nil {
			status = "FAIL"
			failed++
		}
		fmt.Printf("[%s] %s", status, r.name)
		if r.err != nil {
			fmt.Printf(": %v", r.err)
		}
		fmt.Println()
	}
	if failed > 0 {
		fmt.Fprintf(os.Stderr, "rimsky-blob-backend-conformance: %d failure(s)\n", failed)
		os.Exit(1)
	}
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

type checkResult struct {
	name string
	err  error
}

func runChecks(ctx context.Context, be persistence.BlobBackend) []checkResult {
	checks := []func(context.Context, persistence.BlobBackend) error{
		checkRoundtripSmall,
		checkRoundtripLarge,
		checkReadRange,
		checkDeleteThenRead,
		checkIdempotentDelete,
		checkConcurrentWrites,
	}
	names := []string{
		"round-trip 1KB",
		"round-trip 10MB",
		"range read",
		"delete then read returns ErrBlobNotFound",
		"idempotent delete",
		"concurrent writes",
	}
	out := make([]checkResult, 0, len(checks))
	for i, c := range checks {
		err := c(ctx, be)
		out = append(out, checkResult{name: names[i], err: err})
	}
	return out
}

func checkRoundtripSmall(ctx context.Context, be persistence.BlobBackend) error {
	payload := bytes.Repeat([]byte("x"), 1024)
	h, err := be.Write(ctx, persistence.BlobKey{Hint: "rt-small"}, payload)
	if err != nil {
		return fmt.Errorf("write: %w", err)
	}
	got, err := be.Read(ctx, h)
	if err != nil {
		return fmt.Errorf("read: %w", err)
	}
	if !bytes.Equal(got, payload) {
		return errors.New("byte mismatch")
	}
	return be.Delete(ctx, h)
}

func checkRoundtripLarge(ctx context.Context, be persistence.BlobBackend) error {
	payload := bytes.Repeat([]byte("0123456789"), 1024*1024) // 10 MiB
	h, err := be.Write(ctx, persistence.BlobKey{Hint: "rt-large"}, payload)
	if err != nil {
		return fmt.Errorf("write: %w", err)
	}
	got, err := be.Read(ctx, h)
	if err != nil {
		return fmt.Errorf("read: %w", err)
	}
	if !bytes.Equal(got, payload) {
		return errors.New("byte mismatch")
	}
	return be.Delete(ctx, h)
}

func checkReadRange(ctx context.Context, be persistence.BlobBackend) error {
	payload := []byte("0123456789abcdef")
	h, err := be.Write(ctx, persistence.BlobKey{Hint: "range"}, payload)
	if err != nil {
		return err
	}
	got, err := be.ReadRange(ctx, h, 5, 5)
	if err != nil {
		return err
	}
	if string(got) != "56789" {
		return fmt.Errorf("range mismatch: got %q", got)
	}
	return be.Delete(ctx, h)
}

func checkDeleteThenRead(ctx context.Context, be persistence.BlobBackend) error {
	h, err := be.Write(ctx, persistence.BlobKey{Hint: "del-read"}, []byte("x"))
	if err != nil {
		return err
	}
	if err := be.Delete(ctx, h); err != nil {
		return fmt.Errorf("delete: %w", err)
	}
	if _, err := be.Read(ctx, h); !errors.Is(err, persistence.ErrBlobNotFound) {
		return fmt.Errorf("post-delete Read: want ErrBlobNotFound, got %v", err)
	}
	return nil
}

func checkIdempotentDelete(ctx context.Context, be persistence.BlobBackend) error {
	h, err := be.Write(ctx, persistence.BlobKey{Hint: "idem"}, []byte("x"))
	if err != nil {
		return err
	}
	if err := be.Delete(ctx, h); err != nil {
		return err
	}
	if err := be.Delete(ctx, h); err != nil {
		return fmt.Errorf("second delete: %w", err)
	}
	return nil
}

func checkConcurrentWrites(ctx context.Context, be persistence.BlobBackend) error {
	const N = 16
	var wg sync.WaitGroup
	errs := make([]error, N)
	for i := 0; i < N; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			h, err := be.Write(ctx, persistence.BlobKey{Hint: fmt.Sprintf("c-%d", i)}, []byte(fmt.Sprintf("payload-%d", i)))
			if err != nil {
				errs[i] = err
				return
			}
			defer func() { _ = be.Delete(ctx, h) }()
			got, err := be.Read(ctx, h)
			if err != nil {
				errs[i] = err
				return
			}
			if !bytes.Equal(got, []byte(fmt.Sprintf("payload-%d", i))) {
				errs[i] = fmt.Errorf("c-%d byte mismatch", i)
			}
		}()
	}
	wg.Wait()
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}

// silence unused-import warning when io is only conditionally used.
var _ = io.EOF
