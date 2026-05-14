// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// instance_terminator.go — background worker that polls
// rimsky_instances rows with terminated_at IS NOT NULL and outstanding
// rimsky_lifecycle_idempotencies rows, then fires OnInstanceTerminated to
// settle the per-store bookkeeping. Per docs/specs/2026-05-01-control-
// plane-and-store-lifecycle-design.md §2.4 and §5.5.
package controlapi

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/fallguy/rimsky/foundation/locks"
	"github.com/fallguy/rimsky/foundation/persistence"
)

// tickBudget bounds a single terminator iteration so a wedged store
// RPC cannot block process shutdown forever. Chosen to be a few times
// the normal RPC round-trip without dwarfing the default poll interval.
const tickBudget = 10 * time.Second

// stopBudget bounds Stop's wait for an in-flight tick to drain. After
// the budget elapses we let the goroutine leak rather than blocking
// process shutdown indefinitely; the OS reclaims the goroutine on exit.
const stopBudget = 5 * time.Second

// errStoresRegistryUnset signals a misconfigured terminator (no store
// registry on AppDeps). Surfaced via the lifecycle-fallback path so
// operators see it in the warn log; the next tick re-tries.
var errStoresRegistryUnset = errors.New("terminator: store registry not initialized")

// InstanceTerminator polls for terminated instances that still have
// rimsky_lifecycle_idempotencies rows at scope='instance' and fires
// OnInstanceTerminated to the relevant stores. At-least-once delivery;
// re-fire is OK because store handlers are idempotent.
type InstanceTerminator struct {
	deps         AppDeps
	pollInterval time.Duration
	logger       *slog.Logger

	mu      sync.Mutex
	stop    chan struct{}
	done    chan struct{}
	started bool
}

// NewInstanceTerminator wires a terminator against the supplied deps.
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

// Run drives the polling loop until ctx is cancelled or Stop is called.
// Safe to call once per instance; subsequent calls return immediately.
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

// Stop signals the loop to exit and waits up to stopBudget for the
// goroutine to drain. After the budget elapses we return without
// blocking; the goroutine is then leaked but cannot block process
// shutdown. The runtime reclaims it at exit.
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
		// After 5s the goroutine is left running; the OS reclaims it
		// at process exit. The leak is not bounded by the runtime —
		// only by process lifetime.
	}
}

// tick is the per-iteration body. Pulled out as a method so tests can
// drive it directly. Each tick is bounded by tickBudget so a wedged
// store-RPC cannot block shutdown forever.
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
			// Template gone (force-deleted, etc). Fall back to firing
			// OnInstanceTerminated against every store named in the
			// recorded lifecycle rows so per-instance state in stores
			// is still settled, then drop the lifecycle rows directly.
			if err := t.fanOutFromLifecycleRows(tickCtx, inst); err != nil {
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
		_, perStoreErr, err := FanOutInstanceEvent(tickCtx, t.deps,
			EventInstanceTerminated, inst.TemplateHash, inst.ID.String(), tpl.Spec,
			InstancePayload{TerminatedAtUnixMs: terminatedAtMs}, nil)
		if err != nil {
			// Per-store failure: log and try again next tick. The
			// FanOut helper has already deleted lifecycle rows for the
			// stores that succeeded before the failure point.
			t.logger.Warn("instance_terminator.fanout_partial_failure",
				"instance_id", inst.ID,
				"per_store_error", perStoreErr)
			continue
		}
	}
}

// fanOutFromLifecycleRows is the template-missing fallback. Iterates the
// recorded rimsky_lifecycle_idempotencies rows for the instance, calls
// OnInstanceTerminated on each registered store, then deletes the row.
// Rows whose store is no longer in the registry are skipped with a
// warning so the operator can either re-introduce the store or delete
// the bookkeeping row by hand.
func (t *InstanceTerminator) fanOutFromLifecycleRows(ctx context.Context, inst persistence.InstanceRow) error {
	var rows []persistence.LifecycleIdempotencyRow
	if err := t.deps.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := t.deps.Persist.LifecycleIdempotency().ListByScope(ctx,
			persistence.LifecycleIdempotencyScopeInstance, inst.ID.String(), tx)
		rows = r
		return err
	}); err != nil {
		return err
	}
	if t.deps.LifecycleSubs == nil {
		return errStoresRegistryUnset
	}
	for _, r := range rows {
		s, ok := t.deps.LifecycleSubs.Get(r.StoreRegistrationName)
		if !ok {
			t.logger.Warn("instance_terminator.unknown_lifecycle_subscriber",
				"instance_id", inst.ID,
				"peer_name", r.StoreRegistrationName)
			continue
		}
		var terminatedAtMs int64
		if inst.TerminatedAt != nil {
			terminatedAtMs = inst.TerminatedAt.UnixMilli()
		}
		if err := s.OnInstanceTerminated(ctx, locks.OnInstanceTerminatedRequest{
			InstanceID:         inst.ID.String(),
			TemplateHash:       inst.TemplateHash,
			TerminatedAtUnixMs: terminatedAtMs,
		}); err != nil {
			return err
		}
		if err := t.deps.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			return t.deps.Persist.LifecycleIdempotency().Delete(ctx,
				r.StoreRegistrationName,
				persistence.LifecycleIdempotencyScopeInstance,
				inst.ID.String(), tx)
		}); err != nil {
			return err
		}
	}
	return nil
}
