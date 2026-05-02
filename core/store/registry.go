// Registry — the per-process map from operator-chosen store name to a
// Store implementation. Per spec docs/specs/2026-04-27-stores-redesign-
// v3-design.md §3.1 + §6.
//
// In v3 the Registry is a simple name→Store map populated externally:
// each rimsky process's cmd binary loads rimsky.yml, dials a remote
// gRPC client per entry (core/store/remote/), validates the
// Capabilities() handshake, and Add()s the result. There is no Factory
// interface, no per-kind dispatch, no BuildAll, no ceiling check —
// every store-service runs at exactly one write_semantics (per spec
// §4.8) and rimsky just verifies the operator's declared expectation
// matches.
//
// NamedLocksConfig and named-lock helpers stay here unchanged.

package store

import (
	"fmt"
	"log/slog"
)

// NamedLockConfig is the operator-side configuration for one named
// lock. Limit must be ≥ 1; limit=1 is a mutex, limit=N>1 is a counting
// semaphore. Templates reference named locks by name only; the limit
// lives here.
type NamedLockConfig struct {
	Limit int `yaml:"limit"`
}

// NamedLocksConfig is the top-level "named_locks" map from process
// YAML. Keys are operator-chosen lock names; each value carries the
// limit. Per spec §6.1 — stores and named locks live in one operator
// config bundle.
type NamedLocksConfig struct {
	Locks map[string]NamedLockConfig
}

// Get returns the configured NamedLockConfig for `name`, plus a
// presence bool. Used by the queue eligibility predicate and by
// template-deploy validation.
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

// Registry holds the per-process name→Store map. Populated externally
// by each rimsky cmd binary's startup wiring; consumed by the
// supervisor's acquisition flow, the scheduler's orphan reaper, and
// the control-api's template validator.
type Registry struct {
	stores map[string]Store
}

// NewRegistry returns an empty Registry. Callers Add(name, store)
// after dialing each remote store-service.
func NewRegistry() *Registry {
	return &Registry{stores: make(map[string]Store)}
}

// Add registers a Store under name. Re-adding the same name overwrites
// the previous entry; the caller is responsible for avoiding accidental
// shadowing.
//
// As a defensive sanity check (logged at startup): if the store reports
// a non-empty Name() that disagrees with the registration name, Add
// emits a slog.Warn but proceeds with the registration. The remote-
// client constructor always sets the store's internal name from the
// registration `name` argument, so a mismatch indicates a wiring bug —
// but tests exercising the Fake store may register under a name that
// differs from the constructor-supplied one, so we no longer panic.
func (r *Registry) Add(name string, s Store) {
	if s != nil {
		if got := s.Name(); got != "" && got != name {
			slog.Warn("store registry: registration name disagrees with store.Name()",
				"registration_name", name,
				"store_internal_name", got,
				"hint", "registration name and store-internal name should agree; check the wiring path that constructed this store")
		}
	}
	r.stores[name] = s
}

// Get returns the Store registered under name and a boolean indicating
// presence.
func (r *Registry) Get(name string) (Store, bool) {
	s, ok := r.stores[name]
	return s, ok
}

// Stores returns a snapshot of every registered store keyed by name.
// The returned map is a fresh copy; callers may mutate it without
// affecting the Registry.
func (r *Registry) Stores() map[string]Store {
	out := make(map[string]Store, len(r.stores))
	for name, s := range r.stores {
		out[name] = s
	}
	return out
}

// Names returns the set of registered store names. Used to derive the
// supervisor's accepted_stores list for the dispatch eligibility
// predicate.
func (r *Registry) Names() []string {
	out := make([]string, 0, len(r.stores))
	for name := range r.stores {
		out = append(out, name)
	}
	return out
}

// closer is the optional interface a store implements when it owns
// disposable resources (e.g. a gRPC client connection). Registry.Close
// calls it on each registered store; stores that don't implement it
// are no-ops.
type closer interface {
	Close()
}

// Close walks every registered store and calls Close() on those that
// implement the closer interface. Intended for cmd binaries to call at
// shutdown so per-store resources are released. Idempotent: stores
// without resources skip; well-behaved Close implementations handle
// repeat calls.
func (r *Registry) Close() {
	for _, s := range r.stores {
		if c, ok := s.(closer); ok {
			c.Close()
		}
	}
}
