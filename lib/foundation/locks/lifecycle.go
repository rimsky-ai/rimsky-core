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
	reg namedRegistry[LifecycleSubscriber]
}

func NewLifecycleRegistry() *LifecycleRegistry {
	return &LifecycleRegistry{reg: newNamedRegistry[LifecycleSubscriber]()}
}

func (r *LifecycleRegistry) Add(name string, s LifecycleSubscriber) {
	r.reg.add(name, s)
}

func (r *LifecycleRegistry) Get(name string) (LifecycleSubscriber, bool) {
	return r.reg.get(name)
}

func (r *LifecycleRegistry) Subscribers() map[string]LifecycleSubscriber {
	return r.reg.copyMap()
}

func (r *LifecycleRegistry) Names() []string {
	return r.reg.names()
}

func (r *LifecycleRegistry) Close() {
	r.reg.closeAll()
}
