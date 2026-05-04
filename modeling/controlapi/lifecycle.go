// Lifecycle event fan-out helper. Per the layer-crystallization design
// (2026-05-04, Phase 4) lifecycle events fire synchronously from
// control-api state-transition handlers to every LifecycleSubscriber
// peer declared in rimsky.yml under either claim_producers: or
// executors:. Progress-preserving retry is tracked in
// rimsky_lifecycle_idempotency (renamed from rimsky_store_lifecycle in
// Task 31).
package controlapi

import (
	"context"
	"fmt"
	"sort"

	"github.com/fallguy/rimsky/foundation/locks"
	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/modeling/node"
)

// LifecycleEvent enumerates the six lifecycle events from spec §4.1.
type LifecycleEvent int

const (
	EventTemplateRegistered LifecycleEvent = iota
	EventTemplateDeployed
	EventTemplateUndeployed
	EventTemplateDeregistered
	EventInstanceCreated
	EventInstanceTerminated
)

// String returns the constant name so log messages and error strings
// formatting LifecycleEvent values via %v / %s are human-readable
// rather than printing integer ordinals.
func (e LifecycleEvent) String() string {
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
	}
	return fmt.Sprintf("LifecycleEvent(%d)", int(e))
}

// FanOutTemplateEvent fires `event` to every LifecycleSubscriber peer
// declared in rimsky.yml. Filters by the set of subscribers referenced
// by the template's nodes (a peer that doesn't appear in any node's
// stores or executor field is not notified). Idempotent against
// rimsky_lifecycle_idempotency: peers already at the target state are
// skipped, peers that successfully process the event have their
// bookkeeping row upserted, peers that fail surface the error and abort
// the iteration.
//
// Returns the deduped peer-name list, the per-peer error map (only
// populated when at least one peer failed), and a top-level error
// when the operation should be considered a failure.
func FanOutTemplateEvent(
	ctx context.Context,
	deps AppDeps,
	event LifecycleEvent,
	templateHash string,
	spec node.TemplateSpec,
) ([]string, map[string]error, error) {
	switch event {
	case EventTemplateRegistered, EventTemplateDeployed, EventTemplateUndeployed, EventTemplateDeregistered:
	default:
		return nil, nil, fmt.Errorf("FanOutTemplateEvent: %v is not a template-scope event", event)
	}
	peerNames := lifecyclePeersForSpec(deps, spec)
	target := targetStateFor(event)
	deletesRow := event == EventTemplateDeregistered
	scopeKind := persistence.LifecycleIdempotencyScopeTemplate

	perPeerErr := map[string]error{}
	for _, name := range peerNames {
		row, err := deps.Persist.LifecycleIdempotency().Get(ctx, name, scopeKind, templateHash, nil)
		if err != nil {
			perPeerErr[name] = err
			return peerNames, perPeerErr, fmt.Errorf("FanOutTemplateEvent: lifecycle row lookup for %q: %w", name, err)
		}
		if !deletesRow && row != nil && row.State == target {
			continue
		}
		if deletesRow && row == nil {
			continue
		}
		if deps.LifecycleSubs == nil {
			perPeerErr[name] = fmt.Errorf("lifecycle subscriber registry not initialized")
			return peerNames, perPeerErr, fmt.Errorf("FanOutTemplateEvent: lifecycle subscriber registry not initialized")
		}
		s, ok := deps.LifecycleSubs.Get(name)
		if !ok {
			// Peer is referenced by the template but does not subscribe
			// to lifecycle events; skip silently.
			continue
		}
		if err := dispatchTemplateEvent(ctx, s, event, templateHash); err != nil {
			perPeerErr[name] = err
			return peerNames, perPeerErr, fmt.Errorf("FanOutTemplateEvent: peer %q: %w", name, err)
		}
		if deletesRow {
			if err := deps.Persist.LifecycleIdempotency().Delete(ctx, name, scopeKind, templateHash, nil); err != nil {
				perPeerErr[name] = err
				return peerNames, perPeerErr, fmt.Errorf("FanOutTemplateEvent: delete lifecycle row %q: %w", name, err)
			}
			continue
		}
		if err := deps.Persist.LifecycleIdempotency().Upsert(ctx, persistence.LifecycleIdempotencyRow{
			StoreRegistrationName: name,
			ScopeKind:             scopeKind,
			ScopeID:               templateHash,
			State:                 target,
		}, nil); err != nil {
			perPeerErr[name] = err
			return peerNames, perPeerErr, fmt.Errorf("FanOutTemplateEvent: upsert lifecycle row %q: %w", name, err)
		}
	}
	return peerNames, nil, nil
}

