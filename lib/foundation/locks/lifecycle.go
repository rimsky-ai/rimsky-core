// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package locks

import (
	"github.com/rimsky-ai/rimsky-core/lib/protocols/lifecycle"
)

type OnTemplateRegisteredRequest = lifecycle.OnTemplateRegisteredRequest

type OnTemplateDeployedRequest = lifecycle.OnTemplateDeployedRequest

type OnTemplateUndeployedRequest = lifecycle.OnTemplateUndeployedRequest

type OnTemplateDeregisteredRequest = lifecycle.OnTemplateDeregisteredRequest

type OnInstanceCreatedRequest = lifecycle.OnInstanceCreatedRequest

type OnInstanceTerminatedRequest = lifecycle.OnInstanceTerminatedRequest

type OnRunScopeTerminalRequest = lifecycle.OnRunScopeTerminalRequest

type LifecycleSubscriber = lifecycle.LifecycleSubscriber

type LifecycleRegistry struct {
	subs map[string]LifecycleSubscriber
}

func NewLifecycleRegistry() *LifecycleRegistry {
	return &LifecycleRegistry{subs: make(map[string]LifecycleSubscriber)}
}

func (r *LifecycleRegistry) Add(name string, s LifecycleSubscriber) {
	r.subs[name] = s
}

func (r *LifecycleRegistry) Get(name string) (LifecycleSubscriber, bool) {
	s, ok := r.subs[name]
	return s, ok
}

func (r *LifecycleRegistry) Subscribers() map[string]LifecycleSubscriber {
	out := make(map[string]LifecycleSubscriber, len(r.subs))
	for name, s := range r.subs {
		out[name] = s
	}
	return out
}

func (r *LifecycleRegistry) Names() []string {
	out := make([]string, 0, len(r.subs))
	for name := range r.subs {
		out = append(out, name)
	}
	return out
}

func (r *LifecycleRegistry) Close() {
	for _, s := range r.subs {
		if c, ok := s.(closer); ok {
			c.Close()
		}
	}
}
