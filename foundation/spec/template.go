// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Template DSL row-types — the persistable shape of a template. The
// graph-author's view of a node: stores it interacts with, named locks
// it holds, attributes it declares, and inheritance edges for held
// claims it consumes downstream.
//
// Graph algorithms that consume these types (the template validator,
// the holding-subgraph computation, inheritance resolution) live in
// graph/node — this package defines only the data.

package spec

// TemplateSpec is the top-level template structure, parsed from YAML
// or JSON.
type TemplateSpec struct {
	Name                string            `yaml:"name" json:"name"`
	Version             string            `yaml:"version" json:"version"`
	Description         string            `yaml:"description,omitempty" json:"description,omitempty"`
	FrameResolutionMode string            `yaml:"frame_resolution_mode" json:"frame_resolution_mode"`
	FrameTimeoutMs      int64             `yaml:"frame_timeout_ms,omitempty" json:"frame_timeout_ms,omitempty"`
	Nodes               []TemplateNodeDef `yaml:"nodes" json:"nodes"`
	ParamsSchema        map[string]any    `yaml:"params_schema,omitempty" json:"params_schema,omitempty"` // JSON Schema
	ParamsRedact        []string          `yaml:"params_redact,omitempty" json:"params_redact,omitempty"`
}

// Frame-resolution constants (per docs/history/2026-04-26-frame-resolution-design.md).
const (
	FrameResolutionCoalesce    = "coalesce"
	FrameResolutionSerialQueue = "serial_queue"
	FrameTimeoutDefaultMs      = int64(600000) // 10 minutes
	FrameTimeoutMinMs          = int64(60000)  // 60 seconds (hard floor)
)

// TemplateNodeDef is one node in a template. An empty Executor means
// the node is a pure-cascade or pure-infra node — it runs no executor
// handler and is only used to express dependency fan-out and/or
// claim/lock orchestration.
type TemplateNodeDef struct {
	Type         string                     `yaml:"type" json:"type"`
	Description  string                     `yaml:"description,omitempty" json:"description,omitempty"`
	Executor     string                     `yaml:"executor,omitempty" json:"executor,omitempty"` // optional; empty = no executor
	Userdata     map[string]any             `yaml:"userdata,omitempty" json:"userdata,omitempty"`
	Schedule     string                     `yaml:"schedule,omitempty" json:"schedule,omitempty"` // cron expr; optional
	Dependencies []string                   `yaml:"dependencies,omitempty" json:"dependencies,omitempty"`
	Stores       []NodeStoreRef             `yaml:"stores,omitempty" json:"stores,omitempty"`
	Locks        []NodeLockRef              `yaml:"locks,omitempty" json:"locks,omitempty"`
	Attributes   *NodeAttributesDef         `yaml:"attributes,omitempty" json:"attributes,omitempty"`
	QualityRules []QualityRuleSpec          `yaml:"quality_rules,omitempty" json:"quality_rules,omitempty"`
	Inherits     []InheritEntry             `yaml:"inherits,omitempty" json:"inherits,omitempty"`
	ErrorTypes   map[string]ErrorTypePolicy `yaml:"error_types,omitempty" json:"error_types,omitempty"`

	// Lifecycle handlers — declarative slots for the three supervisor
	// terminal-event paths. Per the reactive-loops + lifecycle-handlers
	// spec at .ok-planner/specs/2026-05-05-reactive-loops-and-lifecycle-handlers-design.md §3
	// and the 2026-05-12 nomenclature-resolution collapse of the
	// `on_executor_blocked` slot. All three slots are optional; absent
	// slots use today's hardcoded supervisor defaults (silent retry on
	// Unavailable, by_changed on Complete, route through error_types on
	// Errored). All non-Complete error variants flow through
	// OnExecutorErrored; operator-declared `error_types` discriminates by
	// `error_class`.
	OnAcquireUnavailable *OnAcquireUnavailableHandler `yaml:"on_acquire_unavailable,omitempty" json:"on_acquire_unavailable,omitempty"`
	OnExecutorComplete   *OnExecutorCompleteHandler   `yaml:"on_executor_complete,omitempty"   json:"on_executor_complete,omitempty"`
	OnExecutorErrored    *OnExecutorTerminalHandler   `yaml:"on_executor_errored,omitempty"    json:"on_executor_errored,omitempty"`

	// OnEvent declares per-named-event handlers for non-terminal events
	// the executor emits via the protocol-layer NamedEvent wire type.
	// Keys are event names that MUST appear in the executor's
	// Capabilities.declared_events; the graph-layer template validator
	// (graph/template/userdata_validation.go) cross-checks.
	// Per the 2026-05-08 platform-extensions plan F1.
	OnEvent map[string]EventHandler `yaml:"on_event,omitempty" json:"on_event,omitempty"`

	// MaxParkDuration caps how long a parked node may stay parked before
	// the SweepParkedNodes watchdog forces it to fail with
	// error_class=park_timeout. Empty string means "use deployment
	// default" (rimsky has no global hard cap; absence = unbounded).
	// Format: any time.ParseDuration string ("24h", "30m", etc.).
	// Per the 2026-05-08 platform-extensions plan F2.
	MaxParkDuration string `yaml:"max_park_duration,omitempty" json:"max_park_duration,omitempty"`

	// MaxRetriesWithoutProgress caps the number of consecutive retry
	// dispatches that produce no last_outcome change before the runner
	// forces an Errored verdict with error_class=retry_loop_no_progress.
	// Pointer for tri-state semantics: nil = use deployment default
	// (default 100); 0 = disable cap entirely (infinite retries
	// permitted); N>0 = use N. Per plan F3.
	MaxRetriesWithoutProgress *int `yaml:"max_retries_without_progress,omitempty" json:"max_retries_without_progress,omitempty"`
}

