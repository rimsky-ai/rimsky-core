// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package main

import (
	"context"
	"strings"
	"sync"
)

type MemoryLister struct {
	mu   sync.Mutex
	data map[string][]ObjectMeta
}

func NewMemoryLister() *MemoryLister {
	return &MemoryLister{data: make(map[string][]ObjectMeta)}
}

func (m *MemoryLister) Put(bucket string, obj ObjectMeta) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[bucket] = append(m.data[bucket], obj)
}

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
