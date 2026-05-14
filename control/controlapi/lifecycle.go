// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Lifecycle event fan-out helper. Per the layer-crystallization design
// (2026-05-04, Phase 4) lifecycle events fire synchronously from
// control-api state-transition handlers to every LifecycleSubscriber
// peer declared in rimsky.yml under either claim_producers: or
// executors:. Progress-preserving retry is tracked in
// rimsky_lifecycle_idempotencies (renamed from rimsky_store_lifecycle in
// Task 31).
package controlapi

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/fallguy/rimsky/foundation/locks"
	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/graph/node"
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

// TemplatePayload carries the per-event payload data the fan-out helper
// passes through to subscribers. Fields are populated by the caller per
// event type; unused fields stay at zero-value.
type TemplatePayload struct {
	// Spec is the canonical JCS-canonicalized template spec bytes.
	// Populated for OnTemplateRegistered.
	Spec json.RawMessage
	// Tags is the set of tags newly attached to the template hash.
	// Populated for OnTemplateDeployed (the bag of tags now pointing
	// at this hash on this state transition).
	Tags []string
}

// InstancePayload carries the per-event payload data for instance-scope
// events.
type InstancePayload struct {
	// InstanceKey is rimsky_instances.instance_key (may be empty).
	// Populated for OnInstanceCreated.
	InstanceKey string
	// Params is rimsky_instances.params (canonical JSON bytes).
	// Populated for OnInstanceCreated.
	Params json.RawMessage
	// TerminatedAtUnixMs is rimsky_instances.terminated_at expressed
	// as Unix milliseconds. Populated for OnInstanceTerminated.
	TerminatedAtUnixMs int64
}

// FanOutTemplateEvent fires `event` to every LifecycleSubscriber peer
// declared in rimsky.yml. Filters by the set of subscribers referenced
// by the template's nodes (a peer that doesn't appear in any node's
// stores or executor field is not notified). Idempotent against
// rimsky_lifecycle_idempotencies: peers already at the target state are
// skipped, peers that successfully process the event have their
// bookkeeping row upserted, peers that fail surface the error and abort
// the iteration.
//
// Returns the deduped peer-name list, the per-peer error map (only
// populated when at least one peer failed), and a top-level error
// when the operation should be considered a failure.
//
// `tx` is the open transaction the caller holds, or nil. When non-nil,
// every persistence call inside the fan-out reuses that connection.
// When nil, each persistence call inside the fan-out is wrapped in its
// own short Persist.Transaction (the per-peer RPC must run between the
// idempotency Get and the Delete/Upsert; serializing it inside one long
// tx would risk holding a write lock across a peer round-trip).
func FanOutTemplateEvent(
	ctx context.Context,
	deps AppDeps,
	event LifecycleEvent,
	templateHash string,
	spec node.TemplateSpec,
	payload TemplatePayload,
	tx persistence.Tx,
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

	flog := slog.With("event", fmt.Sprintf("%v", event), "template_hash", templateHash, "peers", len(peerNames))
	perPeerErr := map[string]error{}
	for _, name := range peerNames {
		tPeer := time.Now()
		var row *persistence.LifecycleIdempotencyRow
		if err := withOptionalTx(ctx, deps.Persist, tx, func(ctx context.Context, useTx persistence.Tx) error {
			r, err := deps.Persist.LifecycleIdempotency().Get(ctx, name, scopeKind, templateHash, useTx)
			row = r
			return err
		}); err != nil {
			flog.Debug("fanout.peer.lookup", "peer", name, "elapsed_ms", time.Since(tPeer).Milliseconds(), "err", err)
			perPeerErr[name] = err
			return peerNames, perPeerErr, fmt.Errorf("FanOutTemplateEvent: lifecycle row lookup for %q: %w", name, err)
		}
		flog.Debug("fanout.peer.lookup", "peer", name, "elapsed_ms", time.Since(tPeer).Milliseconds())
		if !deletesRow && row != nil && row.State == target {
			flog.Debug("fanout.peer.skip", "peer", name, "reason", "already_at_target")
			continue
		}
		if deletesRow && row == nil {
			flog.Debug("fanout.peer.skip", "peer", name, "reason", "delete_no_row")
			continue
		}
		if deps.LifecycleSubs == nil {
			perPeerErr[name] = fmt.Errorf("lifecycle subscriber registry not initialized")
			return peerNames, perPeerErr, fmt.Errorf("FanOutTemplateEvent: lifecycle subscriber registry not initialized")
		}
		s, ok := deps.LifecycleSubs.Get(name)
		if !ok {
			flog.Debug("fanout.peer.skip", "peer", name, "reason", "not_subscribed")
			// Peer is referenced by the template but does not subscribe
			// to lifecycle events; skip silently.
			continue
		}
		tDispatch := time.Now()
		if err := dispatchTemplateEvent(ctx, s, event, templateHash, payload); err != nil {
			flog.Debug("fanout.peer.dispatch.err", "peer", name, "elapsed_ms", time.Since(tDispatch).Milliseconds(), "err", err)
			perPeerErr[name] = err
			return peerNames, perPeerErr, fmt.Errorf("FanOutTemplateEvent: peer %q: %w", name, err)
		}
		if deletesRow {
			if err := withOptionalTx(ctx, deps.Persist, tx, func(ctx context.Context, useTx persistence.Tx) error {
				return deps.Persist.LifecycleIdempotency().Delete(ctx, name, scopeKind, templateHash, useTx)
			}); err != nil {
				perPeerErr[name] = err
				return peerNames, perPeerErr, fmt.Errorf("FanOutTemplateEvent: delete lifecycle row %q: %w", name, err)
			}
			continue
		}
		if err := withOptionalTx(ctx, deps.Persist, tx, func(ctx context.Context, useTx persistence.Tx) error {
			return deps.Persist.LifecycleIdempotency().Upsert(ctx, persistence.LifecycleIdempotencyRow{
				StoreRegistrationName: name,
				ScopeKind:             scopeKind,
				ScopeID:               templateHash,
				State:                 target,
			}, useTx)
		}); err != nil {
			perPeerErr[name] = err
			return peerNames, perPeerErr, fmt.Errorf("FanOutTemplateEvent: upsert lifecycle row %q: %w", name, err)
		}
	}
	return peerNames, nil, nil
}

