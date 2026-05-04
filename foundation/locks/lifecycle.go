// LifecycleSubscriber interface — the rimsky-side contract for binaries
// that hook into Rimsky's control-plane lifecycle events. Per spec
// docs/specs/2026-05-04-service-protocol-contract.md §3 (extracted from
// the bundled-into-Store pattern under the layer-crystallization plan,
// Phase 4).
//
// Implementer pattern: return nil from any method the binary doesn't
// react to. Binaries that don't react to any event simply don't
// implement the interface.
//
// Idempotency: control-api tracks per-peer idempotency in
// rimsky_lifecycle_idempotency; subscribers can assume each (peer,
// event) pair fires exactly once on the rimsky side. Subscribers SHOULD
// also be idempotent against duplicate calls in case of network retries.

package locks

import "context"

// LifecycleSubscriber is the universal interface every lifecycle
// subscriber implementation satisfies.
type LifecycleSubscriber interface {
	// Name returns the operator-configured peer name (matches the
	// peer's name in rimsky.yml under claim_producers: or executors:).
	Name() string

	OnTemplateRegistered(ctx context.Context, templateID string) error
	OnTemplateDeployed(ctx context.Context, templateID string) error
	OnTemplateUndeployed(ctx context.Context, templateID string) error
	OnTemplateDeregistered(ctx context.Context, templateID string) error
	OnInstanceCreated(ctx context.Context, templateID, instanceID string) error
	OnInstanceTerminated(ctx context.Context, templateID, instanceID string) error
}

// LifecycleRegistry holds the per-process name→LifecycleSubscriber map.
// Populated externally by each rimsky cmd binary's startup wiring;
// consumed by control-api's lifecycle fan-out. Subscribers are dialed
// from peers (claim_producers or executors) whose protocols list
// contains "lifecycle_subscriber".
type LifecycleRegistry struct {
	subs map[string]LifecycleSubscriber
}

// NewLifecycleRegistry returns an empty LifecycleRegistry. Callers Add
// after dialing each subscribed peer.
func NewLifecycleRegistry() *LifecycleRegistry {
	return &LifecycleRegistry{subs: make(map[string]LifecycleSubscriber)}
}

// Add registers a LifecycleSubscriber under name. Re-adding the same
// name overwrites the previous entry.
func (r *LifecycleRegistry) Add(name string, s LifecycleSubscriber) {
	r.subs[name] = s
}

// Get returns the LifecycleSubscriber registered under name and a
// boolean indicating presence.
func (r *LifecycleRegistry) Get(name string) (LifecycleSubscriber, bool) {
	s, ok := r.subs[name]
	return s, ok
}

// Subscribers returns a snapshot of every registered subscriber keyed by
// name. The returned map is a fresh copy.
func (r *LifecycleRegistry) Subscribers() map[string]LifecycleSubscriber {
	out := make(map[string]LifecycleSubscriber, len(r.subs))
	for name, s := range r.subs {
		out[name] = s
	}
	return out
}

// Names returns the set of registered subscriber names.
func (r *LifecycleRegistry) Names() []string {
	out := make([]string, 0, len(r.subs))
	for name := range r.subs {
		out = append(out, name)
	}
	return out
}

// Close walks every registered subscriber and calls Close() on those
// that implement the closer interface (defined in registry.go).
func (r *LifecycleRegistry) Close() {
	for _, s := range r.subs {
		if c, ok := s.(closer); ok {
			c.Close()
		}
	}
}
