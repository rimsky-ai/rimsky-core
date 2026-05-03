// Lifecycle event fan-out helper. Per docs/specs/2026-05-01-control-
// plane-and-store-lifecycle-design.md §5: lifecycle events fire
// synchronously from control-api state-transition handlers to every
// store referenced by the affected template, with progress-preserving
// retry tracked in rimsky_store_lifecycle.
package controlapi

import (
	"context"
	"fmt"
	"sort"

	"github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/persistence"
	"github.com/fallguy/rimsky/core/store"
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

// FanOutTemplateEvent fires `event` to every distinct store referenced
// by the template's nodes. Idempotent against rimsky_store_lifecycle:
// stores already at the target state are skipped, stores that
// successfully process the event have their bookkeeping row upserted,
// stores that fail surface the error and abort the iteration. Per spec
// §5.4.
//
// Returns the deduped store name list, the per-store error map (only
// populated when at least one store failed), and a top-level error
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
	storeNames := storesReferencedBySpec(spec)
	target := targetStateFor(event)
	deletesRow := event == EventTemplateDeregistered
	scopeKind := persistence.StoreLifecycleScopeTemplate

	perStoreErr := map[string]error{}
	for _, name := range storeNames {
		row, err := deps.Persist.StoreLifecycle().Get(ctx, name, scopeKind, templateHash, nil)
		if err != nil {
			perStoreErr[name] = err
			return storeNames, perStoreErr, fmt.Errorf("FanOutTemplateEvent: lifecycle row lookup for %q: %w", name, err)
		}
		if !deletesRow && row != nil && row.State == target {
			continue
		}
		if deletesRow && row == nil {
			continue
		}
		if deps.Stores == nil {
			perStoreErr[name] = fmt.Errorf("store registry not initialized")
			return storeNames, perStoreErr, fmt.Errorf("FanOutTemplateEvent: store registry not initialized")
		}
		s, ok := deps.Stores.Get(name)
		if !ok {
			perStoreErr[name] = fmt.Errorf("store %q not registered", name)
			return storeNames, perStoreErr, fmt.Errorf("FanOutTemplateEvent: store %q not registered", name)
		}
		if err := dispatchTemplateEvent(ctx, s, event, templateHash); err != nil {
			perStoreErr[name] = err
			return storeNames, perStoreErr, fmt.Errorf("FanOutTemplateEvent: store %q: %w", name, err)
		}
		if deletesRow {
			if err := deps.Persist.StoreLifecycle().Delete(ctx, name, scopeKind, templateHash, nil); err != nil {
				perStoreErr[name] = err
				return storeNames, perStoreErr, fmt.Errorf("FanOutTemplateEvent: delete lifecycle row %q: %w", name, err)
			}
			continue
		}
		if err := deps.Persist.StoreLifecycle().Upsert(ctx, persistence.StoreLifecycleRow{
			StoreRegistrationName: name,
			ScopeKind:             scopeKind,
			ScopeID:               templateHash,
			State:                 target,
		}, nil); err != nil {
			perStoreErr[name] = err
			return storeNames, perStoreErr, fmt.Errorf("FanOutTemplateEvent: upsert lifecycle row %q: %w", name, err)
		}
	}
	return storeNames, nil, nil
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
	storeNames := storesReferencedBySpec(spec)
	deletesRow := event == EventInstanceTerminated
	scopeKind := persistence.StoreLifecycleScopeInstance
	target := targetStateFor(event)

	perStoreErr := map[string]error{}
	for _, name := range storeNames {
		row, err := deps.Persist.StoreLifecycle().Get(ctx, name, scopeKind, instanceID, nil)
		if err != nil {
			perStoreErr[name] = err
			return storeNames, perStoreErr, fmt.Errorf("FanOutInstanceEvent: lifecycle row lookup for %q: %w", name, err)
		}
		if !deletesRow && row != nil && row.State == target {
			continue
		}
		if deletesRow && row == nil {
			continue
		}
		if deps.Stores == nil {
			perStoreErr[name] = fmt.Errorf("store registry not initialized")
			return storeNames, perStoreErr, fmt.Errorf("FanOutInstanceEvent: store registry not initialized")
		}
		s, ok := deps.Stores.Get(name)
		if !ok {
			perStoreErr[name] = fmt.Errorf("store %q not registered", name)
			return storeNames, perStoreErr, fmt.Errorf("FanOutInstanceEvent: store %q not registered", name)
		}
		if err := dispatchInstanceEvent(ctx, s, event, templateHash, instanceID); err != nil {
			perStoreErr[name] = err
			return storeNames, perStoreErr, fmt.Errorf("FanOutInstanceEvent: store %q: %w", name, err)
		}
		if deletesRow {
			if err := deps.Persist.StoreLifecycle().Delete(ctx, name, scopeKind, instanceID, nil); err != nil {
				perStoreErr[name] = err
				return storeNames, perStoreErr, fmt.Errorf("FanOutInstanceEvent: delete lifecycle row %q: %w", name, err)
			}
			continue
		}
		if err := deps.Persist.StoreLifecycle().Upsert(ctx, persistence.StoreLifecycleRow{
			StoreRegistrationName: name,
			ScopeKind:             scopeKind,
			ScopeID:               instanceID,
			State:                 target,
		}, nil); err != nil {
			perStoreErr[name] = err
			return storeNames, perStoreErr, fmt.Errorf("FanOutInstanceEvent: upsert lifecycle row %q: %w", name, err)
		}
	}
	return storeNames, nil, nil
}

func dispatchTemplateEvent(ctx context.Context, s store.Store, event LifecycleEvent, templateID string) error {
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

func dispatchInstanceEvent(ctx context.Context, s store.Store, event LifecycleEvent, templateID, instanceID string) error {
	switch event {
	case EventInstanceCreated:
		return s.OnInstanceCreated(ctx, templateID, instanceID)
	case EventInstanceTerminated:
		return s.OnInstanceTerminated(ctx, templateID, instanceID)
	}
	return fmt.Errorf("dispatchInstanceEvent: %v is not instance-scope", event)
}

func targetStateFor(event LifecycleEvent) persistence.StoreLifecycleState {
	switch event {
	case EventTemplateRegistered:
		return persistence.StoreLifecycleStateRegistered
	case EventTemplateDeployed:
		return persistence.StoreLifecycleStateDeployed
	case EventTemplateUndeployed:
		return persistence.StoreLifecycleStateUndeployed
	case EventInstanceCreated:
		return persistence.StoreLifecycleStateCreated
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
