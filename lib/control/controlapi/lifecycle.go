// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package controlapi

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/lifecycle"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
)

func withLifecycleScopeTx(
	ctx context.Context,
	deps AppDeps,
	scopeKind persistence.LifecycleIdempotencyScopeKind,
	scopeID string,
	fn func(ctx context.Context, tx persistence.Tx) error,
	tx persistence.Tx,
) error {
	if deps.AdvisoryLocker == nil {
		return errAdvisoryLockerNotInitialized
	}
	return withOptionalTx(ctx, deps.Persist, func(ctx context.Context, useTx persistence.Tx) error {
		if err := deps.AdvisoryLocker.TakeLifecycleScopeLock(ctx, scopeKind, scopeID, useTx); err != nil {
			return fmt.Errorf("lifecycle scope lock %s/%s: %w", scopeKind, scopeID, err)
		}
		return fn(ctx, useTx)
	}, tx)
}

type fanOutOutcome int

const (
	fanOutContinue fanOutOutcome = iota
	fanOutAbort
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
	InstanceKey           string
	Params                json.RawMessage
	TerminatedAtUnixMs    int64
	ServiceBindings       json.RawMessage
	OwnerAPIKeyID         *shared.UUID
	TargetRoutingIdentity string
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

	perPeerErr := map[string]error{}
	for _, name := range peerNames {
		if err := withLifecycleScopeTx(ctx, deps, scopeKind, templateHash, func(ctx context.Context, useTx persistence.Tx) error {
			row, err := deps.Persist.LifecycleIdempotency().Get(ctx, name, scopeKind, templateHash, useTx)
			if err != nil {
				return fmt.Errorf("lifecycle row lookup for %q: %w", name, err)
			}
			if !deletesRow && row != nil && row.State == target {
				return nil
			}
			if deletesRow && row == nil {
				return nil
			}
			if deps.LifecycleSubs == nil {
				return errLifecycleSubsNotInitialized
			}
			s, ok := deps.LifecycleSubs.Get(name)
			if !ok {
				return nil
			}
			if err := dispatchTemplateEvent(ctx, s, event, templateHash, payload); err != nil {
				return fmt.Errorf("peer %q: %w", name, err)
			}
			if deletesRow {
				if err := deps.Persist.LifecycleIdempotency().Delete(ctx, name, scopeKind, templateHash, useTx); err != nil {
					return fmt.Errorf("delete lifecycle row %q: %w", name, err)
				}
				return nil
			}
			if err := deps.Persist.LifecycleIdempotency().Upsert(ctx, persistence.LifecycleIdempotencyRow{
				ClaimProducerName: name,
				ScopeKind:         scopeKind,
				ScopeID:           templateHash,
				State:             target,
			}, useTx); err != nil {
				return fmt.Errorf("upsert lifecycle row %q: %w", name, err)
			}
			return nil
		}, tx); err != nil {
			perPeerErr[name] = err
			return peerNames, perPeerErr, fmt.Errorf("FanOutTemplateEvent: %w", err)
		}
	}
	return peerNames, nil, nil
}

func withOptionalTx(
	ctx context.Context, store persistence.Tables, fn func(ctx context.Context, tx persistence.Tx) error, tx persistence.Tx,
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
		outcome, ferr := fanOutInstancePeer(ctx, deps, name, scopeKind, instanceID, target, deletesRow, event, templateHash, payload, perPeerErr, tx)
		if outcome == fanOutAbort {
			return peerNames, perPeerErr, ferr
		}
	}
	return peerNames, nil, nil
}