// EventHandler declares the supervisor's behavior when an executor emits
// a NamedEvent matching one of the keys in TemplateNodeDef.OnEvent.
//
// Resolve is one of "pass" | "retry" | "error" | "" (default = do
// nothing beyond firing Invalidate). ErrorClass is required when
// Resolve == "error".
//
// Invalidate fires unconditionally when the handler runs (orthogonal to
// Resolve), exactly like the lifecycle-handler invalidate slot. Targets
// are node types or "self"; Frame is "in" | "next" (default "next").
//
// Per plan F1.
type EventHandler struct {
	Resolve    string             `yaml:"resolve,omitempty" json:"resolve,omitempty"`
	ErrorClass string             `yaml:"error_class,omitempty" json:"error_class,omitempty"`
	Invalidate *HandlerInvalidate `yaml:"invalidate,omitempty" json:"invalidate,omitempty"`
}

// OnAcquireUnavailableHandler declares the supervisor's behavior when
// any required claim's Open returns Unavailable. See spec §3.
type OnAcquireUnavailableHandler struct {
	Resolve    string             `yaml:"resolve" json:"resolve"`                             // pass | retry | error
	ErrorClass string             `yaml:"error_class,omitempty" json:"error_class,omitempty"` // required when resolve=error
	Invalidate *HandlerInvalidate `yaml:"invalidate,omitempty" json:"invalidate,omitempty"`
}

// OnExecutorCompleteHandler declares the supervisor's behavior on a
// Complete terminal. See spec §3.
type OnExecutorCompleteHandler struct {
	Resolve    string             `yaml:"resolve" json:"resolve"` // by_changed | always_propagate | never_propagate
	Invalidate *HandlerInvalidate `yaml:"invalidate,omitempty" json:"invalidate,omitempty"`
}

// OnExecutorTerminalHandler declares behavior on a Blocked or Errored
// terminal. See spec §3.
type OnExecutorTerminalHandler struct {
	Resolve    string             `yaml:"resolve" json:"resolve"`                             // error | pass
	ErrorClass string             `yaml:"error_class,omitempty" json:"error_class,omitempty"` // required when resolve=error
	Invalidate *HandlerInvalidate `yaml:"invalidate,omitempty" json:"invalidate,omitempty"`
}

// HandlerInvalidate is the optional invalidate-emit slot on every
// lifecycle handler. Fires unconditionally when the handler runs;
// orthogonal to resolve. See spec §3.5.
type HandlerInvalidate struct {
	Targets []string `yaml:"targets" json:"targets"`
	Frame   string   `yaml:"frame,omitempty" json:"frame,omitempty"` // in | next; default next
}

// Resolve constants per handler. The validator at template-deploy
// rejects out-of-vocabulary combinations.
const (
	ResolvePass            = "pass"
	ResolveRetry           = "retry"
	ResolveError           = "error"
	ResolveByChanged       = "by_changed"
	ResolveAlwaysPropagate = "always_propagate"
	ResolveNeverPropagate  = "never_propagate"

	FrameIn   = "in"
	FrameNext = "next"

	SelfTarget = "self"
)

// NodeStoreRef declares this node's claim against a registered store.
// Selector is opaque text post-substitution; the store parses and
// decides what it means (scope access vs. configured pick policy).
// Intent is "r" (read) or "rw" (read-write). Alias is the per-claim
// name within the node, used in {{claim.<alias>.<...>}} substitution
// paths and in inheritance references; defaults to Name (the
// producer name) when not set.
type NodeStoreRef struct {
	Name     string `yaml:"name" json:"name"`
	Selector string `yaml:"selector" json:"selector"`
	Intent   string `yaml:"intent" json:"intent"` // "r" | "rw"
	Alias    string `yaml:"alias,omitempty" json:"alias,omitempty"`
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

// InheritEntry declares that this node inherits a held claim from an
// upstream acquirer. Per spec §14: inheritance is direct only (does
// not propagate transitively through dep chains); each downstream
// node that needs the live claim declares it explicitly. Each
// inheritance edge extends the claim's lifetime over the inheriting
// node's run.
//
// Claim is the per-claim alias declared on the upstream acquirer's
// stores: entry. Validation at template deploy resolves the alias to
// a specific acquirer reachable via deps.
type InheritEntry struct {
	Claim string `yaml:"claim" json:"claim"`
}
