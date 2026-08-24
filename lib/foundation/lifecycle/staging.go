// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @decision: lifecycle-fanout-after-commit
// @decision: lifecycle-subscriber-at-least-once-delivery
// @concept: lifecycle-subscriber

package lifecycle

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

type Event int

const (
	EventTemplateRegistered Event = iota
	EventTemplateDeployed
	EventTemplateUndeployed
	EventTemplateDeregistered
	EventInstanceCreated
	EventInstanceTerminated
	EventRunScopeTerminal
)

func (e Event) String() string {
	switch e {
	case EventTemplateRegistered:
		return "EventTemplateRegistered"
	case EventTemplateDeployed:
		return "EventTemplateDeployed"
	case EventTemplateUndeployed:
		return "EventTemplateUndeployed"
	case EventTemplateDeregistered:
		return "EventTemplateDeregistered"
	case EventInstanceCreated:
		return "EventInstanceCreated"
	case EventInstanceTerminated:
		return "EventInstanceTerminated"
	case EventRunScopeTerminal:
		return "EventRunScopeTerminal"
	}
	return fmt.Sprintf("LifecycleEvent(%d)", int(e))
}

func ParseEvent(name string) (Event, bool) {
	for _, e := range []Event{
		EventTemplateRegistered,
		EventTemplateDeployed,
		EventTemplateUndeployed,
		EventTemplateDeregistered,
		EventInstanceCreated,
		EventInstanceTerminated,
		EventRunScopeTerminal,
	} {
		if e.String() == name {
			return e, true
		}
	}
	return 0, false
}

type TemplatePayload struct {
	Spec json.RawMessage
	Tags []string
}

type InstancePayload struct {
	InstanceKey           string
	Params                json.RawMessage
	TerminatedAtUnixMs    int64
	ServiceBindings       json.RawMessage
	OwnerAPIKeyID         *shared.UUID
	TargetRoutingIdentity string
}

type StagedPayload struct {
	TemplateHash   string           `json:"template_hash,omitempty"`
	InstanceID     string           `json:"instance_id,omitempty"`
	RunScopeID     string           `json:"run_scope_id,omitempty"`
	TerminalReason string           `json:"terminal_reason,omitempty"`
	Template       *TemplatePayload `json:"template,omitempty"`
	Instance       *InstancePayload `json:"instance,omitempty"`
}

// @decision: lifecycle-fanout-after-commit
func DecodeStagedPayload(body []byte) (StagedPayload, error) {
	var out StagedPayload
	if err := json.Unmarshal(body, &out); err != nil {
		return StagedPayload{}, fmt.Errorf("decode staged lifecycle payload: %w", err)
	}
	return out, nil
}

func (p StagedPayload) TemplatePayloadOrZero() TemplatePayload {
	if p.Template == nil {
		return TemplatePayload{}
	}
	return *p.Template
}

func (p StagedPayload) InstancePayloadOrZero() InstancePayload {
	if p.Instance == nil {
		return InstancePayload{}
	}
	return *p.Instance
}

// @decision: lifecycle-fanout-after-commit
func StageDeliveries(
	ctx context.Context,
	outbox persistence.LifecycleOutboxTable,
	event Event,
	scopeKind persistence.LifecycleScopeKind,
	scopeID string,
	services []string,
	payload StagedPayload,
	tx persistence.Tx,
) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal staged lifecycle payload for %s/%s: %w", scopeKind, scopeID, err)
	}
	for _, name := range services {
		if err := outbox.Stage(ctx, persistence.LifecycleOutboxRow{
			ClaimProducerName: name,
			ScopeKind:         scopeKind,
			ScopeID:           scopeID,
			Event:             event.String(),
			Payload:           body,
		}, tx); err != nil {
			return fmt.Errorf("stage %s for service %q: %w", event.String(), name, err)
		}
	}
	return nil
}

// @decision: lifecycle-fanout-after-commit
func StageTemplateEvent(
	ctx context.Context,
	outbox persistence.LifecycleOutboxTable,
	event Event,
	templateHash string,
	sp spec.TemplateSpec,
	payload TemplatePayload,
	tx persistence.Tx,
) error {
	switch event {
	case EventTemplateRegistered, EventTemplateDeployed, EventTemplateUndeployed, EventTemplateDeregistered:
	default:
		return fmt.Errorf("StageTemplateEvent: %v is not a template-scope event", event)
	}
	return StageDeliveries(ctx, outbox, event,
		persistence.LifecycleScopeTemplate, templateHash,
		ServicesReferencedBySpec(sp),
		StagedPayload{TemplateHash: templateHash, Template: &payload},
		tx)
}

// @decision: lifecycle-fanout-after-commit
func StageInstanceEvent(
	ctx context.Context,
	outbox persistence.LifecycleOutboxTable,
	event Event,
	templateHash, instanceID string,
	sp spec.TemplateSpec,
	lateBindServiceProxies map[string]string,
	payload InstancePayload,
	tx persistence.Tx,
) error {
	switch event {
	case EventInstanceCreated, EventInstanceTerminated:
	default:
		return fmt.Errorf("StageInstanceEvent: %v is not an instance-scope event", event)
	}
	return StageDeliveries(ctx, outbox, event,
		persistence.LifecycleScopeInstance, instanceID,
		SubscribingServices(sp, lateBindServiceProxies),
		StagedPayload{TemplateHash: templateHash, InstanceID: instanceID, Instance: &payload},
		tx)
}

