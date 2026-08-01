// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package runtime

import "sync"

type concurrencyGate struct {
	mu     sync.Mutex
	limit  int
	active int
}

func newConcurrencyGate(limit int) *concurrencyGate {
	if limit < 1 {
		limit = 1
	}
	return &concurrencyGate{limit: limit}
}

func (g *concurrencyGate) tryAcquire() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.active >= g.limit {
		return false
	}
	g.active++
	return true
}

func (g *concurrencyGate) release() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.active <= 0 {
		panic("concurrencyGate.release without a matching tryAcquire")
	}
	g.active--
}

func (g *concurrencyGate) activeCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.active
}
