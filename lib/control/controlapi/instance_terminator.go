// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package controlapi

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

const tickBudget = 10 * time.Second

const stopBudget = 5 * time.Second

type InstanceTerminator struct {
	deps         AppDeps
	pollInterval time.Duration
	logger       *slog.Logger

	mu      sync.Mutex
	stop    chan struct{}
	done    chan struct{}
	started bool
}

func NewInstanceTerminator(deps AppDeps, pollInterval time.Duration) *InstanceTerminator {
	if pollInterval <= 0 {
		pollInterval = 2 * time.Second
	}
	return &InstanceTerminator{
		deps:         deps,
		pollInterval: pollInterval,
		logger:       slog.Default(),
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
	if !t.started {
		t.mu.Unlock()
		return
	}
	t.mu.Unlock()
	select {
	case <-t.stop:
	default:
		close(t.stop)
	}
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
			t.logger.Warn("instance_terminator.template_lookup_failed",
				"instance_id", inst.ID,
				"template_hash", inst.TemplateHash,
				"error", err.Error())
			continue
		}
		if tpl == nil {
			if err := fanOutInstanceTerminatedFromLifecycleRows(tickCtx, t.deps, inst, "instance_terminated"); err != nil {
				t.logger.Warn("instance_terminator.fallback_fanout_failed",
					"instance_id", inst.ID,
					"template_hash", inst.TemplateHash,
					"error", err.Error())
			}
			continue
		}
		var terminatedAtMs int64
		if inst.TerminatedAt != nil {
			terminatedAtMs = inst.TerminatedAt.UnixMilli()
		}
		if err := CloseAndFanOutRunScopesForInstance(tickCtx, t.deps, tpl.Spec, inst.ID, "instance_terminated"); err != nil {
			t.logger.Warn("instance_terminator.run_scope_fanout_failed",
				"instance_id", inst.ID,
				"error", err.Error())
			continue
		}
		_, perStoreErr, err := FanOutInstanceEvent(tickCtx, t.deps,
			EventInstanceTerminated, inst.TemplateHash, inst.ID.String(), tpl.Spec,
			InstancePayload{TerminatedAtUnixMs: terminatedAtMs}, nil)
		if err != nil {
			t.logger.Warn("instance_terminator.fanout_partial_failure",
				"instance_id", inst.ID,
				"per_store_error", perStoreErr)
			continue
		}
	}
}