// withOptionalTx runs fn inside the supplied tx if non-nil, otherwise
// opens a fresh short Persist.Transaction. Used by the lifecycle fan-out
// helpers to support both inside-tx callers (deploy/undeploy holding the
// templates row lock) and outside-tx callers (register / instance-create
// / terminator) without forcing the caller to wrap each persistence call
// itself.
func withOptionalTx(
	ctx context.Context,
	store persistence.Tables,
	tx persistence.Tx,
	fn func(ctx context.Context, tx persistence.Tx) error,
) error {
	if tx != nil {
		return fn(ctx, tx)
	}
	return store.Transaction(ctx, fn)
}

// FanOutInstanceEvent is the instance-scope analogue of
// FanOutTemplateEvent. See FanOutTemplateEvent for the `tx` semantics —
// callers inside an open transaction MUST pass it through, otherwise
// the SQLite single-connection pool self-deadlocks.
func FanOutInstanceEvent(
	ctx context.Context,
	deps AppDeps,
	event LifecycleEvent,
	templateHash, instanceID string,
	spec node.TemplateSpec,
	payload InstancePayload,
	tx persistence.Tx,
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
		var row *persistence.LifecycleIdempotencyRow
		if err := withOptionalTx(ctx, deps.Persist, tx, func(ctx context.Context, useTx persistence.Tx) error {
			r, err := deps.Persist.LifecycleIdempotency().Get(ctx, name, scopeKind, instanceID, useTx)
			row = r
			return err
		}); err != nil {
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
		if err := dispatchInstanceEvent(ctx, s, event, templateHash, instanceID, payload); err != nil {
			perPeerErr[name] = err
			return peerNames, perPeerErr, fmt.Errorf("FanOutInstanceEvent: peer %q: %w", name, err)
		}
		if deletesRow {
			if err := withOptionalTx(ctx, deps.Persist, tx, func(ctx context.Context, useTx persistence.Tx) error {
				return deps.Persist.LifecycleIdempotency().Delete(ctx, name, scopeKind, instanceID, useTx)
			}); err != nil {
				perPeerErr[name] = err
				return peerNames, perPeerErr, fmt.Errorf("FanOutInstanceEvent: delete lifecycle row %q: %w", name, err)
			}
			continue
		}
		if err := withOptionalTx(ctx, deps.Persist, tx, func(ctx context.Context, useTx persistence.Tx) error {
			return deps.Persist.LifecycleIdempotency().Upsert(ctx, persistence.LifecycleIdempotencyRow{
				StoreRegistrationName: name,
				ScopeKind:             scopeKind,
				ScopeID:               instanceID,
				State:                 target,
			}, useTx)
		}); err != nil {
			perPeerErr[name] = err
			return peerNames, perPeerErr, fmt.Errorf("FanOutInstanceEvent: upsert lifecycle row %q: %w", name, err)
		}
	}
	return peerNames, nil, nil
}