// FanOutInstanceEvent is the instance-scope analogue of
// FanOutTemplateEvent.
func FanOutInstanceEvent(
	ctx context.Context,
	deps AppDeps,
	event LifecycleEvent,
	templateHash, instanceID string,
	spec node.TemplateSpec,
) ([]string, map[string]error, error) {
	switch event {
	case EventInstanceCreated, EventInstanceTerminated:
	default:
		return nil, nil, fmt.Errorf("FanOutInstanceEvent: %v is not an instance-scope event", event)
	}
	peerNames := lifecyclePeersForSpec(deps, spec)
	deletesRow := event == EventInstanceTerminated
	scopeKind := persistence.LifecycleIdempotencyScopeInstance
	target := targetStateFor(event)

	perPeerErr := map[string]error{}
	for _, name := range peerNames {
		row, err := deps.Persist.LifecycleIdempotency().Get(ctx, name, scopeKind, instanceID, nil)
		if err != nil {
			perPeerErr[name] = err
			return peerNames, perPeerErr, fmt.Errorf("FanOutInstanceEvent: lifecycle row lookup for %q: %w", name, err)
		}
		if !deletesRow && row != nil && row.State == target {
			continue
		}
		if deletesRow && row == nil {
			continue
		}
		if deps.LifecycleSubs == nil {
			perPeerErr[name] = fmt.Errorf("lifecycle subscriber registry not initialized")
			return peerNames, perPeerErr, fmt.Errorf("FanOutInstanceEvent: lifecycle subscriber registry not initialized")
		}
		s, ok := deps.LifecycleSubs.Get(name)
		if !ok {
			continue
		}
		if err := dispatchInstanceEvent(ctx, s, event, templateHash, instanceID); err != nil {
			perPeerErr[name] = err
			return peerNames, perPeerErr, fmt.Errorf("FanOutInstanceEvent: peer %q: %w", name, err)
		}
		if deletesRow {
			if err := deps.Persist.LifecycleIdempotency().Delete(ctx, name, scopeKind, instanceID, nil); err != nil {
				perPeerErr[name] = err
				return peerNames, perPeerErr, fmt.Errorf("FanOutInstanceEvent: delete lifecycle row %q: %w", name, err)
			}
			continue
		}
		if err := deps.Persist.LifecycleIdempotency().Upsert(ctx, persistence.LifecycleIdempotencyRow{
			StoreRegistrationName: name,
			ScopeKind:             scopeKind,
			ScopeID:               instanceID,
			State:                 target,
		}, nil); err != nil {
			perPeerErr[name] = err
			return peerNames, perPeerErr, fmt.Errorf("FanOutInstanceEvent: upsert lifecycle row %q: %w", name, err)
		}
	}
	return peerNames, nil, nil
}

func dispatchTemplateEvent(ctx context.Context, s locks.LifecycleSubscriber, event LifecycleEvent, templateID string) error {
	switch event {
	case EventTemplateRegistered:
		return s.OnTemplateRegistered(ctx, templateID)
	case EventTemplateDeployed:
		return s.OnTemplateDeployed(ctx, templateID)
	case EventTemplateUndeployed:
		return s.OnTemplateUndeployed(ctx, templateID)
	case EventTemplateDeregistered:
		return s.OnTemplateDeregistered(ctx, templateID)
	}
	return fmt.Errorf("dispatchTemplateEvent: %v is not template-scope", event)
}

func dispatchInstanceEvent(ctx context.Context, s locks.LifecycleSubscriber, event LifecycleEvent, templateID, instanceID string) error {
	switch event {
	case EventInstanceCreated:
		return s.OnInstanceCreated(ctx, templateID, instanceID)
	case EventInstanceTerminated:
		return s.OnInstanceTerminated(ctx, templateID, instanceID)
	}
	return fmt.Errorf("dispatchInstanceEvent: %v is not instance-scope", event)
}

// lifecyclePeersForSpec returns the deduped, lex-sorted set of peer
// names referenced by the template (currently: the union of the
// store-aliases on each node). A peer that's referenced but does not
// appear in deps.LifecycleSubs is fanned-out-skipped at dispatch time
// (no error, no bookkeeping).
func lifecyclePeersForSpec(_ AppDeps, spec node.TemplateSpec) []string {
	return storesReferencedBySpec(spec)
}

func targetStateFor(event LifecycleEvent) persistence.LifecycleIdempotencyState {
	switch event {
	case EventTemplateRegistered:
		return persistence.LifecycleIdempotencyStateRegistered
	case EventTemplateDeployed:
		return persistence.LifecycleIdempotencyStateDeployed
	case EventTemplateUndeployed:
		return persistence.LifecycleIdempotencyStateUndeployed
	case EventInstanceCreated:
		return persistence.LifecycleIdempotencyStateCreated
	}
	return ""
}

// storesReferencedBySpec returns the deduped store-name set referenced
// by the template's nodes' Stores entries, sorted lexicographically per
// spec §5.6 for deterministic call order.
func storesReferencedBySpec(spec node.TemplateSpec) []string {
	seen := map[string]struct{}{}
	for _, n := range spec.Nodes {
		for _, s := range n.Stores {
			if s.Name == "" {
				continue
			}
			seen[s.Name] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
