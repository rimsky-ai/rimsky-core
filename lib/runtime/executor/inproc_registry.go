// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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
