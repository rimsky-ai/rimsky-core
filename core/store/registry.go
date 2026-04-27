package store

import "fmt"

// Factory builds a concrete Store from a per-store config map. Implementations
// register themselves with a Registry keyed by Kind(); the Registry inspects
// the YAML config's "kind" field on each store entry to pick the factory.
type Factory interface {
	// Kind returns the store kind this factory builds (e.g. "filesystem",
	// "postgres").
	Kind() string

	// MaxWriteSemantics returns the substrate's ceiling for the
	// write_semantics config field. The Registry rejects an operator
	// config whose write_semantics exceeds this rank. (Spec §8.2.)
	MaxWriteSemantics() WriteSemantics

	// Build constructs a Store from the operator-supplied config. The name
	// is the operator-chosen YAML key (e.g. "stores.foo" → name="foo"); cfg
	// is the remaining YAML map after the "kind" key has been consumed.
	Build(name string, cfg map[string]any) (Store, error)
}

// StoresConfig is the top-level "stores" map from process YAML. Keys are
// operator-chosen store names; each value carries a "kind" entry plus
// kind-specific fields (which may include a top-level "write_semantics"
// override and a substrate-specific "pick_policies" block).
type StoresConfig struct {
	Stores map[string]map[string]any
}

// NamedLockConfig is the operator-side configuration for one named lock
// (spec §15.2). Limit must be ≥ 1; limit=1 is a mutex, limit=N>1 is a
// counting semaphore. Templates reference named locks by name only;
// the limit lives here.
type NamedLockConfig struct {
	Limit int `yaml:"limit"`
}

// NamedLocksConfig is the top-level "named_locks" map from process YAML.
// Keys are operator-chosen lock names; each value carries the limit.
// Spec §15.3: stores and named locks live in one operator config bundle
// (loaded from RIMSKY_STORES_CONFIG).
type NamedLocksConfig struct {
	Locks map[string]NamedLockConfig
}

// Get returns the configured NamedLockConfig for `name`, plus a
// presence bool. Used by the queue eligibility predicate (§13.2) and
// by template-deploy validation (T23).
func (c NamedLocksConfig) Get(name string) (NamedLockConfig, bool) {
	if c.Locks == nil {
		return NamedLockConfig{}, false
	}
	cfg, ok := c.Locks[name]
	return cfg, ok
}

// Validate checks that every NamedLockConfig.Limit ≥ 1. Returns an
// error listing the offending names. Run at process startup; reject
// invalid configs loudly.
func (c NamedLocksConfig) Validate() error {
	for name, cfg := range c.Locks {
		if cfg.Limit < 1 {
			return fmt.Errorf("named_locks[%q]: limit must be >= 1, got %d", name, cfg.Limit)
		}
	}
	return nil
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
// Per spec §8.2, BuildAll also enforces the write_semantics ceiling: when
// an entry carries a top-level "write_semantics" string, it is checked
// against the factory's MaxWriteSemantics() and rejected on upgrade
// attempts (e.g., declaring staged_async on a substrate that only supports
// direct).
//
// Returns an error on the first failure (missing kind, unknown kind,
// ceiling violation, factory build error). On error, the Registry's
// internal store map is left empty so a partial-build does not leak into
// GetStore.
func (r *Registry) BuildAll(cfg StoresConfig) (map[string]Store, error) {
	built := make(map[string]Store, len(cfg.Stores))
	closeBuilt := func() {
		for _, s := range built {
			if c, ok := s.(closer); ok {
				c.Close()
			}
		}
	}
	for name, raw := range cfg.Stores {
		kindRaw, ok := raw["kind"]
		if !ok {
			closeBuilt()
			return nil, fmt.Errorf("store %q: missing 'kind' field", name)
		}
		kind, ok := kindRaw.(string)
		if !ok {
			closeBuilt()
			return nil, fmt.Errorf("store %q: 'kind' must be string, got %T", name, kindRaw)
		}
		factory, ok := r.factories[kind]
		if !ok {
			closeBuilt()
			return nil, fmt.Errorf("store %q: no factory registered for kind %q", name, kind)
		}
		// write_semantics ceiling check (spec §8.2). Empty / missing →
		// the factory's default applies inside Build.
		if wsRaw, ok := raw["write_semantics"]; ok {
			wsStr, ok := wsRaw.(string)
			if !ok {
				closeBuilt()
				return nil, fmt.Errorf("store %q: 'write_semantics' must be string, got %T", name, wsRaw)
			}
			ws := WriteSemantics(wsStr)
			if writeSemanticsRank(ws) < 0 {
				closeBuilt()
				return nil, fmt.Errorf("store %q: 'write_semantics' = %q is not one of direct|staged_blocking|staged_async", name, wsStr)
			}
			max := factory.MaxWriteSemantics()
			if writeSemanticsRank(ws) > writeSemanticsRank(max) {
				closeBuilt()
				return nil, fmt.Errorf("store %q: configured write_semantics %q exceeds substrate max %q", name, wsStr, string(max))
			}
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
			closeBuilt()
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
// the scheduler's §12.12 visibility-timeout sweep to walk every store
// that exposes pick policies. The returned map is a fresh copy; callers
// may mutate it without affecting the Registry.
func (r *Registry) Stores() map[string]Store {
	out := make(map[string]Store, len(r.stores))
	for name, s := range r.stores {
		out[name] = s
	}
	return out
}

// closer is the optional interface a store implements when it owns
// disposable resources (e.g. a per-store *pgxpool.Pool the factory
// opened). Registry.Close calls it on each built store; stores that
// don't implement it are no-ops.
type closer interface {
	Close()
}

// Close walks every built store and calls Close() on those that
// implement the closer interface. Intended for cmd binaries to call at
// shutdown so per-store pools (opened via the postgres store's
// `connection:` config) are released. Idempotent: stores that don't
// own resources skip; pgxpool.Pool.Close is itself idempotent.
func (r *Registry) Close() {
	for _, s := range r.stores {
		if c, ok := s.(closer); ok {
			c.Close()
		}
	}
}

// writeSemanticsRank returns 0/1/2 for direct/staged_blocking/staged_async,
// or -1 for an unrecognized value. The supervisor's config ceiling check
// (§8.2) compares ranks to allow downgrades but reject upgrades.
func writeSemanticsRank(ws WriteSemantics) int {
	switch ws {
	case WriteSemanticsDirect:
		return 0
	case WriteSemanticsStagedBlocking:
		return 1
	case WriteSemanticsStagedAsync:
		return 2
	}
	return -1
}
