// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package spec

import (
	"time"

	"github.com/fallguyconsulting/rimsky/foundation/signal"
)

// ErrorTypePolicy and PolicyAction describe the per-error-class repair
// chains declared in a node's error_types template block.
//
// The Evaluate function that advances the chain lives in graph/node;
// this package defines only the persistable data shape.
type ErrorTypePolicy struct {
	Policy []PolicyAction `yaml:"policy" json:"policy"`
}

// PolicyAction is one entry in a node's per-error-class repair chain.
//
// Action vocabulary (4-value set; reshape per
// `spec:2026-05-23-signal-taxonomy-and-policy-decoupling-design`):
//   - "pass"                       — settle the run as fresh; the chain
//     advances so a subsequent same-class error doesn't pass again.
//   - "retry"                      — re-enqueue; held claims are
//     preserved (the runner does NOT fire Abandon on the store).
//   - "discard_claims_then_retry"  — re-enqueue; the runner fires
//     Abandon on each held claim before re-enqueue (staged stores
//     undo write-side state; direct stores degenerate to keep-then-
//     retry because direct writes cannot be undone).
//   - "give_up"                    — settle the run as failed; per-
//     claim release fires Abandon on the store.
//
// Historical vocabulary retired by the 2026-05-23 reshape:
//   - `invalidate(targets)`     — retired 2026-05-14; receivers declare
//     cascade coupling via `subscribes: [{node: <sender>, type:
//     terminal/error/<class>}]`.
//   - `discard_then_retry`      — renamed to `discard_claims_then_retry`
//     for clarity (the verb is on the claim handles, not on the node
//     row).
//   - `resume_then_retry`       — deleted; behaviorally identical to
//     `discard_claims_then_retry` under the post-E.2 wire shape, so
//     the duplicate slot retires.
type PolicyAction struct {
	Action         string      `yaml:"action" json:"action"`
	Count          int         `yaml:"count,omitempty" json:"count,omitempty"`
	Backoff        BackoffKind `yaml:"backoff,omitempty" json:"backoff,omitempty"`
	Jitter         JitterKind  `yaml:"jitter,omitempty" json:"jitter,omitempty"`
	BaseDelayMs    int         `yaml:"base_delay_ms,omitempty" json:"base_delay_ms,omitempty"`
	MaxDelayMs     int         `yaml:"max_delay_ms,omitempty" json:"max_delay_ms,omitempty"`
	ReasonTemplate string      `yaml:"reason_template,omitempty" json:"reason_template,omitempty"`
}

// EvaluatorState is the persisted per-node, per-error-class policy chain
// position. Stored on the rimsky_nodes row as (current_error_class,
// retry_counter, action_index).
type EvaluatorState struct {
	ActionIndex       int
	RetryCounter      int
	CurrentErrorClass string
}

// ResolvedAction is the outcome of one Evaluate call. Kind carries the
// runtime intent the runner branches on:
//
//   - "pass"                       — terminal: settle the run as fresh.
//     The chain advances so a subsequent same-class error doesn't pass.
//   - "retry"                      — re-enqueue; held claims preserved.
//   - "discard_claims_then_retry"  — re-enqueue; runner fires Abandon
//     on each held claim before re-enqueue.
//   - "give_up"                    — terminal: settle the run as failed.
type ResolvedAction struct {
	Kind     string
	DelayMs  int
	Reason   string
	NewState EvaluatorState
}

// DispatchDisposition is what becomes of the current dispatch after a
// policy resolution. Carried on `Resolution.DispatchDisposition`.
//
//	@concept: error-policy
type DispatchDisposition string

const (
	// DispositionEnd settles the dispatch (give_up | pass). The run row's
	// color is taken from `Resolution.Color`.
	DispositionEnd DispatchDisposition = "end"
	// DispositionRetry re-enqueues a fresh dispatch row with the
	// configured backoff. The retry preserves held claims unless
	// `Resolution.RetryDiscardClaims` is set.
	DispositionRetry DispatchDisposition = "retry"
	// DispositionParkAsync acknowledges an executor-issued async park.
	// The run row settles parked; the dispatch terminates without a
	// re-enqueue (the eventual callback drives the next dispatch).
	DispositionParkAsync DispatchDisposition = "park_async"
	// DispositionParkScheduled parks the dispatch with a wake at
	// `Resolution.WakeAt`. The scheduler unparks at that time.
	DispositionParkScheduled DispatchDisposition = "park_scheduled"
)

// SettledColor is the run-row's settled color; only meaningful when
// `Resolution.DispatchDisposition` is End or Park*.
//
//	@concept: error-policy
type SettledColor string

const (
	// ColorFresh — the run settled successfully (Success terminal, or
	// a `pass` resolution that absolved an Error).
	ColorFresh SettledColor = "fresh"
	// ColorFailed — the run settled with an Error that ran out of
	// retries (give_up or chain exhaustion).
	ColorFailed SettledColor = "failed"
	// ColorParked — the run is parked awaiting wake or callback.
	ColorParked SettledColor = "parked"
)

// Resolution is the unified output of one policy-resolution decision.
// Decouples the conflated `PolicyAction` / `ResolvedAction` pair into
// three orthogonal axes:
//
//   - Signal: the signal envelope emitted to the cascade walker and the
//     audit log (per `concept:signal`).
//   - DispatchDisposition: what becomes of this dispatch (end | retry |
//     park_async | park_scheduled).
//   - Color: the run-row's settled state (fresh | failed | parked);
//     only meaningful when the disposition is End or Park*.
//
// Built by the runtime's error-policy resolver from a `ResolvedAction`
// plus the originating error class, attempt counter, and payload (see
// `runtime/runner_error_policy.go::buildResolution`).
//
//	@concept: error-policy
//	@concept: signal
type Resolution struct {
	Signal              signal.Signal
	DispatchDisposition DispatchDisposition
	Color               SettledColor
	// RetryDiscardClaims is set when the resolution is a retry that
	// should release held claims before re-enqueue (i.e.
	// `discard_claims_then_retry`).
	RetryDiscardClaims bool
	// RetryDelayMs is the configured backoff for a retry disposition.
	RetryDelayMs int
	// WakeAt is the scheduled wake time for a park_scheduled disposition.
	WakeAt time.Time
}
