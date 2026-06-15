// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package spec

import "encoding/json"

// TemplateSpec is the top-level template structure, parsed from YAML
// or JSON.
//
// Pre-spec-2026-05-15 templates declared a single flat list under
// `nodes:`; the data-platform-extensions spec introduces `graphs:`
// (one `main` graph plus zero or more named sub-graphs with
// entry/exit aliases) and `publishers:` (per-instance publisher
// subscriptions; renamed from the pre-2026-05-17 `sensors:` block).
// Both shapes are accepted at parse time; the canonicalizer rejects
// templates that declare both (the legacy flat `Nodes` and a
// non-empty `Graphs`). Per spec
// .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
// §Sub-graphs.
type TemplateSpec struct {
	Name                string            `yaml:"name" json:"name"`
	Version             string            `yaml:"version" json:"version"`
	Description         string            `yaml:"description,omitempty" json:"description,omitempty"`
	FrameResolutionMode string            `yaml:"frame_resolution_mode" json:"frame_resolution_mode"`
	FrameTimeoutMs      int64             `yaml:"frame_timeout_ms,omitempty" json:"frame_timeout_ms,omitempty"`
	Nodes               []TemplateNodeDef `yaml:"nodes,omitempty" json:"nodes,omitempty"`
	// Graphs is the post-spec-2026-05-15 nested form. When non-empty,
	// the canonicalizer rejects any non-empty `Nodes` field.
	Graphs []GraphSpec `yaml:"graphs,omitempty" json:"graphs,omitempty"`
	// Publishers declares per-instance publisher-subscriptions. Each
	// entry seeds one row in table:rimsky_publisher_subscriptions at
	// instance creation.
	Publishers []PublisherSpec `yaml:"publishers,omitempty" json:"publishers,omitempty"`
	// ParamsSchema is a JSON Schema describing the params bag.
	ParamsSchema map[string]any `yaml:"params_schema,omitempty" json:"params_schema,omitempty"`
	ParamsRedact []string       `yaml:"params_redact,omitempty" json:"params_redact,omitempty"`

	// LateBindServices declares service names whose registration-time
	// existence and schema checks are deferred to dispatch. Names in
	// this list bypass the discovery-cache check and the
	// expected_attributes_schema cross-check during template
	// registration. At dispatch, the spawned binary's Capabilities
	// provides the schema; the proxy validates resolved attribute
	// values against it; mismatch → contract_mismatch error.
	//
	// Stored inside the canonical spec bytes — changes participate
	// in the template hash (concept:template's content-addressing
	// invariant is preserved).
	LateBindServices []string `yaml:"late_bind_services,omitempty" json:"late_bind_services,omitempty"`

	// Defaults holds template-author attribute baselines (L1 in the
	// attribute override merge), merged into per-node effective schemas
	// at registration. See `TemplateDefaults` for the shape; absent
	// means "no template-author defaults".
	//
	// @concept: attribute
	Defaults *TemplateDefaults `yaml:"defaults,omitempty" json:"defaults,omitempty"`
}

// TemplateDefaults declares template-author baselines applied at
// template registration into per-node effective attribute schemas.
// See concept:attribute for the four-layer merge order:
//
//	L1: template.defaults.attributes.by_executor[<executor>]
//	      → folded into the effective schema's `default:` values at registration
//	L2: node.attributes.schema
//	      → per-node declaration; overrides L1
//	L3: instance.attribute_overrides.by_executor[<executor>]
//	      → operator overrides at dispatch
//	L4: instance.attribute_overrides.by_node[<node>]
//	      → most specific; wins over L3
type TemplateDefaults struct {
	Attributes *TemplateAttributeDefaults `yaml:"attributes,omitempty" json:"attributes,omitempty"`
}

// TemplateAttributeDefaults carries per-executor attribute-value
// baselines. These contribute `default:` values to the effective
// schema at template registration (L1 in the override merge); per-node
// declarations (L2) override these where they conflict.
//
// Only `by_executor` is supported; per-node defaults are expressed by
// declaring `default:` on the node's attribute schema property
// directly (declaring a `by_node` defaults layer would be redundant
// with that).
//
// @concept: attribute
type TemplateAttributeDefaults struct {
	ByExecutor map[string]map[string]any `yaml:"by_executor,omitempty" json:"by_executor,omitempty"`
}

// @concept: frame
// @constraint: frame-resolution policy (coalesce vs. serial_queue) is part of the template surface; the timeout floor and default come from the design.
const (
	FrameResolutionCoalesce    = "coalesce"
	FrameResolutionSerialQueue = "serial_queue"
	// @constraint: FrameTimeoutDefaultMs is the default per-frame timeout (10 minutes).
	FrameTimeoutDefaultMs = int64(600000)
	// @constraint: FrameTimeoutMinMs is the hard floor for per-frame timeout (60 seconds).
	FrameTimeoutMinMs = int64(60000)
)

