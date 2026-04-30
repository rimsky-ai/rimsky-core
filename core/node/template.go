// Template DSL types (spec §18). The graph-author's view of a node:
// stores it interacts with, named locks it holds, attributes it
// declares, and inheritance edges for held claims it consumes
// downstream.
//
// History (informational):
//   - Stores-redesign-v2 dropped per-claim on_commit/on_give_up
//     overrides from claim entries and added a `claim_resolutions`
//     map on the acquiring node.
//   - The 2026-04-30 stores cleanup
//     (`docs/specs/2026-04-30-stores-protocol-cleanup-design.md`)
//     removes `claim_resolutions` entirely. Substrate disposition
//     (what Commit / Abandon mean for the substrate's own state) is
//     governed entirely by per-substrate config; rimsky carries only
//     the success/failure binary (success → Commit; failure →
//     Abandon).
//   - NodeStoreRef carries selector + intent + alias.
//   - TemplateNodeDef carries Inherits []InheritEntry for held-claim
//     consumers downstream of an acquirer.

package node

import "github.com/fallguy/rimsky/core/qualityrule"

// TemplateSpec is the top-level template structure, parsed from YAML
// or JSON.
type TemplateSpec struct {
	Name            string
	Version         string
	Description     string
	FrameResolution string `yaml:"frame_resolution" json:"frame_resolution"`
	FrameTimeoutMs  int64  `yaml:"frame_timeout_ms,omitempty" json:"frame_timeout_ms,omitempty"`
	Nodes           []TemplateNodeDef
	ParamsSchema    map[string]any // JSON Schema
	ParamsRedact    []string
}

// Frame-resolution constants (per docs/specs/2026-04-26-frame-resolution-design.md).
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
	Type         string
	Description  string
	Executor     string // optional; empty = no executor
	Userdata     map[string]any
	Schedule     string // cron expr; optional
	Dependencies []string
	Stores       []NodeStoreRef
	Locks        []NodeLockRef
	Attributes   NodeAttributesDef
	QualityRules []qualityrule.Spec
	Inherits     []InheritEntry `yaml:"inherits,omitempty"`
	ErrorTypes   map[string]ErrorTypePolicy
}

// NodeStoreRef declares this node's claim against a registered store.
// Selector is opaque text post-substitution; substrate parses and
// decides what it means (regional access vs. configured pick policy).
// Intent is "r" (read) or "rw" (read-write). Alias is the per-claim
// name within the node, used in {{claim.<alias>.<...>}} substitution
// paths and in inheritance references; defaults to StoreName when not
// set.
type NodeStoreRef struct {
	Name     string `yaml:"name"`
	Selector string `yaml:"selector"`
	Intent   string `yaml:"intent"` // "r" | "rw"
	Alias    string `yaml:"alias,omitempty"`
}

// NodeLockRef declares a named lock the node must hold for the
// duration of its run. Limit lives in operator config (`named_locks:`
// block per spec §6.1), so the template only references the lock by
// name.
type NodeLockRef struct {
	Name string `yaml:"name"`
}

// NodeAttributesDef declares the per-run typed attributes contract
// for the node. Schema is a JSON Schema fragment whose
// `properties[*].source` directives are substituted at dispatch
// (claim payload, deps, params).
type NodeAttributesDef struct {
	Schema map[string]any `yaml:"schema,omitempty"`
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
	Claim string `yaml:"claim"`
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

// RequiredStores returns the distinct store names referenced by
// node.Stores, preserving first-seen order. Used by enqueue logic to
// populate rimsky_dispatch.required_stores.
func RequiredStores(node TemplateNodeDef) []string {
	if len(node.Stores) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(node.Stores))
	out := make([]string, 0, len(node.Stores))
	for _, s := range node.Stores {
		if s.Name == "" {
			continue
		}
		if _, ok := seen[s.Name]; ok {
			continue
		}
		seen[s.Name] = struct{}{}
		out = append(out, s.Name)
	}
	return out
}
