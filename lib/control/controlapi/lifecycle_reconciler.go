// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package controlapi

import (
	"context"
	"sync"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

const tickBudget = 10 * time.Second

const stopBudget = 5 * time.Second

const reconcilerFailureLogEvery = 10

type LifecycleReconciler struct {
	deps         AppDeps
	pollInterval time.Duration
	logger       shared.Logger

	mu       sync.Mutex
	stop     chan struct{}
	stopOnce sync.Once
	done     chan struct{}
	started  bool

	failMu    sync.Mutex
	failCount map[string]int
}

func NewLifecycleReconciler(deps AppDeps, pollInterval time.Duration) *LifecycleReconciler {
	if pollInterval <= 0 {
		pollInterval = 2 * time.Second
	}
	logger := deps.Logger
	if logger == nil {
		logger = shared.SilentLogger{}
	}
	return &LifecycleReconciler{
		deps:         deps,
		pollInterval: pollInterval,
		logger:       logger,
		stop:         make(chan struct{}),
		done:         make(chan struct{}),
	}
}

func (t *LifecycleReconciler) Run(ctx context.Context) {
	t.mu.Lock()
	if t.started {
		t.mu.Unlock()
		return
	}
	t.started = true
	t.mu.Unlock()
	defer close(t.done)
	ticker := time.NewTicker(t.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.stop:
			return
		case <-ticker.C:
			t.tick(ctx)
		}
	}
}

func (t *LifecycleReconciler) Stop() {
	t.mu.Lock()
	started := t.started
	t.mu.Unlock()
	if !started {
		return
	}
	t.stopOnce.Do(func() { close(t.stop) })
	select {
	case <-t.done:
	case <-time.After(stopBudget):
	}
}

// @decision: lifecycle-subscriber-at-least-once-delivery
func (t *LifecycleReconciler) tick(ctx context.Context) {
	tickCtx, cancel := context.WithTimeout(ctx, tickBudget)
	defer cancel()

	t.drainStagedLifecycleDeliveries(tickCtx)
	t.drainTerminatedInstances(tickCtx)
}

// @concept: lifecycle-subscriber
func (t *LifecycleReconciler) drainTerminatedInstances(tickCtx context.Context) {
	const batch = 100
	var rows []persistence.InstanceRow
	if err := t.deps.Persist.Transaction(tickCtx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := t.deps.Persist.Instances().ListTerminatedWithLifecycleRows(ctx, batch, tx)
		rows = r
		return err
	}); err != nil {
		if n, log := t.recordFailure("terminated_instances"); log {
			t.logger.Warn("lifecycle_reconciler.list_failed", "error", err.Error(), "consecutive_failures", n)
		}
		return
	}
	t.clearFailure("terminated_instances")
	for _, inst := range rows {
		var tpl *persistence.TemplateRow
		if err := t.deps.Persist.Transaction(tickCtx, func(ctx context.Context, tx persistence.Tx) error {
			r, err := t.deps.Persist.Templates().GetByHash(ctx, inst.TemplateHash, tx)
			tpl = r
			return err
		}); err != nil {
			if n, log := t.recordFailure(inst.ID.String()); log {
				t.logger.Warn("lifecycle_reconciler.template_lookup_failed",
					"instance_id", inst.ID,
					"template_hash", inst.TemplateHash,
					"error", err.Error(),
					"consecutive_failures", n)
			}
			continue
		}
		if tpl == nil {
			if err := fanOutInstanceTerminatedFromLifecycleRows(tickCtx, t.deps, inst, "instance_terminated"); err != nil {
				if n, log := t.recordFailure(inst.ID.String()); log {
					t.logger.Warn("lifecycle_reconciler.fallback_fanout_failed",
						"instance_id", inst.ID,
						"template_hash", inst.TemplateHash,
						"error", err.Error(),
						"consecutive_failures", n)
				}
				continue
			}
			t.clearFailure(inst.ID.String())
			continue
		}
		var terminatedAtMs int64
		if inst.TerminatedAt != nil {
			terminatedAtMs = inst.TerminatedAt.UnixMilli()
		}
		if err := CloseAndFanOutRunScopesForInstance(tickCtx, t.deps, tpl.Spec, inst.ID, "instance_terminated"); err != nil {
			if n, log := t.recordFailure(inst.ID.String()); log {
				t.logger.Warn("lifecycle_reconciler.run_scope_fanout_failed",
					"instance_id", inst.ID,
					"error", err.Error(),
					"consecutive_failures", n)
			}
			continue
		}
		_, perStoreErr, err := FanOutInstanceEvent(tickCtx, t.deps,
			EventInstanceTerminated, inst.TemplateHash, inst.ID.String(), tpl.Spec,
			InstancePayload{TerminatedAtUnixMs: terminatedAtMs}, nil)
		if err != nil || len(perStoreErr) > 0 {
			if n, log := t.recordFailure(inst.ID.String()); log {
				t.logger.Warn("lifecycle_reconciler.fanout_partial_failure",
					"instance_id", inst.ID,
					"per_store_error", perStoreErr,
					"consecutive_failures", n)
			}
			continue
		}
		t.clearFailure(inst.ID.String())
	}
}

func (t *LifecycleReconciler) recordFailure(key string) (attempt int, shouldLog bool) {
	t.failMu.Lock()
	defer t.failMu.Unlock()
	if t.failCount == nil {
		t.failCount = make(map[string]int)
	}
	t.failCount[key]++
	n := t.failCount[key]
	return n, n == 1 || n%reconcilerFailureLogEvery == 0
}

func (t *LifecycleReconciler) clearFailure(key string) {
	t.failMu.Lock()
	delete(t.failCount, key)
	t.failMu.Unlock()
}