// @concept: run-scope
// @decision: lifecycle-fanout-after-commit
func StageRunScopeTerminal(
	ctx context.Context,
	tables persistence.Tables,
	instanceID, runScopeID shared.UUID,
	terminalReason string,
	lateBindServiceProxies map[string]string,
	tx persistence.Tx,
) error {
	inst, err := tables.Instances().Get(ctx, instanceID, tx)
	if err != nil {
		return fmt.Errorf("StageRunScopeTerminal: get instance %s: %w", instanceID, err)
	}
	if inst == nil {
		return fmt.Errorf("StageRunScopeTerminal: instance %s: %w", instanceID, persistence.ErrNotFound)
	}
	tpl, err := tables.Templates().GetByHash(ctx, inst.TemplateHash, tx)
	if err != nil {
		return fmt.Errorf("StageRunScopeTerminal: get template %s: %w", inst.TemplateHash, err)
	}
	if tpl == nil {
		return fmt.Errorf("StageRunScopeTerminal: template %s of instance %s: %w",
			inst.TemplateHash, instanceID, persistence.ErrNotFound)
	}
	return StageDeliveries(ctx, tables.LifecycleOutbox(), EventRunScopeTerminal,
		persistence.LifecycleScopeRunScope, runScopeID.String(),
		SubscribingServices(tpl.Spec, lateBindServiceProxies),
		StagedPayload{
			RunScopeID:     runScopeID.String(),
			InstanceID:     instanceID.String(),
			TerminalReason: terminalReason,
		},
		tx)
}

// @concept: lifecycle-subscriber
func DispatchEvent(ctx context.Context, s Subscriber, event Event, payload StagedPayload) error {
	switch event {
	case EventTemplateRegistered:
		return s.OnTemplateRegistered(ctx, OnTemplateRegisteredRequest{
			TemplateHash: payload.TemplateHash,
			Spec:         payload.TemplatePayloadOrZero().Spec,
		})
	case EventTemplateDeployed:
		return s.OnTemplateDeployed(ctx, OnTemplateDeployedRequest{
			TemplateHash: payload.TemplateHash,
			Tags:         payload.TemplatePayloadOrZero().Tags,
		})
	case EventTemplateUndeployed:
		return s.OnTemplateUndeployed(ctx, OnTemplateUndeployedRequest{
			TemplateHash: payload.TemplateHash,
		})
	case EventTemplateDeregistered:
		return s.OnTemplateDeregistered(ctx, OnTemplateDeregisteredRequest{
			TemplateHash: payload.TemplateHash,
		})
	case EventInstanceCreated:
		inst := payload.InstancePayloadOrZero()
		return s.OnInstanceCreated(ctx, OnInstanceCreatedRequest{
			InstanceID:            payload.InstanceID,
			TemplateHash:          payload.TemplateHash,
			InstanceKey:           inst.InstanceKey,
			Params:                inst.Params,
			ServiceBindings:       inst.ServiceBindings,
			OwnerAPIKeyID:         uuidString(inst.OwnerAPIKeyID),
			TargetRoutingIdentity: inst.TargetRoutingIdentity,
		})
	case EventInstanceTerminated:
		return s.OnInstanceTerminated(ctx, OnInstanceTerminatedRequest{
			InstanceID:         payload.InstanceID,
			TemplateHash:       payload.TemplateHash,
			TerminatedAtUnixMs: payload.InstancePayloadOrZero().TerminatedAtUnixMs,
		})
	case EventRunScopeTerminal:
		return s.OnRunScopeTerminal(ctx, OnRunScopeTerminalRequest{
			RunScopeID:     payload.RunScopeID,
			InstanceID:     payload.InstanceID,
			TerminalReason: payload.TerminalReason,
		})
	}
	return fmt.Errorf("DispatchEvent: unknown lifecycle event %v", event)
}

func uuidString(u *shared.UUID) string {
	if u == nil {
		return ""
	}
	return u.String()
}

func ServicesReferencedBySpec(sp spec.TemplateSpec) []string {
	seen := map[string]struct{}{}
	for _, n := range sp.Nodes {
		for _, s := range n.ClaimProducers {
			if s.Name == "" {
				continue
			}
			seen[s.Name] = struct{}{}
		}
		if n.Executor != "" {
			seen[n.Executor] = struct{}{}
		}
	}
	for _, p := range sp.Publishers {
		if p.Name == "" {
			continue
		}
		seen[p.Name] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// @concept: host-daemon-proxy
func SubscribingServices(sp spec.TemplateSpec, lateBindServiceProxies map[string]string) []string {
	services := ServicesReferencedBySpec(sp)
	if len(sp.LateBindServices) == 0 {
		return services
	}
	seen := make(map[string]struct{}, len(services))
	for _, s := range services {
		seen[s] = struct{}{}
	}
	proxied := make([]string, 0, len(lateBindServiceProxies))
	for svcName := range lateBindServiceProxies {
		proxied = append(proxied, svcName)
	}
	sort.Strings(proxied)
	for _, svcName := range proxied {
		proxyName := lateBindServiceProxies[svcName]
		if proxyName == "" {
			continue
		}
		if _, exists := seen[proxyName]; exists {
			continue
		}
		services = append(services, proxyName)
		seen[proxyName] = struct{}{}
	}
	return services
}
