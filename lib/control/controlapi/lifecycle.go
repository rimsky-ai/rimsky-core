// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package controlapi

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
)

type LifecycleEvent int

const (
	EventTemplateRegistered LifecycleEvent = iota
	EventTemplateDeployed
	EventTemplateUndeployed
	EventTemplateDeregistered
	EventInstanceCreated
	EventInstanceTerminated
)

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

type TemplatePayload struct {
	Spec json.RawMessage
	Tags []string
}

type InstancePayload struct {
	InstanceKey        string
	Params             json.RawMessage
	TerminatedAtUnixMs int64
	ServiceBindings    json.RawMessage
	OwnerAPIKeyID      *shared.UUID
}

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
	peerNames := peersReferencedBySpec(spec)
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
	peerNames := LifecyclePeersForSpec(deps, spec)
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

func FanOutRunScopeEvent(
	ctx context.Context,
	deps AppDeps,
	tplSpec node.TemplateSpec,
	runScopeID shared.UUID,
	instanceID shared.UUID,
	terminalReason string,
	tx persistence.Tx,
) ([]string, map[string]error, error) {
	peers := LifecyclePeersForSpec(deps, tplSpec)
	scopeID := runScopeID.String()
	scopeKind := persistence.LifecycleIdempotencyScopeRunScope

	perPeerErr := map[string]error{}
	for _, name := range peers {
		var row *persistence.LifecycleIdempotencyRow
		if err := withOptionalTx(ctx, deps.Persist, tx, func(ctx context.Context, useTx persistence.Tx) error {
			r, err := deps.Persist.LifecycleIdempotency().Get(ctx, name, scopeKind, scopeID, useTx)
			row = r
			return err
		}); err != nil {
			perPeerErr[name] = err
			return peers, perPeerErr, fmt.Errorf("FanOutRunScopeEvent: lifecycle row lookup for %q: %w", name, err)
		}
		if row != nil && row.State == persistence.LifecycleIdempotencyStateRunScopeTerminal {
			continue
		}
		if deps.LifecycleSubs == nil {
			perPeerErr[name] = fmt.Errorf("lifecycle subscriber registry not initialized")
			return peers, perPeerErr, fmt.Errorf("FanOutRunScopeEvent: lifecycle subscriber registry not initialized")
		}
		s, ok := deps.LifecycleSubs.Get(name)
		if !ok {
			continue
		}
		req := locks.OnRunScopeTerminalRequest{
			RunScopeID:     scopeID,
			TerminalReason: terminalReason,
			InstanceID:     instanceID.String(),
		}
		if err := s.OnRunScopeTerminal(ctx, req); err != nil {
			perPeerErr[name] = err
			continue
		}
		if err := withOptionalTx(ctx, deps.Persist, tx, func(ctx context.Context, useTx persistence.Tx) error {
			return deps.Persist.LifecycleIdempotency().Upsert(ctx, persistence.LifecycleIdempotencyRow{
				StoreRegistrationName: name,
				ScopeKind:             scopeKind,
				ScopeID:               scopeID,
				State:                 persistence.LifecycleIdempotencyStateRunScopeTerminal,
			}, useTx)
		}); err != nil {
			perPeerErr[name] = err
			return peers, perPeerErr, fmt.Errorf("FanOutRunScopeEvent: upsert lifecycle row %q: %w", name, err)
		}
	}
	return peers, perPeerErr, nil
}

// @concept: run-scope
// @concept: frame
func CloseAndFanOutFrameRootRunScopesForInstance(
	ctx context.Context,
	deps AppDeps,
	tplSpec node.TemplateSpec,
	instanceID shared.UUID,
	terminalReason string,
) {
	logger := deps.Logger
	pag := persistence.ListPagination{Limit: 256}
	instArg := instanceID
	filter := persistence.FrameListFilter{InstanceID: &instArg}
	seen := map[shared.UUID]struct{}{}
	for {
		var page persistence.PaginatedListResult[persistence.FrameRow]
		if err := deps.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			p, err := deps.Persist.Frames().ListForObservability(ctx, filter, pag, tx)
			page = p
			return err
		}); err != nil {
			if logger != nil {
				logger.Warn("CloseAndFanOutFrameRootRunScopesForInstance: list frames failed",
					"instance_id", instanceID.String(),
					"error", err.Error())
			}
			return
		}
		for _, f := range page.Rows {
			scope := f.RootRunScopeID
			if scope == (shared.UUID{}) {
				continue
			}
			if _, dup := seen[scope]; dup {
				continue
			}
			seen[scope] = struct{}{}
			if err := deps.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
				return deps.Persist.RunScopes().Close(ctx, tx, scope)
			}); err != nil {
				if logger != nil {
					logger.Warn("CloseAndFanOutFrameRootRunScopesForInstance: close run-scope failed",
						"instance_id", instanceID.String(),
						"frame_id", f.FrameID.String(),
						"root_run_scope_id", scope.String(),
						"error", err.Error())
				}
				continue
			}
			_, _, _ = FanOutRunScopeEvent(ctx, deps, tplSpec, scope, instanceID, terminalReason, nil)
		}
		if page.NextCursor == "" {
			return
		}
		pag.Cursor = page.NextCursor
	}
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
			InstanceID:      instanceID,
			TemplateHash:    templateID,
			InstanceKey:     payload.InstanceKey,
			Params:          payload.Params,
			ServiceBindings: payload.ServiceBindings,
			OwnerAPIKeyID:   uuidString(payload.OwnerAPIKeyID),
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

func LifecyclePeersForSpec(deps AppDeps, spec node.TemplateSpec) []string {
	peers := peersReferencedBySpec(spec)
	if len(spec.LateBindServices) > 0 {
		seen := make(map[string]struct{}, len(peers))
		for _, p := range peers {
			seen[p] = struct{}{}
		}
		for _, proxyName := range deps.LateBindServiceProxies {
			if proxyName == "" {
				continue
			}
			if _, exists := seen[proxyName]; exists {
				continue
			}
			peers = append(peers, proxyName)
			seen[proxyName] = struct{}{}
		}
	}
	return peers
}

func uuidString(u *shared.UUID) string {
	if u == nil {
		return ""
	}
	return u.String()
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

func peersReferencedBySpec(spec node.TemplateSpec) []string {
	seen := map[string]struct{}{}
	for _, n := range spec.Nodes {
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
	for _, p := range spec.Publishers {
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
