// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package spec

import (
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/signal"
)

type ErrorTypePolicy struct {
	Policy []PolicyAction `yaml:"policy" json:"policy"`
}

type PolicyAction struct {
	Action         string      `yaml:"action" json:"action"`
	Count          int         `yaml:"count,omitempty" json:"count,omitempty"`
	Backoff        BackoffKind `yaml:"backoff,omitempty" json:"backoff,omitempty"`
	Jitter         JitterKind  `yaml:"jitter,omitempty" json:"jitter,omitempty"`
	BaseDelayMs    int         `yaml:"base_delay_ms,omitempty" json:"base_delay_ms,omitempty"`
	MaxDelayMs     int         `yaml:"max_delay_ms,omitempty" json:"max_delay_ms,omitempty"`
	ReasonTemplate string      `yaml:"reason_template,omitempty" json:"reason_template,omitempty"`
}

type EvaluatorState struct {
	ActionIndex       int
	RetryCounter      int
	CurrentErrorClass string
}

type ResolvedAction struct {
	Kind     string
	DelayMs  int
	Reason   string
	NewState EvaluatorState
}

//	@concept: error-policy
type DispatchDisposition string

const (
	DispositionEnd DispatchDisposition = "end"
	DispositionRetry DispatchDisposition = "retry"
	DispositionParkAsync DispatchDisposition = "park_async"
	DispositionParkScheduled DispatchDisposition = "park_scheduled"
)

//	@concept: error-policy
type SettledColor string

const (
	ColorFresh SettledColor = "fresh"
	ColorFailed SettledColor = "failed"
	ColorParked SettledColor = "parked"
)

//	@concept: error-policy
//	@concept: signal
type Resolution struct {
	Signal              signal.Signal
	DispatchDisposition DispatchDisposition
	Color               SettledColor
	RetryDiscardClaims bool
	RetryDelayMs int
	WakeAt time.Time
}
