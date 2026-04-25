// Package inlinejsonb implements resource.Factory and resource.Resource for
// the inline-JSONB access kind. Committed results are stored directly in the
// rimsky_resource_versions.data column; no external data store is involved.
//
// The implementation is database-agnostic at its interface: it talks to
// Postgres (or any other backing store) exclusively through
// storage.ResourceRegistry, which is injected into the Factory. Tests wire in
// an in-memory fake; production wires in the Postgres-backed impl.
package inlinejsonb

import (
	"fmt"

	"github.com/google/uuid"

	"github.com/fallguy/rimsky/core/qualityrule"
	"github.com/fallguy/rimsky/core/resource"
	"github.com/fallguy/rimsky/core/storage"
)

// configSchema describes the template-level config block accepted by this
// implementation. Template validation uses this schema to reject malformed
// access.config blocks early.
var configSchema = []byte(`{
  "type": "object",
  "properties": { "keep_versions": { "type": "integer", "minimum": 1 } },
  "additionalProperties": false
}`)

// Factory constructs inline-jsonb Resources. StorageRegistry is the richer
// storage surface the implementation needs (CommitVersion / NoOpCommit / etc.)
// rather than the narrow resource.Registry.
type Factory struct {
	// StorageRegistry is the broader storage.ResourceRegistry surface;
	// inline-jsonb calls CommitVersion / NoOpCommit / etc. directly on it.
	// Not the narrow resource.Registry — we need the richer surface.
	StorageRegistry storage.ResourceRegistry
}

// ConfigSchema implements resource.Factory.
func (f Factory) ConfigSchema() []byte { return configSchema }

// Create implements resource.Factory. cfg must carry the instance-specific
// allocation keys "_resource_id", "_path", and "_owner_node_id"; the instance
// factory populates these before invoking Create. The caller-provided
// resource.Registry (reg) is not needed by inline-jsonb at runtime — all
// registry calls go through the richer storage.ResourceRegistry already held
// by the Factory — so it is accepted and ignored here.
func (f Factory) Create(cfg resource.Config, rules []qualityrule.Spec, reg resource.Registry) (resource.Resource, error) {
	_ = reg // narrow registry unused; richer StorageRegistry is sufficient
	keep := 2
	if kv, ok := cfg["keep_versions"].(int); ok && kv > 0 {
		keep = kv
	} else if kvf, ok := cfg["keep_versions"].(float64); ok && kvf > 0 {
		keep = int(kvf)
	}
	rid, okRID := cfg["_resource_id"].(string)
	path, okPath := extractPath(cfg["_path"])
	owner, okOwn := cfg["_owner_node_id"].(string)
	if !okRID || !okPath || !okOwn {
		return nil, fmt.Errorf("inlinejsonb: Factory.Create requires cfg[_resource_id], cfg[_path], cfg[_owner_node_id]")
	}
	resourceID, err := uuid.Parse(rid)
	if err != nil {
		return nil, fmt.Errorf("inlinejsonb: bad _resource_id: %w", err)
	}
	ownerID, err := uuid.Parse(owner)
	if err != nil {
		return nil, fmt.Errorf("inlinejsonb: bad _owner_node_id: %w", err)
	}
	if f.StorageRegistry == nil {
		return nil, fmt.Errorf("inlinejsonb: Factory.StorageRegistry is nil")
	}
	return &inlineResource{
		resourceID:   resourceID,
		path:         path,
		ownerNodeID:  ownerID,
		keepVersions: keep,
		rules:        rules,
		storage:      f.StorageRegistry,
	}, nil
}

// extractPath accepts either []string or []any (JSON-decoded list of strings).
func extractPath(v any) ([]string, bool) {
	switch x := v.(type) {
	case []string:
		return x, true
	case []any:
		out := make([]string, 0, len(x))
		for _, item := range x {
			s, ok := item.(string)
			if !ok {
				return nil, false
			}
			out = append(out, s)
		}
		return out, true
	}
	return nil, false
}
