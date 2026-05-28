// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package spec

import "fmt"

// AggregationPolicy is the per-fan-out / per-sub-graph error-policy
// snapshot persisted on col:rimsky_node_runs.aggregation_policy. The
// state-propagation engine in runtime/ consults it when computing the
// parent run's aggregated state from its children.
//
// Spec .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
// §Fan-out template DSL and §Aggregation for sub-graphs.
type AggregationPolicy struct {
	// Kind is one of "strict", "threshold", "best_effort", "first".
	Kind string `json:"kind"`
	// CancelSiblings is meaningful only when Kind == "strict". When
	// true, the first child failure triggers Abandon of all sibling
	// claims and transitions siblings to failed{error_class: "sibling_failed"}.
	CancelSiblings bool `json:"cancel_siblings,omitempty"`
	// MaxFailures is meaningful only when Kind == "threshold". The
	// parent transitions to failed when the count of failed children
	// exceeds this value.
	MaxFailures int `json:"max_failures,omitempty"`
}

// AggregationPolicy.Kind constants.
const (
	AggregationKindStrict     = "strict"
	AggregationKindThreshold  = "threshold"
	AggregationKindBestEffort = "best_effort"
	AggregationKindFirst      = "first"
)

// Validate returns an error if p is not a well-formed policy. Used at
// template registration.
func (p AggregationPolicy) Validate() error {
	switch p.Kind {
	case AggregationKindStrict:
		if p.MaxFailures != 0 {
			return fmt.Errorf("aggregation_policy: max_failures is only meaningful for kind=threshold")
		}
	case AggregationKindThreshold:
		if p.CancelSiblings {
			return fmt.Errorf("aggregation_policy: cancel_siblings is only meaningful for kind=strict")
		}
		if p.MaxFailures < 1 {
			return fmt.Errorf("aggregation_policy: kind=threshold requires max_failures >= 1")
		}
	case AggregationKindBestEffort, AggregationKindFirst:
		if p.CancelSiblings {
			return fmt.Errorf("aggregation_policy: cancel_siblings is only meaningful for kind=strict")
		}
		if p.MaxFailures != 0 {
			return fmt.Errorf("aggregation_policy: max_failures is only meaningful for kind=threshold")
		}
	case "":
		return fmt.Errorf("aggregation_policy: kind is required")
	default:
		return fmt.Errorf("aggregation_policy: unknown kind %q (want strict|threshold|best_effort|first)", p.Kind)
	}
	return nil
}
