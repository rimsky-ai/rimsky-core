// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package locks

import "sync"

type namedRegistry[T any] struct {
	mu    sync.RWMutex
	items map[string]T
}

func newNamedRegistry[T any]() namedRegistry[T] {
	return namedRegistry[T]{items: make(map[string]T)}
}

func (r *namedRegistry[T]) add(name string, item T) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items[name] = item
}

func (r *namedRegistry[T]) get(name string) (T, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	item, ok := r.items[name]
	return item, ok
}

func (r *namedRegistry[T]) names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.items))
	for name := range r.items {
		out = append(out, name)
	}
	return out
}

func (r *namedRegistry[T]) copyMap() map[string]T {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]T, len(r.items))
	for name, item := range r.items {
		out[name] = item
	}
	return out
}

func (r *namedRegistry[T]) closeAll() {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, item := range r.items {
		if c, ok := any(item).(closer); ok {
			c.Close()
		}
	}
}
