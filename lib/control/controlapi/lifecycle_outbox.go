// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @decision: lifecycle-subscriber-at-least-once-delivery
// @decision: lifecycle-fanout-after-commit
// @concept: lifecycle-subscriber

package controlapi

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
)

type stagedLifecyclePayload struct {
	TemplateHash string           `json:"template_hash"`
	InstanceID   string           `json:"instance_id,omitempty"`
	Template     *TemplatePayload `json:"template,omitempty"`
	Instance     *InstancePayload `json:"instance,omitempty"`
}

func parseLifecycleEvent(name string) (LifecycleEvent, bool) {
	for _, e := range []LifecycleEvent{
		EventTemplateRegistered,
		EventTemplateDeployed,
		EventTemplateUndeployed,
		EventTemplateDeregistered,
		EventInstanceCreated,
		EventInstanceTerminated,
	} {
		if e.String() == name {
			return e, true
		}
	}
	return 0, false
}

func lifecycleEventDeletesLedgerRow(event LifecycleEvent) bool {
	return event == EventTemplateDeregistered || event == EventInstanceTerminated
}

func stageLifecycleDeliveries(
	ctx context.Context,
	deps AppDeps,
	event LifecycleEvent,
	scopeKind persistence.LifecycleIdempotencyScopeKind,
	scopeID string,
	peers []string,
	payload stagedLifecyclePayload,
	tx persistence.Tx,
) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal staged lifecycle payload for %s/%s: %w", scopeKind, scopeID, err)
	}
	for _, name := range peers {
		if err := deps.Persist.LifecycleOutbox().Stage(ctx, persistence.LifecycleOutboxRow{
			ClaimProducerName: name,
			ScopeKind:         scopeKind,
			ScopeID:           scopeID,
			Event:             event.String(),
			Payload:           body,
		}, tx); err != nil {
			return fmt.Errorf("stage %s for peer %q: %w", event.String(), name, err)
		}
	}
	return nil
}

// @decision: lifecycle-fanout-after-commit
func StageTemplateLifecycleEvent(
	ctx context.Context,
	deps AppDeps,
	event LifecycleEvent,
	templateHash string,
	spec node.TemplateSpec,
	payload TemplatePayload,
	tx persistence.Tx,
) error {
	switch event {
	case EventTemplateRegistered, EventTemplateDeployed, EventTemplateUndeployed, EventTemplateDeregistered:
	default:
		return fmt.Errorf("StageTemplateLifecycleEvent: %v is not a template-scope event", event)
	}
	return stageLifecycleDeliveries(ctx, deps, event,
		persistence.LifecycleIdempotencyScopeTemplate, templateHash,
		peersReferencedBySpec(spec),
		stagedLifecyclePayload{TemplateHash: templateHash, Template: &payload},
		tx)
}

// @decision: lifecycle-fanout-after-commit
func StageInstanceLifecycleEvent(
	ctx context.Context,
	deps AppDeps,
	event LifecycleEvent,
	templateHash, instanceID string,
	spec node.TemplateSpec,
	payload InstancePayload,
	tx persistence.Tx,
) error {
	switch event {
	case EventInstanceCreated, EventInstanceTerminated:
	default:
		return fmt.Errorf("StageInstanceLifecycleEvent: %v is not an instance-scope event", event)
	}
	return stageLifecycleDeliveries(ctx, deps, event,
		persistence.LifecycleIdempotencyScopeInstance, instanceID,
		LifecyclePeersForSpec(deps, spec),
		stagedLifecyclePayload{TemplateHash: templateHash, InstanceID: instanceID, Instance: &payload},
		tx)
}

// @decision: lifecycle-fanout-after-commit
// @concept: lifecycle-subscriber
func deliverStagedLifecycleScope(
	ctx context.Context,
	deps AppDeps,
	scopeKind persistence.LifecycleIdempotencyScopeKind,
	scopeID string,
) (map[string]error, error) {
	var rows []persistence.LifecycleOutboxRow
	if err := deps.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := deps.Persist.LifecycleOutbox().ListPendingForScope(ctx, scopeKind, scopeID, tx)
		rows = r
		return err
	}); err != nil {
		return nil, fmt.Errorf("list staged lifecycle deliveries for %s/%s: %w", scopeKind, scopeID, err)
	}
	return deliverStagedLifecycleRows(ctx, deps, rows), nil
}

type stagedDeliveryStream struct {
	peer      string
	scopeKind persistence.LifecycleIdempotencyScopeKind
	scopeID   string
}