func dispatchTemplateEvent(ctx context.Context, s locks.LifecycleSubscriber, event LifecycleEvent, templateID string, payload TemplatePayload) error {
	switch event {
	case EventTemplateRegistered:
		return s.OnTemplateRegistered(ctx, locks.OnTemplateRegisteredRequest{
			TemplateHash: templateID,
			Spec:         payload.Spec,
		})
	case EventTemplateDeployed:
		return s.OnTemplateDeployed(ctx, locks.OnTemplateDeployedRequest{
			TemplateHash: templateID,
			Tags:         payload.Tags,
		})
	case EventTemplateUndeployed:
		return s.OnTemplateUndeployed(ctx, locks.OnTemplateUndeployedRequest{
			TemplateHash: templateID,
		})
	case EventTemplateDeregistered:
		return s.OnTemplateDeregistered(ctx, locks.OnTemplateDeregisteredRequest{
			TemplateHash: templateID,
		})
	}
	return fmt.Errorf("dispatchTemplateEvent: %v is not template-scope", event)
}

func dispatchInstanceEvent(ctx context.Context, s locks.LifecycleSubscriber, event LifecycleEvent, templateID, instanceID string, payload InstancePayload) error {
	switch event {
	case EventInstanceCreated:
		return s.OnInstanceCreated(ctx, locks.OnInstanceCreatedRequest{
			InstanceID:   instanceID,
			TemplateHash: templateID,
			InstanceKey:  payload.InstanceKey,
			Params:       payload.Params,
		})
	case EventInstanceTerminated:
		return s.OnInstanceTerminated(ctx, locks.OnInstanceTerminatedRequest{
			InstanceID:         instanceID,
			TemplateHash:       templateID,
			TerminatedAtUnixMs: payload.TerminatedAtUnixMs,
		})
	}
	return fmt.Errorf("dispatchInstanceEvent: %v is not instance-scope", event)
}

// lifecyclePeersForSpec returns the deduped, lex-sorted set of peer
// names referenced by the template — the union of (a) every store-alias
// declared on each node and (b) every executor declared on each node.
// Per service-protocol-contract.md §3, a peer's `protocols:` list is the
// only declaration mechanism for lifecycle subscription; peers that
// declare lifecycle_subscriber but are referenced via the executor field
// must still receive events on templates that reference them. A peer
// that's referenced but does not appear in deps.LifecycleSubs is
// fanned-out-skipped at dispatch time (no error, no bookkeeping).
func lifecyclePeersForSpec(_ AppDeps, spec node.TemplateSpec) []string {
	return peersReferencedBySpec(spec)
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

// peersReferencedBySpec returns the deduped peer-name set referenced by
// the template's nodes — every store name on every node's Stores entry
// PLUS every non-empty Executor name on each node — sorted
// lexicographically per spec §5.6 for deterministic call order. Both
// classes participate in lifecycle fan-out because either may declare
// the lifecycle_subscriber protocol on its rimsky.yml peer block.
func peersReferencedBySpec(spec node.TemplateSpec) []string {
	seen := map[string]struct{}{}
	for _, n := range spec.Nodes {
		for _, s := range n.Stores {
			if s.Name == "" {
				continue
			}
			seen[s.Name] = struct{}{}
		}
		if n.Executor != "" {
			seen[n.Executor] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
