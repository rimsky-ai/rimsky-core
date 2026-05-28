// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// memory_lister.go — in-memory ObjectLister fixture. Production code
// registers this under the "memory" backend so smoke tests + scenario
// tests can drive observations without LocalStack. Backend-specific
// listers (s3, gcs, azure) live in their own files and are wired in
// main() when the corresponding SDK is compiled in.

package main

import (
	"context"
	"strings"
	"sync"
)

// MemoryLister is a thread-safe in-memory ObjectLister fixture. Tests
// call Put() to seed objects and the sensor polls Lists() like a real
// backend. Production main() registers it under "memory" for smoke
// testing.
type MemoryLister struct {
	mu   sync.Mutex
	data map[string][]ObjectMeta // keyed by bucket
}

// NewMemoryLister returns an empty in-memory fixture.
func NewMemoryLister() *MemoryLister {
	return &MemoryLister{data: make(map[string][]ObjectMeta)}
}

// Put adds an object to the fixture. Used by tests.
func (m *MemoryLister) Put(bucket string, obj ObjectMeta) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[bucket] = append(m.data[bucket], obj)
}

// List returns all objects in the bucket whose name starts with the
// prefix.
func (m *MemoryLister) List(_ context.Context, bucket, prefix string) ([]ObjectMeta, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]ObjectMeta, 0, len(m.data[bucket]))
	for _, o := range m.data[bucket] {
		if prefix == "" || strings.HasPrefix(o.Name, prefix) {
			out = append(out, o)
		}
	}
	return out, nil
}