// @decision: lifecycle-subscriber-at-least-once-delivery
func deliverStagedLifecycleRows(ctx context.Context, deps AppDeps, rows []persistence.LifecycleOutboxRow) map[string]error {
	perPeerErr := map[string]error{}
	blocked := map[stagedDeliveryStream]struct{}{}
	for _, row := range rows {
		stream := stagedDeliveryStream{peer: row.ClaimProducerName, scopeKind: row.ScopeKind, scopeID: row.ScopeID}
		if _, held := blocked[stream]; held {
			continue
		}
		if err := deliverStagedLifecycleRow(ctx, deps, row); err != nil {
			blocked[stream] = struct{}{}
			perPeerErr[row.ClaimProducerName] = err
		}
	}
	return perPeerErr
}

// @decision: lifecycle-subscriber-at-least-once-delivery
// @concept: lifecycle-subscriber
func deliverStagedLifecycleRow(ctx context.Context, deps AppDeps, row persistence.LifecycleOutboxRow) error {
	event, ok := parseLifecycleEvent(row.Event)
	if !ok {
		return dropUndeliverableStagedRow(ctx, deps, row, "unknown_event")
	}
	var payload stagedLifecyclePayload
	if err := json.Unmarshal(row.Payload, &payload); err != nil {
		return dropUndeliverableStagedRow(ctx, deps, row, "undecodable_payload")
	}
	deletesLedgerRow := lifecycleEventDeletesLedgerRow(event)
	target := targetStateFor(event)

	return withLifecycleScopeTx(ctx, deps, row.ScopeKind, row.ScopeID, func(ctx context.Context, tx persistence.Tx) error {
		ledger, err := deps.Persist.LifecycleIdempotency().Get(ctx, row.ClaimProducerName, row.ScopeKind, row.ScopeID, tx)
		if err != nil {
			return fmt.Errorf("lifecycle row lookup for %q: %w", row.ClaimProducerName, err)
		}
		if !deletesLedgerRow && ledger != nil && ledger.State == target {
			logStagedDeliverySkipped(deps, row, "peer already at this state")
			return deleteStagedRow(ctx, deps, row, tx)
		}
		if deletesLedgerRow && ledger == nil {
			logStagedDeliverySkipped(deps, row, "peer holds no ledger row for this scope, so it never heard the opening event")
			return deleteStagedRow(ctx, deps, row, tx)
		}
		if deps.LifecycleSubs == nil {
			return errLifecycleSubsNotInitialized
		}
		s, ok := deps.LifecycleSubs.Get(row.ClaimProducerName)
		if !ok {
			if deps.Logger != nil {
				deps.Logger.Warn("lifecycle.unknown_subscriber_dropped",
					"peer", row.ClaimProducerName,
					"scope_kind", string(row.ScopeKind),
					"scope_id", row.ScopeID,
					"event", row.Event)
			}
			if err := deps.Persist.LifecycleIdempotency().Delete(ctx,
				row.ClaimProducerName, row.ScopeKind, row.ScopeID, tx); err != nil {
				return fmt.Errorf("reclaim lifecycle row for unregistered peer %q: %w", row.ClaimProducerName, err)
			}
			return deleteStagedRow(ctx, deps, row, tx)
		}
		var dispatchErr error
		if row.ScopeKind == persistence.LifecycleIdempotencyScopeInstance {
			dispatchErr = dispatchInstanceEvent(ctx, s, event, payload.TemplateHash, payload.InstanceID, instancePayloadOrZero(payload))
		} else {
			dispatchErr = dispatchTemplateEvent(ctx, s, event, payload.TemplateHash, templatePayloadOrZero(payload))
		}
		if dispatchErr != nil {
			return fmt.Errorf("peer %q: %w", row.ClaimProducerName, dispatchErr)
		}
		logStagedDeliveryDelivered(deps, row, ledgerDispositionFor(deletesLedgerRow, target))
		if deletesLedgerRow {
			if err := deps.Persist.LifecycleIdempotency().Delete(ctx, row.ClaimProducerName, row.ScopeKind, row.ScopeID, tx); err != nil {
				return fmt.Errorf("delete lifecycle row %q: %w", row.ClaimProducerName, err)
			}
			return deleteStagedRow(ctx, deps, row, tx)
		}
		if err := deps.Persist.LifecycleIdempotency().Upsert(ctx, persistence.LifecycleIdempotencyRow{
			ClaimProducerName: row.ClaimProducerName,
			ScopeKind:         row.ScopeKind,
			ScopeID:           row.ScopeID,
			State:             target,
		}, tx); err != nil {
			return fmt.Errorf("upsert lifecycle row %q: %w", row.ClaimProducerName, err)
		}
		return deleteStagedRow(ctx, deps, row, tx)
	}, nil)
}

