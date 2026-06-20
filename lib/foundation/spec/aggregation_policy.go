// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package spec

import "fmt"

type AggregationKind string

const (
	AggregationKindStrict     AggregationKind = "strict"
	AggregationKindThreshold  AggregationKind = "threshold"
	AggregationKindBestEffort AggregationKind = "best_effort"
	AggregationKindFirst      AggregationKind = "first"
)

type AggregationPolicy struct {
	Kind           AggregationKind `yaml:"kind" json:"kind"`
	CancelSiblings bool            `yaml:"cancel_siblings,omitempty" json:"cancel_siblings,omitempty"`
	MaxFailures    int             `yaml:"max_failures,omitempty" json:"max_failures,omitempty"`
}

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
