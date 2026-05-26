// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package spec

import "encoding/json"

// Top-level template DSL additions per spec
// .ok-planner/specs/2026-05-15-data-platform-extensions-design.md.
//
// The canonical TemplateSpec is being extended to carry `graphs:` (one
// `main` graph plus zero or more named sub-graphs) and `sensors:`
// (per-instance sensor watch declarations). Pre-v1 the existing
// `Nodes []TemplateNodeDef` field is retained as the legacy shape; the
// template canonicalizer accepts either form and rejects mixing them.

// GraphSpec is one graph in a template. Per spec §Sub-graphs, the
// reserved name `main` is the top-level graph; all other graphs are
// sub-graphs that MUST declare `entry:` and `exit:` aliases.
//
// @concept: graph
type GraphSpec struct {
	Name  string            `yaml:"name" json:"name"`
	Entry string            `yaml:"entry,omitempty" json:"entry,omitempty"`
	Exit  string            `yaml:"exit,omitempty" json:"exit,omitempty"`
	Nodes []TemplateNodeDef `yaml:"nodes" json:"nodes"`
}

// MainGraphName is the reserved graph name for the top-level graph in
// a template. Per spec §Sub-graphs / Identity, exactly one graph in a
// template MUST be named `main`; canonicalization rejects others.
const MainGraphName = "main"

// HoldsBinding declares that a node co-holds an upstream claim. The
// outer key in the `holds:` block (in YAML) is the local alias; the
// value is `{from: <upstream-node-alias>}`. The canonicalizer
// validates that `From` points to an upstream dependency and that the
// upstream declares the referenced claim alias in its `claims:` block.
type HoldsBinding struct {
	// From names the upstream node whose claim this node co-holds.
	From string `yaml:"from" json:"from"`
	// As is the optional local alias under which the co-held address
	// appears in the leaf's ExecuteRequest. Defaults to the outer key.
	As string `yaml:"as,omitempty" json:"as,omitempty"`
}

// FanOutSpec declares that a node fans out across sub-scopes of one of
// its `claims:` aliases. Per spec §Fan-out template DSL.
type FanOutSpec struct {
	// Claim references a claim alias declared on the node (in `claims:`
	// or `holds:`). The producer of this claim must advertise
	// supports_split_scope.
	Claim string `yaml:"claim" json:"claim"`
	// PartitionRequest is the producer-interpreted bytes that drive
	// SplitScope. May be a substitution template (e.g.
	// "{{trigger.message.payload.partition_request_override | default: ...}}").
	// At canonicalization the literal/template string is recorded;
	// substitution happens at runtime.
	PartitionRequest string `yaml:"partition_request" json:"partition_request"`
	// Parallelism caps the number of in-flight leaf runs. Zero means
	// unlimited.
	Parallelism int `yaml:"parallelism,omitempty" json:"parallelism,omitempty"`
	// ErrorPolicy is the per-fan-out aggregation policy.
	ErrorPolicy AggregationPolicy `yaml:"error_policy" json:"error_policy"`
}

// ClaimLifetime is the per-claim lifetime enum: `subgraph` (default;
// claim auto-terminals at holding-subgraph completion) or `durable`
// (requires DataProcessing on the producer; row persists past terminal
// for asset semantics).
//
// @concept: claim-lifetime
type ClaimLifetime string

const (
	ClaimLifetimeSubgraph ClaimLifetime = "subgraph"
	ClaimLifetimeDurable  ClaimLifetime = "durable"
)

// PublisherSpec is one publisher-subscription declared on a template.
// Per spec
// .ok-planner/specs/2026-05-17-sensor-messaging-unification-design.md
// §Publisher protocol unification. At instance creation, rimsky
// generates a publisher_subscription_id per entry and calls
// Publisher.Subscribe on the addressed publisher service; the
// subscription lives in table:rimsky_publisher_subscriptions.
//
// Routing fields (`target_node`, `message_kind`) are inline; there is
// no `on_observation:` substruct (deleted in the 2026-05-17 rename).
type PublisherSpec struct {
	Name        string          `yaml:"name" json:"name"`
	Kind        string          `yaml:"kind" json:"kind"`
	Config      json.RawMessage `yaml:"config" json:"config"`
	TargetNode  string          `yaml:"target_node" json:"target_node"`
	MessageKind string          `yaml:"message_kind,omitempty" json:"message_kind,omitempty"`
}
