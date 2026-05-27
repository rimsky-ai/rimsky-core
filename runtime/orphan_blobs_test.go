// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/runtime"
)

// fakeBlobOrphanTable is a tiny in-memory BlobOrphanTable used by the
// sweep test. It tracks rows and deletes; backend interaction goes
// through fakeBlobBackend.
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

func (f *fakeBlobOrphanTable) DueBefore(_ context.Context, cutoff time.Time, limit int) ([]persistence.BlobOrphanRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []persistence.BlobOrphanRow
	for _, r := range f.rows {
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

// TestSweepOrphanedBlobs covers the happy path: rows with reap_after
// in the past are deleted from the backend and the tracker.
func TestSweepOrphanedBlobs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	be := persistence.NewMemoryBackend()
	// Pre-write three blobs.
	h1, _ := be.Write(ctx, persistence.BlobKey{}, []byte("one"))
	h2, _ := be.Write(ctx, persistence.BlobKey{}, []byte("two"))
	h3, _ := be.Write(ctx, persistence.BlobKey{}, []byte("three"))

	store := &fakeBlobOrphanTable{}
	now := time.Now()
	// h1 due, h2 due, h3 not yet due.
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

	// h1 + h2 should be gone from both the backend and the store.
	if _, err := be.Read(ctx, h1); !errors.Is(err, persistence.ErrBlobNotFound) {
		t.Fatalf("h1 should be deleted; got %v", err)
	}
	if _, err := be.Read(ctx, h2); !errors.Is(err, persistence.ErrBlobNotFound) {
		t.Fatalf("h2 should be deleted; got %v", err)
	}
	// h3 still present.
	if _, err := be.Read(ctx, h3); err != nil {
		t.Fatalf("h3 should still exist; got %v", err)
	}
	if len(store.rows) != 1 || store.rows[0].Handle != string(h3) {
		t.Fatalf("store should retain only h3; got %+v", store.rows)
	}
}

// TestSweepOrphanedBlobsCrossBackendIgnored confirms an orphan row whose
// backend doesn't match the active backend is left alone (forward-compat
// for future mixed-backend deployments).
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

// TestSweepOrphanedBlobsHandlesNotFound confirms an already-deleted blob
// is treated as success — the tracker row is removed.
func TestSweepOrphanedBlobsHandlesNotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	be := persistence.NewMemoryBackend()
	store := &fakeBlobOrphanTable{}
	// Ghost handle: never written to the backend, but tracked.
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