// TemplateNodeDef is one node in a template. An empty Executor means
// the node is a pure-cascade or pure-infra node — it runs no executor
// handler and is only used to express subscription fan-out and/or
// claim/lock orchestration.
type TemplateNodeDef struct {
	Type        string `yaml:"type" json:"type"`
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
	// Executor names the executor for this node; empty means no executor.
	Executor string `yaml:"executor,omitempty" json:"executor,omitempty"`

	// Tags is operator-facing metadata: free-form strings used for
	// filtering at the dashboard / events surface. Tag values admit
	// `{{params.<key>}}` substitution at materialization time
	// (instance creation); no other substitution kinds are available
	// at that phase. Tags do not gate dispatch, cascade, or
	// validation. Per spec
	// .ok-planner/specs/2026-05-19-multi-instance-template-ergonomics-design.md
	// Item 4.
	//
	// @concept: node
	Tags []string `yaml:"tags,omitempty" json:"tags,omitempty"`

	Stores     []NodeStoreRef             `yaml:"stores,omitempty" json:"stores,omitempty"`
	Locks      []NodeLockRef              `yaml:"locks,omitempty" json:"locks,omitempty"`
	Attributes *NodeAttributesDef         `yaml:"attributes,omitempty" json:"attributes,omitempty"`
	ErrorTypes map[string]ErrorTypePolicy `yaml:"error_types,omitempty" json:"error_types,omitempty"`

	// Subscribes declares the node's reactive surface. Each entry names an
	// upstream node (or instance: true for cross-cutting) plus a signal
	// type-path (`type:`) and optional CEL `when:` predicate over the
	// signal payload. Plus implicit subscriptions inferred by the template
	// validator from substitution refs in Attributes (see
	// graph/node/subscription_edges.go). Per spec
	// .ok-planner/specs/2026-05-14-subscription-cascade-and-quality-of-life-design.md
	// Piece 1 and the 2026-05-23 signal-taxonomy reshape.
	//
	//	@concept: node-subscription
	Subscribes []SubscriptionEntry `yaml:"subscribes,omitempty" json:"subscribes,omitempty"`

	// @deliberate: no lifecycle-handler slots
	// (on_acquire_unavailable / on_executor_complete /
	// on_executor_errored). The three uses are expressed as:
	//   - Acquisition failure → error_types[acquire/unavailable].
	//   - Complete cascade-gating → subscriber-side CEL `when:`
	//     predicate (cascade-fire is purely subscriber-driven).
	//   - Error pass/override → error_types[<class>].policy with
	//     {action: pass}.

	// MaxParkDuration caps how long a parked node may stay parked before
	// the SweepParkedNodes watchdog forces it to fail with
	// error_class=park_timeout. Empty string means "use deployment
	// default" (rimsky has no global hard cap; absence = unbounded).
	// Format: any time.ParseDuration string ("24h", "30m", etc.).
	// Per the 2026-05-08 platform-extensions plan F2.
	MaxParkDuration string `yaml:"max_park_duration,omitempty" json:"max_park_duration,omitempty"`

	// MaxRetriesWithoutProgress caps the number of consecutive retry
	// dispatches that produce no settling_signal_type change before the
	// runner forces an Errored verdict with
	// error_class=retry_loop_no_progress.
	// Pointer for tri-state semantics: nil = use deployment default
	// (default 100); 0 = disable cap entirely (infinite retries
	// permitted); N>0 = use N. Per plan F3.
	MaxRetriesWithoutProgress *int `yaml:"max_retries_without_progress,omitempty" json:"max_retries_without_progress,omitempty"`

	// Delegate names a sub-graph (a GraphSpec.Name other than "main")
	// the node delegates to. Mutually exclusive with Executor: the
	// canonicalizer rejects nodes that set both, and absorbs the
	// referenced sub-graph's entry node's executor into this node at
	// canonicalization (per spec §Sub-graphs / Identity and absorption).
	Delegate string `yaml:"delegate,omitempty" json:"delegate,omitempty"`

	// Holds declares the node co-holds upstream claims. The outer key
	// is the local alias; the value names the upstream node whose
	// claim is being co-held. The supervisor INSERTs rows into
	// table:rimsky_claim_holders at dispatch time and binds the
	// upstream's address into the leaf's ExecuteRequest per-claim
	// slot using the local alias. Per spec §Claim co-holdership.
	Holds map[string]HoldsBinding `yaml:"holds,omitempty" json:"holds,omitempty"`

	// FanOut, when set, declares the node fans out across sub-scopes
	// of one of its `stores:` aliases. The supervisor calls
	// ClaimProducer.SplitScope inside the acquisition tx and dispatches
	// one leaf run per sub-scope. Per spec §Fan-out template DSL.
	FanOut *FanOutSpec `yaml:"fan_out,omitempty" json:"fan_out,omitempty"`

	// IsSubgraphEntryAbsorbed is set by the canonicalizer when this
	// node is a sub-graph caller (has a non-empty Delegate). Its
	// `rimsky_nodes` row carries the absorbed entry node's executor +
	// any sub-graph-internal claims/holds/attributes declared on the
	// entry, merged with what the calling node declared externally.
	// At runtime the supervisor consults this marker on the success
	// branch of `applyTerminalComplete` to route through the sub-graph
	// internal-cascade fire instead of the standard single-run
	// resolution. Persisted as JSON so the canonicalizer's emission
	// survives spec hashing.
	//
	// Per spec
	// .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
	// §Sub-graphs / Identity and absorption.
	IsSubgraphEntryAbsorbed bool `yaml:"is_subgraph_entry_absorbed,omitempty" json:"is_subgraph_entry_absorbed,omitempty"`

	// IsSubgraphExit is set by the canonicalizer when this node is the
	// declared `exit:` of a non-main graph. Mirrors
	// `IsSubgraphEntryAbsorbed`'s role on the calling-node side. At
	// runtime the supervisor's terminal handler consults this marker
	// on the success branch of `applyTerminalComplete` to drive the
	// exit-writeback carry-rule (the exit's writeback bytes are
	// persisted onto the parent run's attribute row, and the exit's
	// own attribute row stays empty). Persisted as JSON so the
	// canonicalizer's emission survives spec hashing; this lets the
	// runtime route on a static marker rather than a per-terminal
	// template lookup that could transiently fail.
	//
	// Per spec
	// .ok-planner/specs/2026-05-20-attribute-pull-resolution-design.md
	// §"Subgraph carry-rule".
	IsSubgraphExit bool `yaml:"is_subgraph_exit,omitempty" json:"is_subgraph_exit,omitempty"`
}

