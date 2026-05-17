// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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

// ClaimLifetime constants for the per-claim `lifetime:` field
// (default: "subgraph"; "durable" requires DataProcessing on the
// producer).
const (
	ClaimLifetimeSubgraph = "subgraph"
	ClaimLifetimeDurable  = "durable"
)

// SensorSpec is one sensor watch declared on a template. Per spec
// §Sensors / Per-instance parameterization. At instance creation,
// rimsky generates a watch_id per sensor and calls Sensor.StartWatch
// on the addressed sensor service; the watch lives in
// table:rimsky_sensor_watches.
type SensorSpec struct {
	Name          string            `yaml:"name" json:"name"`
	Kind          string            `yaml:"kind" json:"kind"`
	Config        json.RawMessage   `yaml:"config" json:"config"`
	OnObservation OnObservationSpec `yaml:"on_observation" json:"on_observation"`
}

// OnObservationSpec declares how a sensor observation becomes a
// message envelope on the unified message queue. At observation time
// rimsky applies PayloadTemplate substitution against the observation
// body and constructs a {kind: MessageKind, target: TargetNode}
// envelope.
type OnObservationSpec struct {
	TargetNode      string         `yaml:"target_node" json:"target_node"`
	MessageKind     string         `yaml:"message_kind" json:"message_kind"`
	PayloadTemplate map[string]any `yaml:"payload_template,omitempty" json:"payload_template,omitempty"`
}
