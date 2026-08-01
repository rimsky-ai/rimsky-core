// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package claimproducer

import (
	"fmt"
	"sort"
	"sync"

	protocol "github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/clientiface"
)

// @concept: claim-producer
// @decision: parallel-inproc-claim-producer-registry
type Registration struct {
	Handler        protocol.ClaimProducer
	Capabilities   protocol.Capabilities
	Validation     clientiface.ValidationClient
	DataProcessing clientiface.DataProcessingClient
}

type InProcessRegistry struct {
	mu      sync.RWMutex
	entries map[string]Registration
}

func NewInProcessRegistry() *InProcessRegistry {
	return &InProcessRegistry{entries: map[string]Registration{}}
}

func (r *InProcessRegistry) Register(name string, reg Registration) error {
	if err := validateRegistration(name, reg); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.entries[name]; exists {
		return fmt.Errorf("InProcessRegistry: duplicate registration for %q", name)
	}
	r.entries[name] = reg
	return nil
}

func validateRegistration(name string, reg Registration) error {
	if name == "" {
		return fmt.Errorf("InProcessRegistry: registration name is required")
	}
	if reg.Handler == nil {
		return fmt.Errorf("InProcessRegistry: registration %q: Handler is required", name)
	}
	if len(reg.Capabilities.WriteSemanticsAllowed) == 0 {
		return fmt.Errorf("InProcessRegistry: registration %q: capabilities write_semantics_allowed is empty", name)
	}
	for _, ws := range reg.Capabilities.WriteSemanticsAllowed {
		if _, ok := protocol.ParseWriteSemantics(ws.String()); !ok {
			return fmt.Errorf("InProcessRegistry: registration %q: unknown write_semantics value %q", name, ws)
		}
	}
	advertisesValidation := reg.Capabilities.AdvertisesProtocol(protocol.ProtocolValidation)
	if advertisesValidation != (reg.Validation != nil) {
		return fmt.Errorf("InProcessRegistry: registration %q: validation mix-in requires both the %q protocol in capabilities and a Validation client (advertised=%t, client provided=%t)",
			name, protocol.ProtocolValidation, advertisesValidation, reg.Validation != nil)
	}
	advertisesDataProcessing := reg.Capabilities.AdvertisesProtocol(protocol.ProtocolDataProcessing)
	if advertisesDataProcessing != (reg.DataProcessing != nil) {
		return fmt.Errorf("InProcessRegistry: registration %q: data-processing mix-in requires both the %q protocol in capabilities and a DataProcessing client (advertised=%t, client provided=%t)",
			name, protocol.ProtocolDataProcessing, advertisesDataProcessing, reg.DataProcessing != nil)
	}
	return nil
}

func (r *InProcessRegistry) Lookup(name string) (Registration, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	reg, ok := r.entries[name]
	return reg, ok
}

func (r *InProcessRegistry) RegisteredNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.entries))
	for name := range r.entries {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func (r *InProcessRegistry) Client(name string) (*Client, bool) {
	reg, ok := r.Lookup(name)
	if !ok {
		return nil, false
	}
	return &Client{name: name, caps: reg.Capabilities, handler: reg.Handler}, true
}

func (r *InProcessRegistry) Validations() clientiface.ValidationRegistry {
	return validationView{r: r}
}

func (r *InProcessRegistry) DataProcessors() clientiface.DataProcessingRegistry {
	return dataProcessingView{r: r}
}

type validationView struct{ r *InProcessRegistry }

func (v validationView) Get(name string) (clientiface.ValidationClient, bool) {
	reg, ok := v.r.Lookup(name)
	if !ok || reg.Validation == nil {
		return nil, false
	}
	return reg.Validation, true
}

func (v validationView) All() []clientiface.ValidationClient {
	v.r.mu.RLock()
	defer v.r.mu.RUnlock()
	out := make([]clientiface.ValidationClient, 0, len(v.r.entries))
	for _, reg := range v.r.entries {
		if reg.Validation != nil {
			out = append(out, reg.Validation)
		}
	}
	return out
}

type dataProcessingView struct{ r *InProcessRegistry }

func (v dataProcessingView) Get(name string) (clientiface.DataProcessingClient, bool) {
	reg, ok := v.r.Lookup(name)
	if !ok || reg.DataProcessing == nil {
		return nil, false
	}
	return reg.DataProcessing, true
}
