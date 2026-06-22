// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package spec

import (
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/signal"
)

type ErrorTypePolicy struct {
	Action         string `yaml:"action" json:"action"`
	ReasonTemplate string `yaml:"reason_template,omitempty" json:"reason_template,omitempty"`
}

type PolicyAction = ErrorTypePolicy

type RetryBackoffConfig struct {
	Kind        BackoffKind `yaml:"kind,omitempty" json:"kind,omitempty"`
	Jitter      JitterKind  `yaml:"jitter,omitempty" json:"jitter,omitempty"`
	BaseDelayMs int         `yaml:"base_delay_ms,omitempty" json:"base_delay_ms,omitempty"`
	MaxDelayMs  int         `yaml:"max_delay_ms,omitempty" json:"max_delay_ms,omitempty"`
}

type EvaluatorState struct {
	RetryCounter int
}

type ResolvedAction struct {
	Kind     string
	DelayMs  int
	Reason   string
	NewState EvaluatorState
}

const (
	ActionRetry             = "retry"
	ActionGiveUp            = "give_up"
	ActionPass              = "pass"
	ActionReleaseAndRequeue = "release_and_requeue"
)

// @concept: error-policy
type DispatchDisposition string

const (
	DispositionEnd           DispatchDisposition = "end"
	DispositionRetry         DispatchDisposition = "retry"
	DispositionParkAsync     DispatchDisposition = "park_async"
	DispositionParkScheduled DispatchDisposition = "park_scheduled"
)

// @concept: error-policy
type SettledColor string

const (
	ColorFresh  SettledColor = "fresh"
	ColorFailed SettledColor = "failed"
	ColorParked SettledColor = "parked"
)

// @concept: error-policy
// @concept: signal
type Resolution struct {
	Signal              signal.Signal
	DispatchDisposition DispatchDisposition
	Color               SettledColor
	RetryDiscardClaims  bool
	RetryDelayMs        int
	WakeAt              time.Time
}