func fanOutInstancePeer(
	ctx context.Context, deps AppDeps, name string, scopeKind persistence.LifecycleIdempotencyScopeKind, instanceID string, target persistence.LifecycleIdempotencyState, deletesRow bool, event LifecycleEvent, templateHash string, payload InstancePayload, perPeerErr map[string]error, tx persistence.Tx,
) (fanOutOutcome, error) {
	if err := withLifecycleScopeTx(ctx, deps, scopeKind, instanceID, func(ctx context.Context, useTx persistence.Tx) error {
		row, err := deps.Persist.LifecycleIdempotency().Get(ctx, name, scopeKind, instanceID, useTx)
		if err != nil {
			return fmt.Errorf("lifecycle row lookup for %q: %w", name, err)
		}
		if !deletesRow && row != nil && row.State == target {
			return nil
		}
		if deletesRow && row == nil {
			return nil
		}
		if deps.LifecycleSubs == nil {
			return errLifecycleSubsNotInitialized
		}
		s, ok := deps.LifecycleSubs.Get(name)
		if !ok {
			return nil
		}
		if err := dispatchInstanceEvent(ctx, s, event, templateHash, instanceID, payload); err != nil {
			return fmt.Errorf("peer %q: %w", name, err)
		}
		if deletesRow {
			if err := deps.Persist.LifecycleIdempotency().Delete(ctx, name, scopeKind, instanceID, useTx); err != nil {
				return fmt.Errorf("delete lifecycle row %q: %w", name, err)
			}
			return nil
		}
		if err := deps.Persist.LifecycleIdempotency().Upsert(ctx, persistence.LifecycleIdempotencyRow{
			ClaimProducerName: name,
			ScopeKind:         scopeKind,
			ScopeID:           instanceID,
			State:             target,
		}, useTx); err != nil {
			return fmt.Errorf("upsert lifecycle row %q: %w", name, err)
		}
		return nil
	}, tx); err != nil {
		perPeerErr[name] = err
		return fanOutAbort, fmt.Errorf("FanOutInstanceEvent: %w", err)
	}
	return fanOutContinue, nil
}

func fanOutRunScopeEventForPeers(
	ctx context.Context,
	deps AppDeps,
	peers []string,
	runScopeID shared.UUID,
	instanceID shared.UUID,
	terminalReason string,
	tx persistence.Tx,
) ([]string, map[string]error, error) {
	scopeID := runScopeID.String()
	scopeKind := persistence.LifecycleIdempotencyScopeRunScope

	perPeerErr := map[string]error{}
	for _, name := range peers {
		outcome, ferr := fanOutRunScopePeer(ctx, deps, name, scopeKind, scopeID, instanceID, terminalReason, perPeerErr, tx)
		if outcome == fanOutAbort {
			return peers, perPeerErr, ferr
		}
	}
	return peers, perPeerErr, nil
}

func fanOutRunScopePeer(
	ctx context.Context, deps AppDeps, name string, scopeKind persistence.LifecycleIdempotencyScopeKind, scopeID string, instanceID shared.UUID, terminalReason string, perPeerErr map[string]error, tx persistence.Tx,
) (fanOutOutcome, error) {
	var deliveryErr error
	if err := withLifecycleScopeTx(ctx, deps, scopeKind, scopeID, func(ctx context.Context, useTx persistence.Tx) error {
		row, err := deps.Persist.LifecycleIdempotency().Get(ctx, name, scopeKind, scopeID, useTx)
		if err != nil {
			return fmt.Errorf("lifecycle row lookup for %q: %w", name, err)
		}
		if row != nil && row.State == persistence.LifecycleIdempotencyStateRunScopeTerminal {
			return nil
		}
		if deps.LifecycleSubs == nil {
			return errLifecycleSubsNotInitialized
		}
		s, ok := deps.LifecycleSubs.Get(name)
		if !ok {
			return nil
		}
		req := lifecycle.OnRunScopeTerminalRequest{
			RunScopeID:     scopeID,
			TerminalReason: terminalReason,
			InstanceID:     instanceID.String(),
		}
		if err := s.OnRunScopeTerminal(ctx, req); err != nil {
			deliveryErr = err
			return nil
		}
		if err := deps.Persist.LifecycleIdempotency().Upsert(ctx, persistence.LifecycleIdempotencyRow{
			ClaimProducerName: name,
			ScopeKind:         scopeKind,
			ScopeID:           scopeID,
			State:             persistence.LifecycleIdempotencyStateRunScopeTerminal,
		}, useTx); err != nil {
			return fmt.Errorf("upsert lifecycle row %q: %w", name, err)
		}
		return nil
	}, tx); err != nil {
		perPeerErr[name] = err
		return fanOutAbort, fmt.Errorf("FanOutRunScopeEvent: %w", err)
	}
	if deliveryErr != nil {
		perPeerErr[name] = deliveryErr
	}
	return fanOutContinue, nil
}

// @concept: run-scope
// @concept: frame
func CloseAndFanOutRunScopesForInstance(
	ctx context.Context,
	deps AppDeps,
	tplSpec node.TemplateSpec,
	instanceID shared.UUID,
	terminalReason string,
) error {
	return closeAndFanOutRunScopesForInstanceWithPeers(ctx, deps, LifecyclePeersForSpec(deps, tplSpec), instanceID, terminalReason)
}

