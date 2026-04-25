package resource

import "sync"

// FactoryRegistry is an explicit, non-global factory registry. Prefer this
// over the package-level RegisterFactory/GetFactory functions when multiple
// orchestrators run in the same process (e.g. parallel tests).
//
// Note: named FactoryRegistry (not Registry) because the Registry identifier
// is already taken by the storage-facing interface in interface.go. Callers
// typically pass a *FactoryRegistry as SupervisorConfig.ResourceFactories /
// ControlAPIConfig.ResourceFactories.
type FactoryRegistry struct {
	mu        sync.RWMutex
	factories map[string]Factory
}

// NewRegistry returns an empty *FactoryRegistry.
func NewRegistry() *FactoryRegistry {
	return &FactoryRegistry{factories: map[string]Factory{}}
}

// Register records a factory under the given implementation name. Overwrites
// any prior entry for the same name.
func (r *FactoryRegistry) Register(name string, f Factory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.factories[name] = f
}

// Get returns the factory registered under name, or (nil, false) if missing.
func (r *FactoryRegistry) Get(name string) (Factory, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	f, ok := r.factories[name]
	return f, ok
}

// ListNames returns all registered implementation names. Useful for
// diagnostic logs and operator inspection.
func (r *FactoryRegistry) ListNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.factories))
	for n := range r.factories {
		names = append(names, n)
	}
	return names
}

var defaultRegistry = NewRegistry()

// DefaultRegistry returns the process-global registry. Provided for consumers
// still using RegisterFactory/GetFactory; new code should construct its own
// registry via NewRegistry and pass it explicitly through SupervisorConfig /
// ControlAPIConfig.
func DefaultRegistry() *FactoryRegistry { return defaultRegistry }

// RegisterFactory registers a named resource implementation in the
// package-default registry.
//
// Deprecated: prefer FactoryRegistry.Register on an explicit *FactoryRegistry
// passed through SupervisorConfig. The package-default registry is
// process-global and not safe under parallel multi-orchestrator use.
func RegisterFactory(name string, f Factory) { defaultRegistry.Register(name, f) }

// GetFactory returns a registered factory by name from the package-default
// registry.
//
// Deprecated: prefer FactoryRegistry.Get on an explicit *FactoryRegistry.
func GetFactory(name string) (Factory, bool) { return defaultRegistry.Get(name) }

// ListFactoryNames returns all implementation names registered in the
// package-default registry.
//
// Deprecated: prefer FactoryRegistry.ListNames.
func ListFactoryNames() []string { return defaultRegistry.ListNames() }
