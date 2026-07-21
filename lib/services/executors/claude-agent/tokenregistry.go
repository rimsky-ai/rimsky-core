// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package claudeagent

import (
	"sync"
)

type ScheduleTeardown func(td func() error)

type TokenEntry struct {
	SessionID         string
	AttributesAtSpawn map[string]any
	DispatchContext   DispatchContextSnapshot
	CancelToken       string
	NodeID            string
	CallbackURL       string
	OnComplete        func(attributesDelta map[string]any, changed bool, changeSummary *string, signoffs []string, scheduleTeardown ScheduleTeardown) (CompleteResult, error)
	OnBlocked         func(reason string, context any, scheduleTeardown ScheduleTeardown) error
	OnError           func(errorClass string, payload any, scheduleTeardown ScheduleTeardown) error
	OnPark            func(resumeAtISO string, scheduleTeardown ScheduleTeardown) error
}

type TokenRegistry struct {
	mu sync.RWMutex
	m  map[string]*TokenEntry
}

func NewTokenRegistry() *TokenRegistry {
	return &TokenRegistry{m: make(map[string]*TokenEntry)}
}

func (r *TokenRegistry) Register(token string, entry *TokenEntry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.m[token] = entry
}

func (r *TokenRegistry) Lookup(token string) (*TokenEntry, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry, ok := r.m[token]
	return entry, ok
}

func (r *TokenRegistry) Release(token string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.m, token)
}

func (r *TokenRegistry) Size() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.m)
}