// @concept: run-scope
// @concept: frame
func closeAndFanOutRunScopesForInstanceWithPeers(
	ctx context.Context,
	deps AppDeps,
	peers []string,
	instanceID shared.UUID,
	terminalReason string,
) error {
	logger := deps.Logger
	pag := persistence.ListPagination{Limit: 256}
	instArg := instanceID
	filter := persistence.FrameListFilter{InstanceID: &instArg}
	seen := map[shared.UUID]struct{}{}
	var firstErr error
	for {
		var page persistence.PaginatedListResult[persistence.FrameRow]
		if err := deps.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			p, err := deps.Persist.Frames().ListForObservability(ctx, filter, pag, tx)
			page = p
			return err
		}); err != nil {
			if logger != nil {
				logger.Warn("CloseAndFanOutRunScopesForInstance: list frames failed",
					"instance_id", instanceID.String(),
					"error", err.Error())
			}
			if firstErr == nil {
				firstErr = err
			}
			return firstErr
		}
		for _, f := range page.Rows {
			root := f.RootRunScopeID
			if root == (shared.UUID{}) {
				continue
			}
			if _, dup := seen[root]; dup {
				continue
			}
			seen[root] = struct{}{}
			if err := closeAndFanOutScopeTree(ctx, deps, peers, instanceID, f.FrameID, root, terminalReason); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		if page.NextCursor == "" {
			return firstErr
		}
		pag.Cursor = page.NextCursor
	}
}

// @concept: run-scope
func closeAndFanOutScopeTree(
	ctx context.Context,
	deps AppDeps,
	peers []string,
	instanceID, frameID, rootRunScopeID shared.UUID,
	terminalReason string,
) error {
	logger := deps.Logger
	var treeDeepestFirst []persistence.RunScopeRow
	if err := deps.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		rows, err := deps.Persist.RunScopes().ListTreeDeepestFirst(ctx, rootRunScopeID, tx)
		treeDeepestFirst = rows
		return err
	}); err != nil {
		if logger != nil {
			logger.Warn("CloseAndFanOutRunScopesForInstance: list run-scope tree failed",
				"instance_id", instanceID.String(),
				"frame_id", frameID.String(),
				"root_run_scope_id", rootRunScopeID.String(),
				"error", err.Error())
		}
		return err
	}
	var firstErr error
	for _, scope := range treeDeepestFirst {
		if err := deps.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			return deps.Persist.RunScopes().Close(ctx, scope.ID, tx)
		}); err != nil {
			if logger != nil {
				logger.Warn("CloseAndFanOutRunScopesForInstance: close run-scope failed",
					"instance_id", instanceID.String(),
					"frame_id", frameID.String(),
					"run_scope_id", scope.ID.String(),
					"error", err.Error())
			}
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		_, perPeerErr, err := fanOutRunScopeEventForPeers(ctx, deps, peers, scope.ID, instanceID, terminalReason, nil)
		if err != nil {
			if logger != nil {
				logger.Warn("CloseAndFanOutRunScopesForInstance: run-scope fan-out failed",
					"instance_id", instanceID.String(),
					"run_scope_id", scope.ID.String(),
					"error", err.Error())
			}
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if len(perPeerErr) > 0 {
			if logger != nil {
				logger.Warn("CloseAndFanOutRunScopesForInstance: run-scope fan-out partial failure",
					"instance_id", instanceID.String(),
					"run_scope_id", scope.ID.String(),
					"failed_peer_count", len(perPeerErr))
			}
			if firstErr == nil {
				firstErr = fmt.Errorf("run-scope %s: %d peer(s) failed on_run_scope_terminal", scope.ID.String(), len(perPeerErr))
			}
		}
	}
	return firstErr
}

var errLifecycleSubsNotInitialized = fmt.Errorf("lifecycle subscriber registry not initialized")

var errAdvisoryLockerNotInitialized = fmt.Errorf("advisory locker not initialized")

// @concept: run-scope
// @concept: lifecycle-subscriber
func fanOutInstanceTerminatedFromLifecycleRows(
	ctx context.Context,
	deps AppDeps,
	inst persistence.InstanceRow,
	terminalReason string,
) error {
	var rows []persistence.LifecycleIdempotencyRow
	if err := deps.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := deps.Persist.LifecycleIdempotency().ListByScope(ctx,
			persistence.LifecycleIdempotencyScopeInstance, inst.ID.String(), tx)
		rows = r
		return err
	}); err != nil {
		return fmt.Errorf("lifecycle row list: %w", err)
	}
	if deps.LifecycleSubs == nil {
		return errLifecycleSubsNotInitialized
	}

	peers := make([]string, 0, len(rows))
	for _, r := range rows {
		peers = append(peers, r.ClaimProducerName)
	}
	if err := closeAndFanOutRunScopesForInstanceWithPeers(ctx, deps, peers, inst.ID, terminalReason); err != nil {
		return err
	}

	var terminatedAtMs int64
	if inst.TerminatedAt != nil {
		terminatedAtMs = inst.TerminatedAt.UnixMilli()
	}
	for _, r := range rows {
		if err := dispatchInstanceTerminatedForPeer(ctx, deps, inst.ID.String(), inst.TemplateHash, terminatedAtMs, r.ClaimProducerName); err != nil {
			return err
		}
	}
	return nil
}

