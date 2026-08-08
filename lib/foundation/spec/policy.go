// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package spec

type ErrorTypePolicy struct {
	Action string `yaml:"action" json:"action"`
	Reason string `yaml:"reason,omitempty" json:"reason,omitempty"`
}

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
