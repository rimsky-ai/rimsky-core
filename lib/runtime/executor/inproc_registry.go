// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package executor

import (
	"fmt"
	"sort"
	"sync"
)

// @concept: executor
type InProcessRegistry struct {
	mu       sync.RWMutex
	handlers map[string]InProcessHandler
}

func NewInProcessRegistry() *InProcessRegistry {
	return &InProcessRegistry{handlers: map[string]InProcessHandler{}}
}

func (r *InProcessRegistry) Register(url string, h InProcessHandler) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.handlers[url]; exists {
		return fmt.Errorf("InProcessRegistry: duplicate registration for %q", url)
	}
	r.handlers[url] = h
	return nil
}

func (r *InProcessRegistry) Lookup(url string) (InProcessHandler, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	h, ok := r.handlers[url]
	return h, ok
}

func (r *InProcessRegistry) RegisteredURLs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.handlers))
	for url := range r.handlers {
		out = append(out, url)
	}
	sort.Strings(out)
	return out
}
