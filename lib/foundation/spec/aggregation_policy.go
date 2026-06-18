// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package spec

import "fmt"

type AggregationPolicy struct {
	Kind           string `yaml:"kind" json:"kind"`
	CancelSiblings bool   `yaml:"cancel_siblings,omitempty" json:"cancel_siblings,omitempty"`
	MaxFailures    int    `yaml:"max_failures,omitempty" json:"max_failures,omitempty"`
}

const (
	AggregationKindStrict        = "strict"
	AggregationKindThreshold     = "threshold"
	AggregationKindBestEffort    = "best_effort"
	AggregationKindFirst         = "first"
	AggregationKindCarryVerbatim = "carry_verbatim"
)

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
	case AggregationKindBestEffort, AggregationKindFirst, AggregationKindCarryVerbatim:
		if p.CancelSiblings {
			return fmt.Errorf("aggregation_policy: cancel_siblings is only meaningful for kind=strict")
		}
		if p.MaxFailures != 0 {
			return fmt.Errorf("aggregation_policy: max_failures is only meaningful for kind=threshold")
		}
	case "":
		return fmt.Errorf("aggregation_policy: kind is required")
	default:
		return fmt.Errorf("aggregation_policy: unknown kind %q (want strict|threshold|best_effort|first|carry_verbatim)", p.Kind)
	}
	return nil
}
