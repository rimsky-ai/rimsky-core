package externalsql

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/fallguy/rimsky/core/qualityrule"
	"github.com/fallguy/rimsky/core/resource"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/storage"
)

// Factory constructs external-sql Resources bound to a named pgx pool.
//
// Connections is a name → pool map supplied at supervisor startup (§10.2
// SQLConnections). StorageRegistry is the richer storage.ResourceRegistry
// surface used to record version manifests alongside the SQL writes.
type Factory struct {
	// Connections is the named-pool map from supervisor config. Keys here
	// correspond to the `connection_ref` string in the template config.
	Connections map[string]*pgxpool.Pool

	// StorageRegistry is the broader storage.ResourceRegistry surface;
	// external-sql calls CommitVersion / NoOpCommit / RestoreVersion directly
	// on it. Not the narrow resource.Registry — we need the richer surface.
	StorageRegistry storage.ResourceRegistry
}

// ConfigSchema implements resource.Factory.
func (f Factory) ConfigSchema() []byte { return configSchema }

// Create implements resource.Factory. cfg must carry the instance-specific
// allocation keys "_resource_id", "_path", and "_owner_node_id"; the instance
// factory populates these before invoking Create. The caller-provided
// resource.Registry (reg) is not needed by external-sql at runtime — all
// registry calls go through the richer storage.ResourceRegistry already held
// by the Factory — so it is accepted and ignored here.
//
// Create probes that the target table exists by issuing a cheap
// `SELECT 1 FROM schema.table LIMIT 0` so configuration errors surface at
// provisioning time rather than at first commit.
func (f Factory) Create(cfg resource.Config, rules []qualityrule.Spec, reg resource.Registry) (resource.Resource, error) {
	_ = reg // narrow registry unused; richer StorageRegistry is sufficient

	connRef := asString(cfg["connection_ref"])
	if connRef == "" {
		return nil, fmt.Errorf("externalsql: connection_ref required")
	}
	pool, ok := f.Connections[connRef]
	if !ok || pool == nil {
		return nil, fmt.Errorf("externalsql: connection_ref %q not configured", connRef)
	}
	if f.StorageRegistry == nil {
		return nil, fmt.Errorf("externalsql: Factory.StorageRegistry is nil")
	}

	rid := asString(cfg["_resource_id"])
	if rid == "" {
		return nil, fmt.Errorf("externalsql: cfg[_resource_id] required (set by instance factory)")
	}
	resourceID, err := uuid.Parse(rid)
	if err != nil {
		return nil, fmt.Errorf("externalsql: bad _resource_id: %w", err)
	}
	path, okPath := extractPath(cfg["_path"])
	if !okPath {
		return nil, fmt.Errorf("externalsql: cfg[_path] required (set by instance factory)")
	}
	ownerStr := asString(cfg["_owner_node_id"])
	if ownerStr == "" {
		return nil, fmt.Errorf("externalsql: cfg[_owner_node_id] required (set by instance factory)")
	}
	ownerID, err := uuid.Parse(ownerStr)
	if err != nil {
		return nil, fmt.Errorf("externalsql: bad _owner_node_id: %w", err)
	}

	inst := loadConfig(cfg)
	if inst.Schema == "" || inst.Table == "" {
		return nil, fmt.Errorf("externalsql: schema and table required")
	}
	if len(inst.PrimaryKey) == 0 {
		return nil, fmt.Errorf("externalsql: primary_key required (non-empty)")
	}
	if err := validateIdentifiers(inst); err != nil {
		return nil, err
	}

	r := &sqlResource{
		resourceID:  resourceID,
		path:        path,
		ownerNodeID: ownerID,
		pool:        pool,
		rules:       rules,
		storage:     f.StorageRegistry,
		cfg:         inst,
	}
	if err := r.probeExists(context.Background()); err != nil {
		return nil, fmt.Errorf("externalsql: probe %s.%s: %w", inst.Schema, inst.Table, err)
	}
	return r, nil
}

// extractPath accepts either []string or []any (JSON-decoded list of strings).
// Duplicated from inlinejsonb.extractPath.
// @source: core/resource/inlinejsonb/factory.go:extractPath
func extractPath(v any) ([]string, bool) {
	switch x := v.(type) {
	case []string:
		return append([]string(nil), x...), true
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

// validateIdentifiers rejects any schema/table/column identifier containing
// a literal double-quote, which would break the `q()` quoting helper.
func validateIdentifiers(c instanceConfig) error {
	names := []string{c.Schema, c.Table, c.StagingTable, c.PreviousTable}
	names = append(names, c.PrimaryKey...)
	for _, n := range names {
		if n == "" {
			continue
		}
		for i := 0; i < len(n); i++ {
			if n[i] == '"' {
				return fmt.Errorf("externalsql: identifier %q must not contain double quotes", n)
			}
		}
	}
	return nil
}

// Ensure Factory satisfies resource.Factory.
var _ resource.Factory = Factory{}

// Ensure shared.UUID stays reachable through this file even if unused elsewhere.
var _ shared.UUID = uuid.UUID{}
