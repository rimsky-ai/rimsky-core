// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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

const terminatorFailureLogEvery = 10

type InstanceTerminator struct {
	deps         AppDeps
	pollInterval time.Duration
	logger       shared.Logger

	mu       sync.Mutex
	stop     chan struct{}
	stopOnce sync.Once
	done     chan struct{}
	started  bool

	failMu    sync.Mutex
	failCount map[shared.UUID]int
}

func NewInstanceTerminator(deps AppDeps, pollInterval time.Duration) *InstanceTerminator {
	if pollInterval <= 0 {
		pollInterval = 2 * time.Second
	}
	logger := deps.Logger
	if logger == nil {
		logger = shared.SilentLogger{}
	}
	return &InstanceTerminator{
		deps:         deps,
		pollInterval: pollInterval,
		logger:       logger,
		stop:         make(chan struct{}),
		done:         make(chan struct{}),
	}
}

func (t *InstanceTerminator) Run(ctx context.Context) {
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

func (t *InstanceTerminator) Stop() {
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

func (t *InstanceTerminator) tick(ctx context.Context) {
	tickCtx, cancel := context.WithTimeout(ctx, tickBudget)
	defer cancel()

	const batch = 100
	var rows []persistence.InstanceRow
	if err := t.deps.Persist.Transaction(tickCtx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := t.deps.Persist.Instances().ListTerminatedWithLifecycleRows(ctx, batch, tx)
		rows = r
		return err
	}); err != nil {
		t.logger.Warn("instance_terminator.list_failed", "error", err.Error())
		return
	}
	for _, inst := range rows {
		var tpl *persistence.TemplateRow
		if err := t.deps.Persist.Transaction(tickCtx, func(ctx context.Context, tx persistence.Tx) error {
			r, err := t.deps.Persist.Templates().GetByHash(ctx, inst.TemplateHash, tx)
			tpl = r
			return err
		}); err != nil {
			if n, log := t.recordFailure(inst.ID); log {
				t.logger.Warn("instance_terminator.template_lookup_failed",
					"instance_id", inst.ID,
					"template_hash", inst.TemplateHash,
					"error", err.Error(),
					"consecutive_failures", n)
			}
			continue
		}
		if tpl == nil {
			if err := fanOutInstanceTerminatedFromLifecycleRows(tickCtx, t.deps, inst, "instance_terminated"); err != nil {
				if n, log := t.recordFailure(inst.ID); log {
					t.logger.Warn("instance_terminator.fallback_fanout_failed",
						"instance_id", inst.ID,
						"template_hash", inst.TemplateHash,
						"error", err.Error(),
						"consecutive_failures", n)
				}
				continue
			}
			t.clearFailure(inst.ID)
			continue
		}
		var terminatedAtMs int64
		if inst.TerminatedAt != nil {
			terminatedAtMs = inst.TerminatedAt.UnixMilli()
		}
		if err := CloseAndFanOutRunScopesForInstance(tickCtx, t.deps, tpl.Spec, inst.ID, "instance_terminated"); err != nil {
			if n, log := t.recordFailure(inst.ID); log {
				t.logger.Warn("instance_terminator.run_scope_fanout_failed",
					"instance_id", inst.ID,
					"error", err.Error(),
					"consecutive_failures", n)
			}
			continue
		}
		_, perStoreErr, err := FanOutInstanceEvent(tickCtx, t.deps,
			EventInstanceTerminated, inst.TemplateHash, inst.ID.String(), tpl.Spec,
			InstancePayload{TerminatedAtUnixMs: terminatedAtMs}, nil)
		if err != nil {
			if n, log := t.recordFailure(inst.ID); log {
				t.logger.Warn("instance_terminator.fanout_partial_failure",
					"instance_id", inst.ID,
					"per_store_error", perStoreErr,
					"consecutive_failures", n)
			}
			continue
		}
		t.clearFailure(inst.ID)
	}
}

func (t *InstanceTerminator) recordFailure(id shared.UUID) (attempt int, shouldLog bool) {
	t.failMu.Lock()
	defer t.failMu.Unlock()
	if t.failCount == nil {
		t.failCount = make(map[shared.UUID]int)
	}
	t.failCount[id]++
	n := t.failCount[id]
	return n, n == 1 || n%terminatorFailureLogEvery == 0
}

func (t *InstanceTerminator) clearFailure(id shared.UUID) {
	t.failMu.Lock()
	delete(t.failCount, id)
	t.failMu.Unlock()
}
