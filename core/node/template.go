package node

import (
	"github.com/fallguy/rimsky/core/qualityrule"
	"github.com/fallguy/rimsky/core/shared"
)

// TemplateSpec is the top-level template structure, parsed from YAML or JSON.
//
// Port of rimsky/src/cell/template.ts:CellTemplateSpec adapted for spec §5.5:
// the TS `kind` discriminator is dropped (v1 Go splits it into Executor +
// Schedule + Userdata) and ResourceDef gains Implementation + Config so the
// template can point at a concrete resource implementation (inline-jsonb,
// external-sql, etc.) registered in core/resource.
type TemplateSpec struct {
	Name         string
	Version      string
	Description  string
	Nodes        []TemplateNodeDef
	ParamsSchema map[string]any // JSON Schema
	ParamsRedact []string
}

// TemplateNodeDef is one node in a template. An empty Executor means the node
// is a pure-cascade node — it holds no resources, runs no handler, and is
// only used to express dependency fan-out.
type TemplateNodeDef struct {
	Type            string
	Description     string
	Executor        string // optional; empty = pure-cascade
	Userdata        map[string]any
	Schedule        string // cron expr; optional
	Dependencies    []string
	ConcurrencyTags []string
	OwnsResources   []ResourceDef
	ReadsResources  []ReadResourceDef
	ErrorTypes      map[string]ErrorTypePolicy
}

// ResourceDef declares an owned resource. Implementation names a registered
// resource backend (e.g. "inline-jsonb", "external-sql"); Config is the
// opaque per-implementation payload.
type ResourceDef struct {
	Path           []string
	Implementation string
	Config         map[string]any
	Retention      *Retention
	QualityRules   []qualityrule.Spec
}

type Retention struct {
	KeepVersions int
}

type ReadResourceDef struct {
	Path []string
	Via  shared.AccessKind
}
