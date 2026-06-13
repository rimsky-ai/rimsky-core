// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package persistence

import (
	"context"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
)

// MemoryBackend is an in-process map[Handle][]byte BlobBackend used for
// development and testing. It is REJECTED at startup unless
// RIMSKY_PROCESS_ROLE=unified — the single-process mode, set only by
// rimsky-entrypoint's no-command all-in-one path, where all three
// roles share one process and the production construction path hands
// every role's driver the same process-shared instance. A per-role
// process cannot share an in-process map with its siblings — the
// Driver in each process would hold an independent map, silently
// breaking cross-process attribute reads.
//
// The reject gate lives in ValidateBlobConfig; the constructor here
// assumes the gate already passed. Tests that exercise this backend
// directly (without going through ValidateBlobConfig) are expected to
// run in a single process.
type MemoryBackend struct {
	mu    sync.RWMutex
	blobs map[Handle][]byte
	seq   atomic.Uint64
}

// Compile-time interface check.
var _ BlobBackend = (*MemoryBackend)(nil)

// NewMemoryBackend constructs an empty MemoryBackend.
func NewMemoryBackend() *MemoryBackend {
	return &MemoryBackend{
		blobs: map[Handle][]byte{},
	}
}

// Write stores a private copy of bytes under a freshly minted handle
// and returns the handle. The copy guards against caller mutation
// after Write returns.
func (b *MemoryBackend) Write(_ context.Context, _ BlobKey, bytes []byte) (Handle, error) {
	cp := make([]byte, len(bytes))
	copy(cp, bytes)
	id := b.seq.Add(1)
	h := Handle(fmt.Sprintf("mem:%d", id))
	b.mu.Lock()
	b.blobs[h] = cp
	b.mu.Unlock()
	return h, nil
}

// Read returns a private copy of the stored bytes (caller-mutation safe)
// or ErrBlobNotFound when the handle is unknown.
func (b *MemoryBackend) Read(_ context.Context, handle Handle) ([]byte, error) {
	b.mu.RLock()
	bytes, ok := b.blobs[handle]
	b.mu.RUnlock()
	if !ok {
		return nil, ErrBlobNotFound
	}
	out := make([]byte, len(bytes))
	copy(out, bytes)
	return out, nil
}

// ReadRange returns a private copy of the requested byte range or
// ErrBlobNotFound when the handle is unknown. Returns io.ErrUnexpectedEOF
// when offset+length exceeds blob size.
func (b *MemoryBackend) ReadRange(_ context.Context, handle Handle, offset, length int64) ([]byte, error) {
	b.mu.RLock()
	bytes, ok := b.blobs[handle]
	b.mu.RUnlock()
	if !ok {
		return nil, ErrBlobNotFound
	}
	if offset < 0 || length < 0 {
		return nil, fmt.Errorf("blob memory: ReadRange: negative offset=%d length=%d", offset, length)
	}
	end := offset + length
	if int64(len(bytes)) < end {
		return nil, io.ErrUnexpectedEOF
	}
	out := make([]byte, length)
	copy(out, bytes[offset:end])
	return out, nil
}

// Delete removes the handle's bytes. Idempotent: deleting an absent
// handle returns nil.
func (b *MemoryBackend) Delete(_ context.Context, handle Handle) error {
	b.mu.Lock()
	delete(b.blobs, handle)
	b.mu.Unlock()
	return nil
}

// Name returns "memory".
func (b *MemoryBackend) Name() string { return "memory" }
