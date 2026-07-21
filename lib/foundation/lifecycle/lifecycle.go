// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package lifecycle

import (
	"github.com/rimsky-ai/rimsky-core/lib/foundation/internal/namedreg"
	protolifecycle "github.com/rimsky-ai/rimsky-core/lib/protocols/lifecycle"
)

// @concept: lifecycle-subscriber
type OnTemplateRegisteredRequest = protolifecycle.OnTemplateRegisteredRequest

type OnTemplateDeployedRequest = protolifecycle.OnTemplateDeployedRequest

type OnTemplateUndeployedRequest = protolifecycle.OnTemplateUndeployedRequest

type OnTemplateDeregisteredRequest = protolifecycle.OnTemplateDeregisteredRequest

type OnInstanceCreatedRequest = protolifecycle.OnInstanceCreatedRequest

type OnInstanceTerminatedRequest = protolifecycle.OnInstanceTerminatedRequest

type OnRunScopeTerminalRequest = protolifecycle.OnRunScopeTerminalRequest

type Subscriber = protolifecycle.LifecycleSubscriber

type Registry struct {
	reg namedreg.Registry[Subscriber]
}

func NewRegistry() *Registry {
	return &Registry{reg: namedreg.New[Subscriber]()}
}

func (r *Registry) Add(name string, s Subscriber) {
	r.reg.Add(name, s)
}

func (r *Registry) Get(name string) (Subscriber, bool) {
	return r.reg.Get(name)
}

func (r *Registry) Subscribers() map[string]Subscriber {
	return r.reg.CopyMap()
}

func (r *Registry) Names() []string {
	return r.reg.Names()
}

func (r *Registry) Close() {
	r.reg.CloseAll()
}
