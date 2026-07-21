// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package claudeagent

import "encoding/json"

type PriorDispatchDisposition string

const (
	PriorStaleRecovery   PriorDispatchDisposition = "stale_recovery"
	PriorRetryAfterError PriorDispatchDisposition = "retry_after_error"
	PriorRecalculate     PriorDispatchDisposition = "recalculate"
)

type DispatchContextSnapshot struct {
	NodeRunID                string                    `json:"dispatch_id"`
	RunScopeID               string                    `json:"run_scope_id"`
	PriorNodeRunID           *string                   `json:"prior_dispatch_id"`
	PriorDispatchDisposition *PriorDispatchDisposition `json:"prior_dispatch_disposition"`
}

type WireContractViolation struct {
	Kind                         string `json:"kind"`
	Message                      string `json:"message"`
	PriorNodeRunID               string `json:"prior_dispatch_id"`
	PriorDispatchDispositionWire string `json:"prior_dispatch_disposition_wire"`
}

type DispatchContextWarn func(event WireContractViolation)

func NewDispatchContextSnapshot(
	nodeRunID string,
	runScopeID string,
	priorNodeRunID string,
	priorDispatchDispositionWire string,
	warn DispatchContextWarn,
) DispatchContextSnapshot {
	disposition := mapDispositionFromWire(priorDispatchDispositionWire)
	var priorRunPtr *string
	if priorNodeRunID != "" {
		priorRunPtr = &priorNodeRunID
	}
	if priorRunPtr != nil && disposition == nil && warn != nil {
		warn(WireContractViolation{
			Kind: "wire_contract_violation",
			Message: "prior_dispatch_id present but prior_dispatch_disposition is " +
				"PRIOR_NONE / empty / unknown; the supervisor must send a typed " +
				"disposition whenever a prior identifier is set",
			PriorNodeRunID:               priorNodeRunID,
			PriorDispatchDispositionWire: priorDispatchDispositionWire,
		})
	}
	snapshot := DispatchContextSnapshot{
		NodeRunID:      nodeRunID,
		RunScopeID:     runScopeID,
		PriorNodeRunID: priorRunPtr,
	}
	if priorRunPtr != nil {
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
