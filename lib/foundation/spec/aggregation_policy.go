// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package spec

type AggregationKind string

const (
	AggregationKindStrict     AggregationKind = "strict"
	AggregationKindThreshold  AggregationKind = "threshold"
	AggregationKindBestEffort AggregationKind = "best_effort"
	AggregationKindFirst      AggregationKind = "first"
)

type AggregationPolicy struct {
	Kind        AggregationKind `yaml:"kind" json:"kind"`
	MaxFailures int             `yaml:"max_failures,omitempty" json:"max_failures,omitempty"`
}