func dispatchInstanceTerminatedForPeer(
	ctx context.Context,
	deps AppDeps,
	instanceID, templateHash string,
	terminatedAtMs int64,
	peerName string,
) error {
	return withLifecycleScopeTx(ctx, deps, persistence.LifecycleIdempotencyScopeInstance, instanceID, func(ctx context.Context, tx persistence.Tx) error {
		row, err := deps.Persist.LifecycleIdempotency().Get(ctx,
			peerName, persistence.LifecycleIdempotencyScopeInstance, instanceID, tx)
		if err != nil {
			return fmt.Errorf("lifecycle row lookup for %q: %w", peerName, err)
		}
		if row == nil {
			return nil
		}
		s, ok := deps.LifecycleSubs.Get(peerName)
		if !ok {
			if deps.Logger != nil {
				deps.Logger.Warn("lifecycle.unknown_subscriber_fallback",
					"instance_id", instanceID,
					"peer_name", peerName)
			}
			return deps.Persist.LifecycleIdempotency().Delete(ctx,
				peerName, persistence.LifecycleIdempotencyScopeInstance, instanceID, tx)
		}
		if err := s.OnInstanceTerminated(ctx, lifecycle.OnInstanceTerminatedRequest{
			InstanceID:         instanceID,
			TemplateHash:       templateHash,
			TerminatedAtUnixMs: terminatedAtMs,
		}); err != nil {
			return fmt.Errorf("peer %q OnInstanceTerminated: %w", peerName, err)
		}
		return deps.Persist.LifecycleIdempotency().Delete(ctx,
			peerName, persistence.LifecycleIdempotencyScopeInstance, instanceID, tx)
	}, nil)
}

func dispatchTemplateEvent(ctx context.Context, s lifecycle.Subscriber, event LifecycleEvent, templateID string, payload TemplatePayload) error {
	switch event {
	case EventTemplateRegistered:
		return s.OnTemplateRegistered(ctx, lifecycle.OnTemplateRegisteredRequest{
			TemplateHash: templateID,
			Spec:         payload.Spec,
		})
	case EventTemplateDeployed:
		return s.OnTemplateDeployed(ctx, lifecycle.OnTemplateDeployedRequest{
			TemplateHash: templateID,
			Tags:         payload.Tags,
		})
	case EventTemplateUndeployed:
		return s.OnTemplateUndeployed(ctx, lifecycle.OnTemplateUndeployedRequest{
			TemplateHash: templateID,
		})
	case EventTemplateDeregistered:
		return s.OnTemplateDeregistered(ctx, lifecycle.OnTemplateDeregisteredRequest{
			TemplateHash: templateID,
		})
	}
	return fmt.Errorf("dispatchTemplateEvent: %v is not template-scope", event)
}

func dispatchInstanceEvent(ctx context.Context, s lifecycle.Subscriber, event LifecycleEvent, templateID, instanceID string, payload InstancePayload) error {
	switch event {
	case EventInstanceCreated:
		return s.OnInstanceCreated(ctx, lifecycle.OnInstanceCreatedRequest{
			InstanceID:            instanceID,
			TemplateHash:          templateID,
			InstanceKey:           payload.InstanceKey,
			Params:                payload.Params,
			ServiceBindings:       payload.ServiceBindings,
			OwnerAPIKeyID:         uuidString(payload.OwnerAPIKeyID),
			TargetRoutingIdentity: payload.TargetRoutingIdentity,
		})
	case EventInstanceTerminated:
		return s.OnInstanceTerminated(ctx, lifecycle.OnInstanceTerminatedRequest{
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
		proxyServiceNames := make([]string, 0, len(deps.LateBindServiceProxies))
		for svcName := range deps.LateBindServiceProxies {
			proxyServiceNames = append(proxyServiceNames, svcName)
		}
		sort.Strings(proxyServiceNames)
		for _, svcName := range proxyServiceNames {
			proxyName := deps.LateBindServiceProxies[svcName]
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
