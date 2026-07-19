// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package blobbackend

import (
	"context"
	"fmt"
	"sync"
	"testing"
)

type memoryBackend struct {
	mu       sync.Mutex
	blobs    map[Handle][]byte
	next     int
	readStub func(Handle, []byte) []byte
}

func newMemoryBackend() *memoryBackend {
	return &memoryBackend{blobs: map[Handle][]byte{}}
}

func (m *memoryBackend) Write(_ context.Context, hint string, data []byte) (Handle, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.next++
	h := Handle(fmt.Sprintf("%s-%d", hint, m.next))
	cp := append([]byte(nil), data...)
	m.blobs[h] = cp
	return h, nil
}

func (m *memoryBackend) Read(_ context.Context, h Handle) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	data, ok := m.blobs[h]
	if !ok {
		return nil, ErrBlobNotFound
	}
	if m.readStub != nil {
		return m.readStub(h, data), nil
	}
	return append([]byte(nil), data...), nil
}

func (m *memoryBackend) ReadRange(ctx context.Context, h Handle, offset, length int64) ([]byte, error) {
	data, err := m.Read(ctx, h)
	if err != nil {
		return nil, err
	}
	if offset < 0 || offset+length > int64(len(data)) {
		return nil, fmt.Errorf("blobbackend/test: range [%d:%d) out of bounds for %d-byte blob", offset, offset+length, len(data))
	}
	return data[offset : offset+length], nil
}

func (m *memoryBackend) Delete(_ context.Context, h Handle) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.blobs, h)
	return nil
}

func TestRun_HonestMemoryBackend_AllChecksPass(t *testing.T) {
	results := Run(context.Background(), newMemoryBackend())
	if len(results) == 0 {
		t.Fatal("Run returned no results")
	}
	for _, r := range results {
		if r.Err != nil {
			t.Errorf("check %q: unexpected failure: %v", r.Name, r.Err)
		}
	}
}

func TestRun_CorruptingReadFailsRoundtripChecks(t *testing.T) {
	be := newMemoryBackend()
	be.readStub = func(_ Handle, data []byte) []byte {
		corrupted := append([]byte(nil), data...)
		if len(corrupted) > 0 {
			corrupted[0] ^= 0xFF
		}
		return corrupted
	}
	results := Run(context.Background(), be)
	found := false
	for _, r := range results {
		if r.Name == "round-trip 1KB" {
			found = true
			if r.Err == nil {
				t.Error("round-trip 1KB must fail when Read returns bytes that do not match what was written")
			}
		}
	}
	if !found {
		t.Fatal(`Run did not report a "round-trip 1KB" row`)
	}
}

func TestRun_MissingDeleteFailsDeleteThenReadCheck(t *testing.T) {
	be := &nonDeletingBackend{memoryBackend: newMemoryBackend()}
	results := Run(context.Background(), be)
	r := findBlobRow(t, results, "delete then read returns ErrBlobNotFound")
	if r.Err == nil {
		t.Fatal("delete-then-read check must fail when Delete does not actually remove the blob")
	}
}

type nonDeletingBackend struct {
	*memoryBackend
}

func (n *nonDeletingBackend) Delete(context.Context, Handle) error { return nil }

func findBlobRow(t *testing.T, results []CheckResult, name string) CheckResult {
	t.Helper()
	for _, r := range results {
		if r.Name == name {
			return r
		}
	}
	names := make([]string, 0, len(results))
	for _, r := range results {
		names = append(names, r.Name)
	}
	t.Fatalf("result set missing the %q row (have: %v)", name, names)
	return CheckResult{}
}
