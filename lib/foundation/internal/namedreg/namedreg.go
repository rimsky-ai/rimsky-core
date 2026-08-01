// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package namedreg

import "sync"

type Registry[T any] struct {
	mu    sync.RWMutex
	items map[string]T
}

func New[T any]() Registry[T] {
	return Registry[T]{items: make(map[string]T)}
}

func (r *Registry[T]) Add(name string, item T) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items[name] = item
}

func (r *Registry[T]) Get(name string) (T, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	item, ok := r.items[name]
	return item, ok
}

func (r *Registry[T]) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.items))
	for name := range r.items {
		out = append(out, name)
	}
	return out
}

func (r *Registry[T]) CopyMap() map[string]T {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]T, len(r.items))
	for name, item := range r.items {
		out[name] = item
	}
	return out
}

type closer interface {
	Close()
}

func (r *Registry[T]) CloseAll() {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, item := range r.items {
		if c, ok := any(item).(closer); ok {
			c.Close()
		}
	}
}
