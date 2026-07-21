// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
)

type fakeBlobOrphanTable struct {
	mu   sync.Mutex
	rows []persistence.BlobOrphanRow
}

func (f *fakeBlobOrphanTable) Insert(_ context.Context, row persistence.BlobOrphanRow, _ persistence.Tx) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows = append(f.rows, row)
	return nil
}

func (f *fakeBlobOrphanTable) DueBefore(_ context.Context, cutoff time.Time, backend string, limit int) ([]persistence.BlobOrphanRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []persistence.BlobOrphanRow
	for _, r := range f.rows {
		if r.Backend != backend {
			continue
		}
		if !r.ReapAfter.After(cutoff) {
			out = append(out, r)
			if limit > 0 && len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

func (f *fakeBlobOrphanTable) Delete(_ context.Context, handle string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.rows[:0]
	for _, r := range f.rows {
		if r.Handle != handle {
			out = append(out, r)
		}
	}
	f.rows = out
	return nil
}

func TestSweepOrphanedBlobs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	be := persistence.NewMemoryBackend()
	h1, _ := be.Write(ctx, persistence.BlobKey{}, []byte("one"))
	h2, _ := be.Write(ctx, persistence.BlobKey{}, []byte("two"))
	h3, _ := be.Write(ctx, persistence.BlobKey{}, []byte("three"))

	store := &fakeBlobOrphanTable{}
	now := time.Now()
	if err := store.Insert(ctx, persistence.BlobOrphanRow{
		Handle: string(h1), Backend: "memory",
		OrphanedAt: now.Add(-time.Hour), ReapAfter: now.Add(-time.Minute),
	}, nil); err != nil {
		t.Fatalf("Insert h1: %v", err)
	}
	if err := store.Insert(ctx, persistence.BlobOrphanRow{
		Handle: string(h2), Backend: "memory",
		OrphanedAt: now.Add(-time.Hour), ReapAfter: now.Add(-time.Minute),
	}, nil); err != nil {
		t.Fatalf("Insert h2: %v", err)
	}
	if err := store.Insert(ctx, persistence.BlobOrphanRow{
		Handle: string(h3), Backend: "memory",
		OrphanedAt: now, ReapAfter: now.Add(time.Hour),
	}, nil); err != nil {
		t.Fatalf("Insert h3: %v", err)
	}

	if err := runtime.SweepOrphanedBlobs(ctx, runtime.OrphanBlobsArgs{
		BlobOrphans: store,
		Backend:     be,
	}); err != nil {
		t.Fatalf("SweepOrphanedBlobs: %v", err)
	}

	if _, err := be.Read(ctx, h1); !errors.Is(err, persistence.ErrBlobNotFound) {
		t.Fatalf("h1 should be deleted; got %v", err)
	}
	if _, err := be.Read(ctx, h2); !errors.Is(err, persistence.ErrBlobNotFound) {
		t.Fatalf("h2 should be deleted; got %v", err)
	}
	if _, err := be.Read(ctx, h3); err != nil {
		t.Fatalf("h3 should still exist; got %v", err)
	}
	if len(store.rows) != 1 || store.rows[0].Handle != string(h3) {
		t.Fatalf("store should retain only h3; got %+v", store.rows)
	}
}

func TestSweepOrphanedBlobsCrossBackendIgnored(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	be := persistence.NewMemoryBackend()
	store := &fakeBlobOrphanTable{}
	if err := store.Insert(ctx, persistence.BlobOrphanRow{
		Handle:     "fs:other-backend",
		Backend:    "filesystem",
		OrphanedAt: time.Now().Add(-time.Hour),
		ReapAfter:  time.Now().Add(-time.Minute),
	}, nil); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if err := runtime.SweepOrphanedBlobs(ctx, runtime.OrphanBlobsArgs{
		BlobOrphans: store,
		Backend:     be,
	}); err != nil {
		t.Fatalf("SweepOrphanedBlobs: %v", err)
	}
	if len(store.rows) != 1 {
		t.Fatalf("cross-backend orphan should not be reaped; got %d rows", len(store.rows))
	}
}

func TestSweepOrphanedBlobsCrossBackendRowsDoNotStarveSameBackendPage(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	be := persistence.NewMemoryBackend()
	h, err := be.Write(ctx, persistence.BlobKey{}, []byte("same-backend"))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	store := &fakeBlobOrphanTable{}
	now := time.Now()
	for i := 0; i < 5; i++ {
		if err := store.Insert(ctx, persistence.BlobOrphanRow{
			Handle:     fmt.Sprintf("fs:foreign-%d", i),
			Backend:    "filesystem",
			OrphanedAt: now.Add(-time.Hour),
			ReapAfter:  now.Add(-time.Minute).Add(-time.Duration(i) * time.Second),
		}, nil); err != nil {
			t.Fatalf("Insert foreign %d: %v", i, err)
		}
	}
	if err := store.Insert(ctx, persistence.BlobOrphanRow{
		Handle: string(h), Backend: "memory",
		OrphanedAt: now.Add(-time.Hour), ReapAfter: now.Add(-time.Minute),
	}, nil); err != nil {
		t.Fatalf("Insert same-backend: %v", err)
	}

	if err := runtime.SweepOrphanedBlobs(ctx, runtime.OrphanBlobsArgs{
		BlobOrphans: store,
		Backend:     be,
		Limit:       2,
	}); err != nil {
		t.Fatalf("SweepOrphanedBlobs: %v", err)
	}

	if _, err := be.Read(ctx, h); !errors.Is(err, persistence.ErrBlobNotFound) {
		t.Fatalf("same-backend orphan must be reaped even though 5 foreign-backend rows sort earlier by reap_after; got %v", err)
	}
	if len(store.rows) != 5 {
		t.Fatalf("only the same-backend row should have been reaped from the store; got %d rows remaining", len(store.rows))
	}
}

func TestSweepOrphanedBlobsHandlesNotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	be := persistence.NewMemoryBackend()
	store := &fakeBlobOrphanTable{}
	if err := store.Insert(ctx, persistence.BlobOrphanRow{
		Handle:     "mem:9999",
		Backend:    "memory",
		OrphanedAt: time.Now().Add(-time.Hour),
		ReapAfter:  time.Now().Add(-time.Minute),
	}, nil); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if err := runtime.SweepOrphanedBlobs(ctx, runtime.OrphanBlobsArgs{
		BlobOrphans: store,
		Backend:     be,
	}); err != nil {
		t.Fatalf("SweepOrphanedBlobs: %v", err)
	}
	if len(store.rows) != 0 {
		t.Fatalf("ghost-handle row should be cleared; got %d rows", len(store.rows))
	}
}
