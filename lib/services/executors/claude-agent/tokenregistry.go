// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package claudeagent

import (
	"encoding/json"
	"sync"
)

type PriorDispatchDisposition string

const (
	PriorStaleRecovery   PriorDispatchDisposition = "stale_recovery"
	PriorRetryAfterError PriorDispatchDisposition = "retry_after_error"
	PriorRecalculate     PriorDispatchDisposition = "recalculate"
)

type DispatchContextSnapshot struct {
	DispatchID               string                    `json:"dispatch_id"`
	RunScopeID               string                    `json:"run_scope_id"`
	PriorDispatchID          *string                   `json:"prior_dispatch_id"`
	PriorDispatchDisposition *PriorDispatchDisposition `json:"prior_dispatch_disposition"`
}

type WireContractViolation struct {
	Kind                         string `json:"kind"`
	Message                      string `json:"message"`
	PriorDispatchID              string `json:"prior_dispatch_id"`
	PriorDispatchDispositionWire string `json:"prior_dispatch_disposition_wire"`
}

type DispatchContextWarn func(event WireContractViolation)

func NewDispatchContextSnapshot(
	dispatchID string,
	runScopeID string,
	priorDispatchID string,
	priorDispatchDispositionWire string,
	warn DispatchContextWarn,
) DispatchContextSnapshot {
	disposition := mapDispositionFromWire(priorDispatchDispositionWire)
	var priorID *string
	if priorDispatchID != "" {
		priorID = &priorDispatchID
	}
	if priorID != nil && disposition == nil && warn != nil {
		warn(WireContractViolation{
			Kind: "wire_contract_violation",
			Message: "prior_dispatch_id present but prior_dispatch_disposition is " +
				"PRIOR_NONE / empty / unknown; the supervisor must send a typed " +
				"disposition whenever a prior identifier is set",
			PriorDispatchID:              priorDispatchID,
			PriorDispatchDispositionWire: priorDispatchDispositionWire,
		})
	}
	snapshot := DispatchContextSnapshot{
		DispatchID:      dispatchID,
		RunScopeID:      runScopeID,
		PriorDispatchID: priorID,
	}
	if priorID != nil {
		snapshot.PriorDispatchDisposition = disposition
	}
	return snapshot
}

func mapDispositionFromWire(wire string) *PriorDispatchDisposition {
	var d PriorDispatchDisposition
	switch wire {
	case "PRIOR_STALE_RECOVERY":
		d = PriorStaleRecovery
	case "PRIOR_RETRY_AFTER_ERROR":
		d = PriorRetryAfterError
	case "PRIOR_RECALCULATE":
		d = PriorRecalculate
	default:
		return nil
	}
	return &d
}

type CompleteResult struct {
	Accepted bool
	Errors   map[string][]string
}

func (r CompleteResult) MarshalJSON() ([]byte, error) {
	if r.Accepted {
		return json.Marshal(map[string]any{"status": "accepted"})
	}
	return json.Marshal(map[string]any{"status": "rejected", "errors": r.Errors})
}

type ScheduleTeardown func(td func() error)

type TokenEntry struct {
	RunID             string
	AttributesAtSpawn map[string]any
	DispatchContext   DispatchContextSnapshot
	CancelToken       string
	NodeID            string
	CallbackURL       string
	OnComplete        func(attributesDelta map[string]any, changed bool, changeSummary *string, signoffs []string, scheduleTeardown ScheduleTeardown) (CompleteResult, error)
	OnBlocked         func(reason string, context any, scheduleTeardown ScheduleTeardown) error
	OnError           func(errorClass string, payload any, scheduleTeardown ScheduleTeardown) error
	OnPark            func(reason string, reasonNote *string, resumeAt *string, scheduleTeardown ScheduleTeardown) error
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
