package node

import (
	"github.com/fallguy/rimsky/core/qualityrule"
	"github.com/fallguy/rimsky/core/store"
)

// TemplateSpec is the top-level template structure, parsed from YAML or JSON.
//
// Per the stores redesign (docs/specs/2026-04-25-stores-redesign-design.md
// §11), templates declare per-node store usage, named locks, typed
// attributes, and held-claim resolutions. Resources/concurrency-tags from
// the previous shape have been removed (§11.3).
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

// TemplateNodeDef is one node in a template. An empty Executor means the node
// is a pure-cascade or pure-infra node — it runs no executor handler and is
// only used to express dependency fan-out and/or claim/lock orchestration.
//
// Field names map to YAML keys via lowercase-snake-case reflection (the
// default decoder convention for this struct); sub-types below carry explicit
// `yaml:` tags only where the wire name diverges from the field name. Keep
// the two conventions consistent: if a field's YAML key needs to differ from
// its lowercased Go name, add an explicit tag here too.
type TemplateNodeDef struct {
	Type             string
	Description      string
	Executor         string // optional; empty = no executor (pure-cascade or pure-infra)
	Userdata         map[string]any
	Schedule         string // cron expr; optional
	Dependencies     []string
	Stores           []NodeStoreRef
	Locks            []NodeLockRef
	Attributes       NodeAttributesDef
	QualityRules     []qualityrule.Spec
	ClaimResolutions []ClaimResolutionRef
	ErrorTypes       map[string]ErrorTypePolicy
}

// NodeStoreRef declares this node's interaction with a registered store.
//
// `Claim` requests claim-and-forget (default) or claim-and-hold (when `Hold`
// is also true). `Write` and `Read` are region-pattern lists (substituted at
// dispatch). `OnCommit` and `OnGiveUp` set the per-node disposition policy
// for held claims, overriding store defaults; resumable controls whether the
// claim-hold survives node restarts.
type NodeStoreRef struct {
	Name      string   `yaml:"name"`
	Claim     bool     `yaml:"claim,omitempty"`
	Hold      bool     `yaml:"hold,omitempty"`
	Write     []string `yaml:"write,omitempty"`
	Read      []string `yaml:"read,omitempty"`
	OnCommit  string   `yaml:"on_commit,omitempty"`
	OnGiveUp  string   `yaml:"on_give_up,omitempty"`
	Resumable bool     `yaml:"resumable,omitempty"`
}

// NodeLockRef declares a named lock the node must hold for the duration of
// its run. Mode discriminates mutex vs. counting; Limit is required for
// counting locks and ignored for mutex.
type NodeLockRef struct {
	Name  string         `yaml:"name"`
	Mode  store.LockMode `yaml:"mode"`
	Limit int            `yaml:"limit,omitempty"`
}

// NodeAttributesDef declares the per-run typed attributes contract for the
// node. Schema is a JSON Schema fragment whose `properties[*].source`
// directives are substituted at dispatch (claim payload, deps, etc.).
type NodeAttributesDef struct {
	Schema map[string]any `yaml:"schema,omitempty"`
}

// ClaimResolutionRef declares how this node resolves a held claim originally
// taken by an upstream node. Source names the holding node; Store names the
// store on which the claim was taken; OnCommit / OnGiveUp override the
// store-default disposition policies for this resolution.
type ClaimResolutionRef struct {
	Source   string `yaml:"source"`
	Store    string `yaml:"store"`
	OnCommit string `yaml:"on_commit,omitempty"`
	OnGiveUp string `yaml:"on_give_up,omitempty"`
}

// RequiredStores returns the distinct store names referenced by node.Stores,
// preserving first-seen order. Used by enqueue logic to populate
// rimsky_dispatch.required_stores (spec §9.6, §14.2 pool-specialization
// predicate).
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
