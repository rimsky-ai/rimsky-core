// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package locks

import (
	"github.com/rimsky-ai/rimsky-core/lib/protocols/lifecycle"
)

// OnTemplateRegisteredRequest fires when a template is first registered
// (its content-hashed spec is persisted but not yet deployed under any
// movable tag).
type OnTemplateRegisteredRequest = lifecycle.OnTemplateRegisteredRequest

// OnTemplateDeployedRequest fires when one or more tags are pointed at a
// template hash. Tags is the set of tags newly attached.
type OnTemplateDeployedRequest = lifecycle.OnTemplateDeployedRequest

// OnTemplateUndeployedRequest fires when the last tag is removed from a
// template hash (the template is no longer reachable by tag, but its
// hashed spec persists).
type OnTemplateUndeployedRequest = lifecycle.OnTemplateUndeployedRequest

// OnTemplateDeregisteredRequest fires when a template hash is fully
// deleted (no tags, no instances).
type OnTemplateDeregisteredRequest = lifecycle.OnTemplateDeregisteredRequest

// OnInstanceCreatedRequest fires when a new instance is created from a
// template hash.
type OnInstanceCreatedRequest = lifecycle.OnInstanceCreatedRequest

// OnInstanceTerminatedRequest fires when an instance reaches the
// terminated state (rimsky_instances.terminated_at is set).
type OnInstanceTerminatedRequest = lifecycle.OnInstanceTerminatedRequest

// OnRunScopeTerminalRequest fires when a run-scope reaches terminal state
// (fired from control-api for main scopes; the supervisor for sub-graph
// and fanout-partition scopes).
type OnRunScopeTerminalRequest = lifecycle.OnRunScopeTerminalRequest

// LifecycleSubscriber is the universal interface every lifecycle
// subscriber implementation satisfies.
type LifecycleSubscriber = lifecycle.LifecycleSubscriber

// LifecycleRegistry holds the per-process name→LifecycleSubscriber map.
// Populated externally by each rimsky cmd binary's startup wiring;
// consumed by control-api's lifecycle fan-out. Subscribers are dialed
// from peers (claim_producers or executors) whose protocols list
// contains "lifecycle_subscriber".
//
// LifecycleRegistry is rimsky-internal; not aliased into the protocols
// package because the registry is an in-process collection, not a wire
// concept.
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
