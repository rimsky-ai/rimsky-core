// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package spec

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