// @deliberate: frame + target constants for SubscriptionEntry. The lifecycle-handler
// resolve vocabulary (`pass | retry | error | by_changed |
// always_propagate | never_propagate`) retired with the handler types
// 2026-05-23; ErrorPolicy's 4-value action vocabulary (`pass | give_up |
// retry | discard_claims_then_retry`) is the replacement and lives on
// `concept:error-policy`.
const (
	FrameIn   = "in"
	FrameNext = "next"

	SelfTarget = "self"
)

// NodeStoreRef declares this node's claim against a registered store.
// Selector is opaque text post-substitution; the store parses and
// decides what it means (scope access vs. configured pick policy).
// Intent is "r" (read) or "rw" (read-write). Alias is the per-claim
// name within the node, used in {{claim.<alias>.<...>}} substitution
// paths and in downstream `holds:` references; defaults to Name (the
// producer name) when not set.
//
// Lifetime is "subgraph" (default) or "durable" (the claim survives
// past holding-subgraph completion — the asset pattern). When
// "durable" the canonicalizer requires the producer advertise the
// DataProcessing mix-in protocol.
//
// Data is the opaque-to-rimsky producer-targeted bytes (the `data:`
// block on the claim in YAML). Per @blessed-invariant 20 rimsky
// forwards verbatim to the producer; never inspects.
//
// @concept: claim
type NodeStoreRef struct {
	Name     string `yaml:"name" json:"name"`
	Selector string `yaml:"selector" json:"selector"`
	// Intent is the access mode requested: "r" or "rw".
	Intent string `yaml:"intent" json:"intent"`
	Alias  string `yaml:"alias,omitempty" json:"alias,omitempty"`
	// Lifetime is the claim lifetime: "subgraph" (default) or "durable".
	Lifetime string          `yaml:"lifetime,omitempty" json:"lifetime,omitempty"`
	Data     json.RawMessage `yaml:"data,omitempty" json:"data,omitempty"`
}

// AliasOf returns the claim alias for this store ref — defaults to
// the store name when not explicitly set. Used by the supervisor
// when constructing the substitution context's Claim map.
func (s NodeStoreRef) AliasOf() string {
	if s.Alias != "" {
		return s.Alias
	}
	return s.Name
}

// NodeLockRef declares a named lock the node must hold for the
// duration of its run. Limit lives in operator config (`named_locks:`
// block per spec §6.1), so the template only references the lock by
// name.
type NodeLockRef struct {
	Name string `yaml:"name" json:"name"`
}

// NodeAttributesDef declares the per-run typed attributes contract
// for the node. Schema is a JSON Schema fragment whose
// `properties[*].source` directives are substituted at dispatch
// (claim payload, deps, params).
type NodeAttributesDef struct {
	Schema map[string]any `yaml:"schema,omitempty" json:"schema,omitempty"`
}
