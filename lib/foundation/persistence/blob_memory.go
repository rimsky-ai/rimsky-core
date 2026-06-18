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

type MemoryBackend struct {
	mu    sync.RWMutex
	blobs map[Handle][]byte
	seq   atomic.Uint64
}

var _ BlobBackend = (*MemoryBackend)(nil)

func NewMemoryBackend() *MemoryBackend {
	return &MemoryBackend{
		blobs: map[Handle][]byte{},
	}
}

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

func (b *MemoryBackend) Delete(_ context.Context, handle Handle) error {
	b.mu.Lock()
	delete(b.blobs, handle)
	b.mu.Unlock()
	return nil
}

func (b *MemoryBackend) Name() string { return "memory" }
