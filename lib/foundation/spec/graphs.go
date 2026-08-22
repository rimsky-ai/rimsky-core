// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package spec

// @concept: graph
type GraphSpec struct {
	Name  string            `yaml:"name" json:"name"`
	Entry string            `yaml:"entry,omitempty" json:"entry,omitempty"`
	Exit  string            `yaml:"exit,omitempty" json:"exit,omitempty"`
	Nodes []TemplateNodeDef `yaml:"nodes" json:"nodes"`
}

const MainGraphName = "main"

type HoldsBinding struct {
	From string `yaml:"from" json:"from"`
	As   string `yaml:"as,omitempty" json:"as,omitempty"`
}

type FanOutSpec struct {
	Claim            string            `yaml:"claim" json:"claim"`
	PartitionRequest string            `yaml:"partition_request" json:"partition_request"`
	Parallelism      int               `yaml:"parallelism,omitempty" json:"parallelism,omitempty"`
	ErrorPolicy      AggregationPolicy `yaml:"error_policy" json:"error_policy"`
}

// @concept: claim-lifetime
type ClaimLifetime string

const (
	ClaimLifetimeSubgraph ClaimLifetime = "subgraph"
	ClaimLifetimeDurable  ClaimLifetime = "durable"
)

type PublisherSpec struct {
	Name        string  `yaml:"name" json:"name"`
	Kind        string  `yaml:"kind" json:"kind"`
	Config      RawJSON `yaml:"config" json:"config"`
	MessageType string  `yaml:"message_type,omitempty" json:"message_type,omitempty"`
}
