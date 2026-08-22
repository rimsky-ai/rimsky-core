// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @decision: lifecycle-subscriber-at-least-once-delivery
// @decision: lifecycle-fanout-after-commit
// @concept: lifecycle-subscriber

package controlapi

import (
	"context"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

const reconcileBatch = 100

// @decision: lifecycle-subscriber-at-least-once-delivery
func (t *LifecycleReconciler) drainStagedLifecycleDeliveries(ctx context.Context) {
	blocked := map[stagedDeliveryStream]struct{}{}
	for delivered := 0; delivered < reconcileBatch; {
		rows, ok := t.listOldestStagedRowPerStream(ctx)
		if !ok {
			return
		}
		before := delivered
		for _, row := range rows {
			stream := stagedDeliveryStream{peer: row.ClaimProducerName, scopeKind: row.ScopeKind, scopeID: row.ScopeID}
			if _, held := blocked[stream]; held {
				continue
			}
			delivered++
			key := string(row.ScopeKind) + "/" + row.ScopeID + "/" + row.ClaimProducerName
			if err := deliverStagedLifecycleRow(ctx, t.deps, row); err != nil {
				blocked[stream] = struct{}{}
				if n, log := t.recordFailure(key); log {
					t.logger.Warn("lifecycle_reconciler.staged_delivery_failed",
						"peer", row.ClaimProducerName,
						"scope_kind", string(row.ScopeKind),
						"scope_id", row.ScopeID,
						"event", row.Event,
						"error", err.Error(),
						"consecutive_failures", n)
				}
				continue
			}
			t.clearFailure(key)
		}
		if delivered == before {
			return
		}
	}
}

// @decision: lifecycle-subscriber-at-least-once-delivery
func (t *LifecycleReconciler) listOldestStagedRowPerStream(ctx context.Context) ([]persistence.LifecycleOutboxRow, bool) {
	var rows []persistence.LifecycleOutboxRow
	if err := t.deps.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := t.deps.Persist.LifecycleOutbox().ListOldestPendingPerStream(ctx, reconcileBatch, tx)
		rows = r
		return err
	}); err != nil {
		if n, log := t.recordFailure("lifecycle_outbox"); log {
			t.logger.Warn("lifecycle_reconciler.staged_delivery_list_failed",
				"error", err.Error(), "consecutive_failures", n)
		}
		return nil, false
	}
	t.clearFailure("lifecycle_outbox")
	return rows, true
}
