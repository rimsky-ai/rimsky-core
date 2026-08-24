// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @decision: lifecycle-subscriber-at-least-once-delivery
// @decision: lifecycle-fanout-after-commit
// @concept: lifecycle-subscriber

package runtime

import (
	"context"
	"errors"
	"fmt"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/lifecycle"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

type LifecycleDeliveryDeps struct {
	Persist        persistence.Tables
	AdvisoryLocker persistence.AdvisoryLocker
	Subscribers    *lifecycle.Registry
	Logger         shared.Logger
}

var (
	ErrLifecycleSubscribersNotInitialized = errors.New("lifecycle subscriber registry not initialized")
	ErrLifecycleAdvisoryLockerMissing     = errors.New("advisory locker not initialized")
)

// @concept: advisory-lock
func WithLifecycleScopeTx(
	ctx context.Context,
	persist persistence.Tables,
	advLock persistence.AdvisoryLocker,
	scopeKind persistence.LifecycleScopeKind,
	scopeID string,
	fn func(ctx context.Context, tx persistence.Tx) error,
	tx persistence.Tx,
) error {
	if advLock == nil {
		return ErrLifecycleAdvisoryLockerMissing
	}
	run := func(ctx context.Context, useTx persistence.Tx) error {
		if err := advLock.TakeLifecycleScopeLock(ctx, scopeKind, scopeID, useTx); err != nil {
			return fmt.Errorf("lifecycle scope lock %s/%s: %w", scopeKind, scopeID, err)
		}
		return fn(ctx, useTx)
	}
	if tx != nil {
		return run(ctx, tx)
	}
	return persist.Transaction(ctx, run)
}

// @decision: lifecycle-fanout-after-commit
func DeliverStagedLifecycleScope(
	ctx context.Context,
	deps LifecycleDeliveryDeps,
	scopeKind persistence.LifecycleScopeKind,
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
	service   string
	scopeKind persistence.LifecycleScopeKind
	scopeID   string
}

func streamOf(row persistence.LifecycleOutboxRow) stagedDeliveryStream {
	return stagedDeliveryStream{service: row.ClaimProducerName, scopeKind: row.ScopeKind, scopeID: row.ScopeID}
}

// @decision: lifecycle-subscriber-at-least-once-delivery
func deliverStagedLifecycleRows(
	ctx context.Context, deps LifecycleDeliveryDeps, rows []persistence.LifecycleOutboxRow,
) map[string]error {
	perServiceErr := map[string]error{}
	blocked := map[stagedDeliveryStream]struct{}{}
	for _, row := range rows {
		stream := streamOf(row)
		if _, held := blocked[stream]; held {
			continue
		}
		if err := DeliverStagedLifecycleRow(ctx, deps, row); err != nil {
			blocked[stream] = struct{}{}
			perServiceErr[row.ClaimProducerName] = err
		}
	}
	return perServiceErr
}

// @decision: lifecycle-subscriber-at-least-once-delivery
// @concept: lifecycle-subscriber
func DeliverStagedLifecycleRow(ctx context.Context, deps LifecycleDeliveryDeps, row persistence.LifecycleOutboxRow) error {
	event, ok := lifecycle.ParseEvent(row.Event)
	if !ok {
		return dropUndeliverableStagedRow(ctx, deps, row, "unknown_event")
	}
	payload, err := lifecycle.DecodeStagedPayload(row.Payload)
	if err != nil {
		return dropUndeliverableStagedRow(ctx, deps, row, "undecodable_payload")
	}
	if deps.Subscribers == nil {
		return ErrLifecycleSubscribersNotInitialized
	}

	// @concept: advisory-lock
	return WithLifecycleScopeTx(ctx, deps.Persist, deps.AdvisoryLocker, row.ScopeKind, row.ScopeID,
		func(ctx context.Context, tx persistence.Tx) error {
			still, err := deps.Persist.LifecycleOutbox().GetBySeq(ctx, row.Seq, tx)
			if err != nil {
				return fmt.Errorf("re-read staged lifecycle delivery %d: %w", row.Seq, err)
			}
			if still == nil {
				return nil
			}
			s, ok := deps.Subscribers.Get(row.ClaimProducerName)
			if !ok {
				logStagedDeliverySkipped(deps, row, "no service of this name is registered here")
				return deleteStagedRow(ctx, deps, row, tx)
			}
			if err := lifecycle.DispatchEvent(ctx, s, event, payload); err != nil {
				return fmt.Errorf("service %q: %w", row.ClaimProducerName, err)
			}
			logStagedDeliveryDelivered(deps, row)
			return deleteStagedRow(ctx, deps, row, tx)
		}, nil)
}

// @decision: lifecycle-subscriber-at-least-once-delivery
func logStagedDeliveryDelivered(deps LifecycleDeliveryDeps, row persistence.LifecycleOutboxRow) {
	if deps.Logger == nil {
		return
	}
	deps.Logger.Info("LIFECYCLE.STAGEDDELIVERY.DELIVERED",
		"service", row.ClaimProducerName,
		"scope_kind", string(row.ScopeKind),
		"scope_id", row.ScopeID,
		"event", row.Event)
}

// @decision: lifecycle-subscriber-at-least-once-delivery
func logStagedDeliverySkipped(deps LifecycleDeliveryDeps, row persistence.LifecycleOutboxRow, reason string) {
	if deps.Logger == nil {
		return
	}
	deps.Logger.Warn("LIFECYCLE.STAGEDDELIVERY.SKIPPED",
		"service", row.ClaimProducerName,
		"scope_kind", string(row.ScopeKind),
		"scope_id", row.ScopeID,
		"event", row.Event,
		"reason", reason)
}

func deleteStagedRow(ctx context.Context, deps LifecycleDeliveryDeps, row persistence.LifecycleOutboxRow, tx persistence.Tx) error {
	if err := deps.Persist.LifecycleOutbox().DeleteBySeq(ctx, row.Seq, tx); err != nil {
		return fmt.Errorf("delete staged lifecycle delivery %s for service %q: %w", row.Event, row.ClaimProducerName, err)
	}
	return nil
}

func dropUndeliverableStagedRow(
	ctx context.Context, deps LifecycleDeliveryDeps, row persistence.LifecycleOutboxRow, reason string,
) error {
	if deps.Logger != nil {
		deps.Logger.Warn("LIFECYCLE.STAGEDDELIVERY.DROPPED",
			"service", row.ClaimProducerName,
			"scope_kind", string(row.ScopeKind),
			"scope_id", row.ScopeID,
			"event", row.Event,
			"reason", reason)
	}
	return deps.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return deleteStagedRow(ctx, deps, row, tx)
	})
}
