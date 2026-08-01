// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

package blobbackend

import (
	"context"
	"fmt"
	"io"
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
		return nil, io.ErrUnexpectedEOF
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

type wrongErrorOnOutOfBoundsBackend struct {
	*memoryBackend
}

func (w *wrongErrorOnOutOfBoundsBackend) ReadRange(ctx context.Context, h Handle, offset, length int64) ([]byte, error) {
	data, err := w.Read(ctx, h)
	if err != nil {
		return nil, err
	}
	if offset < 0 || offset+length > int64(len(data)) {
		return nil, fmt.Errorf("out of range")
	}
	return data[offset : offset+length], nil
}

func TestRun_WrongOutOfBoundsErrorFailsReadRangeOutOfBoundsCheck(t *testing.T) {
	be := &wrongErrorOnOutOfBoundsBackend{memoryBackend: newMemoryBackend()}
	results := Run(context.Background(), be)
	r := findBlobRow(t, results, "range read out of bounds returns io.ErrUnexpectedEOF")
	if r.Err == nil {
		t.Fatal("range-read-out-of-bounds check must fail when ReadRange does not return io.ErrUnexpectedEOF past the blob end")
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

func TestRun_MissingDeleteFailsDeleteThenReadRangeCheck(t *testing.T) {
	be := &nonDeletingBackend{memoryBackend: newMemoryBackend()}
	results := Run(context.Background(), be)
	r := findBlobRow(t, results, "delete then range-read returns ErrBlobNotFound")
	if r.Err == nil {
		t.Fatal("delete-then-range-read check must fail when Delete does not actually remove the blob")
	}
}

type noBoundsCheckBackend struct {
	*memoryBackend
}

func (n *noBoundsCheckBackend) ReadRange(ctx context.Context, h Handle, offset, length int64) ([]byte, error) {
	data, err := n.memoryBackend.Read(ctx, h)
	if err != nil {
		return nil, err
	}
	end := offset + length
	if end > int64(len(data)) {
		end = int64(len(data))
	}
	if offset > end {
		offset = end
	}
	return data[offset:end], nil
}

func TestRun_NoBoundsCheckFailsOutOfRangeCheck(t *testing.T) {
	be := &noBoundsCheckBackend{memoryBackend: newMemoryBackend()}
	results := Run(context.Background(), be)
	r := findBlobRow(t, results, "range read out of bounds returns io.ErrUnexpectedEOF")
	if r.Err == nil {
		t.Fatal("out-of-range check must fail when ReadRange silently truncates instead of erroring")
	}
}

type emptyHandleBackend struct {
	*memoryBackend
}

func (e *emptyHandleBackend) Write(ctx context.Context, hint string, data []byte) (Handle, error) {
	if _, err := e.memoryBackend.Write(ctx, hint, data); err != nil {
		return "", err
	}
	return "", nil
}

func TestRun_EmptyHandleFailsSelfDescribingCheck(t *testing.T) {
	be := &emptyHandleBackend{memoryBackend: newMemoryBackend()}
	results := Run(context.Background(), be)
	r := findBlobRow(t, results, "handle is a non-empty self-describing string")
	if r.Err == nil {
		t.Fatal("self-describing-handle check must fail when Write returns an empty handle")
	}
}

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
