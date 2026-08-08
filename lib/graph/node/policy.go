// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package node

import (
	"math/rand"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

type (
	ErrorTypePolicy    = spec.ErrorTypePolicy
	EvaluatorState     = spec.EvaluatorState
	ResolvedAction     = spec.ResolvedAction
	RetryBackoffConfig = spec.RetryBackoffConfig
)

func Evaluate(
	policy *ErrorTypePolicy,
	state EvaluatorState,
	maxRetries int,
	backoff BackoffConfig,
	rng func() float64,
) ResolvedAction {
	if rng == nil {
		rng = rand.Float64
	}
	if policy == nil {
		return ResolvedAction{
			Kind:     spec.ActionGiveUp,
			Reason:   "unknown_error_class",
			NewState: state,
		}
	}
	switch policy.Action {
	case spec.ActionRetry:
		if maxRetries > 0 && state.RetryCounter >= maxRetries {
			return ResolvedAction{
				Kind:     spec.ActionGiveUp,
				Reason:   "max_retries_exhausted",
				NewState: state,
			}
		}
		newCounter := state.RetryCounter + 1
		delay := ComputeDelay(backoff, newCounter-1, rng)
		return ResolvedAction{
			Kind:     spec.ActionRetry,
			DelayMs:  delay,
			NewState: EvaluatorState{RetryCounter: newCounter},
		}
	case spec.ActionReleaseAndRequeue:
		return ResolvedAction{
			Kind:     spec.ActionReleaseAndRequeue,
			NewState: state,
		}
	case spec.ActionPass:
		return ResolvedAction{
			Kind:     spec.ActionPass,
			NewState: state,
		}
	case spec.ActionGiveUp:
		reason := policy.Reason
		if reason == "" {
			reason = spec.ActionGiveUp
		}
		return ResolvedAction{
			Kind:     spec.ActionGiveUp,
			Reason:   reason,
			NewState: state,
		}
	default:
		return ResolvedAction{
			Kind:     spec.ActionGiveUp,
			Reason:   "unknown_action_type",
			NewState: state,
		}
	}
}