const ledgerDispositionDeleted = "deleted"

func ledgerDispositionFor(deletesLedgerRow bool, target persistence.LifecycleIdempotencyState) string {
	if deletesLedgerRow {
		return ledgerDispositionDeleted
	}
	return string(target)
}

// @decision: lifecycle-subscriber-at-least-once-delivery
func logStagedDeliveryDelivered(deps AppDeps, row persistence.LifecycleOutboxRow, ledgerDisposition string) {
	if deps.Logger == nil {
		return
	}
	deps.Logger.Info("lifecycle.staged_delivered",
		"peer", row.ClaimProducerName,
		"scope_kind", string(row.ScopeKind),
		"scope_id", row.ScopeID,
		"event", row.Event,
		"ledger_disposition", ledgerDisposition)
}

// @decision: lifecycle-subscriber-at-least-once-delivery
func logStagedDeliverySkipped(deps AppDeps, row persistence.LifecycleOutboxRow, reason string) {
	if deps.Logger == nil {
		return
	}
	deps.Logger.Warn("lifecycle.staged_skipped",
		"peer", row.ClaimProducerName,
		"scope_kind", string(row.ScopeKind),
		"scope_id", row.ScopeID,
		"event", row.Event,
		"reason", reason)
}

func deleteStagedRow(ctx context.Context, deps AppDeps, row persistence.LifecycleOutboxRow, tx persistence.Tx) error {
	if err := deps.Persist.LifecycleOutbox().DeleteBySeq(ctx, row.Seq, tx); err != nil {
		return fmt.Errorf("delete staged lifecycle delivery %s for peer %q: %w", row.Event, row.ClaimProducerName, err)
	}
	return nil
}

func dropUndeliverableStagedRow(ctx context.Context, deps AppDeps, row persistence.LifecycleOutboxRow, reason string) error {
	if deps.Logger != nil {
		deps.Logger.Warn("lifecycle.staged_delivery_dropped",
			"peer", row.ClaimProducerName,
			"scope_kind", string(row.ScopeKind),
			"scope_id", row.ScopeID,
			"event", row.Event,
			"reason", reason)
	}
	return deps.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return deleteStagedRow(ctx, deps, row, tx)
	})
}

func templatePayloadOrZero(p stagedLifecyclePayload) TemplatePayload {
	if p.Template == nil {
		return TemplatePayload{}
	}
	return *p.Template
}

func instancePayloadOrZero(p stagedLifecyclePayload) InstancePayload {
	if p.Instance == nil {
		return InstancePayload{}
	}
	return *p.Instance
}

// @decision: lifecycle-fanout-after-commit
func deliverStagedLifecycleAfterCommit(
	ctx context.Context,
	deps AppDeps,
	scopeKind persistence.LifecycleIdempotencyScopeKind,
	scopeID string,
	kv ...any,
) {
	perPeerErr, err := deliverStagedLifecycleScope(ctx, deps, scopeKind, scopeID)
	logFanOutFailures(deps, "lifecycle.fanout_delivery_failed", perPeerErr, err, kv...)
}

// @decision: lifecycle-fanout-after-commit
func stageInstanceCreated(
	ctx context.Context,
	deps AppDeps,
	templateHash string,
	spec node.TemplateSpec,
	resp createInstanceResponse,
	params map[string]any,
	bindings json.RawMessage,
	owner *shared.UUID,
	targetRoutingIdentity string,
	tx persistence.Tx,
) error {
	paramsBytes, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("marshal instance params for %s: %w", resp.InstanceID, err)
	}
	instanceKey := ""
	if resp.InstanceKey != nil {
		instanceKey = *resp.InstanceKey
	}
	return StageInstanceLifecycleEvent(ctx, deps, EventInstanceCreated, templateHash, resp.InstanceID, spec,
		InstancePayload{
			InstanceKey:           instanceKey,
			Params:                paramsBytes,
			ServiceBindings:       bindings,
			OwnerAPIKeyID:         owner,
			TargetRoutingIdentity: targetRoutingIdentity,
		}, tx)
}
