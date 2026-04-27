package store

import "fmt"

// Factory builds a concrete Store from a per-store config map. Implementations
// register themselves with a Registry keyed by Kind(); the Registry inspects
// the YAML config's "kind" field on each store entry to pick the factory.
type Factory interface {
	// Kind returns the store kind this factory builds (e.g. "filesystem",
	// "claim_store").
	Kind() string

	// Build constructs a Store from the operator-supplied config. The name
	// is the operator-chosen YAML key (e.g. "stores.foo" → name="foo"); cfg
	// is the remaining YAML map after the "kind" key has been consumed.
	Build(name string, cfg map[string]any) (Store, error)
}

// StoresConfig is the top-level "stores" map from process YAML. Keys are
// operator-chosen store names; each value carries a "kind" entry plus
// kind-specific fields.
type StoresConfig struct {
	Stores map[string]map[string]any
}

// Registry holds the per-process set of factories and built stores. control-
// api and each supervisor build their own Registry from process YAML at
// startup. Stores are not exchanged across processes; coordination is via
// postgres lock-holder rows.
type Registry struct {
	factories map[string]Factory
	stores    map[string]Store
}

// NewRegistry returns an empty Registry. Callers register factories via
// Register, then call BuildAll once with the parsed YAML.
func NewRegistry() *Registry {
	return &Registry{
		factories: make(map[string]Factory),
		stores:    make(map[string]Store),
	}
}

// Register adds a factory to the registry, keyed by f.Kind(). Re-registering
// the same kind overwrites the previous entry; the caller is responsible for
// avoiding accidental shadowing.
func (r *Registry) Register(f Factory) {
	r.factories[f.Kind()] = f
}

// BuildAll constructs a Store for every entry in cfg.Stores. Each entry must
// carry a "kind" string field whose value matches a registered factory; the
// remaining keys are passed to Factory.Build as cfg. The returned map is
// keyed by store name and is also retained on the Registry for GetStore
// lookups.
//
// Returns an error on the first failure (missing kind, unknown kind, factory
// build error). On error, the Registry's internal store map is left empty so
// a partial-build does not leak into GetStore.
func (r *Registry) BuildAll(cfg StoresConfig) (map[string]Store, error) {
	built := make(map[string]Store, len(cfg.Stores))
	for name, raw := range cfg.Stores {
		kindRaw, ok := raw["kind"]
		if !ok {
			return nil, fmt.Errorf("store %q: missing 'kind' field", name)
		}
		kind, ok := kindRaw.(string)
		if !ok {
			return nil, fmt.Errorf("store %q: 'kind' must be string, got %T", name, kindRaw)
		}
		factory, ok := r.factories[kind]
		if !ok {
			return nil, fmt.Errorf("store %q: no factory registered for kind %q", name, kind)
		}
		// Pass a copy without "kind" so factories see only their own fields.
		fcfg := make(map[string]any, len(raw))
		for k, v := range raw {
			if k == "kind" {
				continue
			}
			fcfg[k] = v
		}
		s, err := factory.Build(name, fcfg)
		if err != nil {
			return nil, fmt.Errorf("store %q (kind %q): build failed: %w", name, kind, err)
		}
		built[name] = s
	}
	r.stores = built
	return built, nil
}

// GetStore returns the Store registered under name and a boolean indicating
// presence. Mirrors the executor-name resolution shape used elsewhere in the
// codebase.
func (r *Registry) GetStore(name string) (Store, bool) {
	s, ok := r.stores[name]
	return s, ok
}

// Stores returns a snapshot of every built store keyed by name. Used by
// the scheduler's §13.5 step-4 visibility-timeout sweep to walk
// `claim-store-postgres` instances. The returned map is a fresh copy;
// callers may mutate it without affecting the Registry.
func (r *Registry) Stores() map[string]Store {
	out := make(map[string]Store, len(r.stores))
	for name, s := range r.stores {
		out[name] = s
	}
	return out
}
