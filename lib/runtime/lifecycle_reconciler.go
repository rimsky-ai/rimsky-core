// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @decision: lifecycle-subscriber-at-least-once-delivery
// @decision: lifecycle-drain-per-role
// @concept: lifecycle-subscriber

package runtime

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/lifecycle"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

const (
	DefaultLifecycleReconcileInterval = 2 * time.Second

	// @decision: service-delivery-stall-signal
	DefaultServiceDeliveryStallAfter = 5 * time.Minute
)

const (
	lifecycleDrainBudget     = 10 * time.Second
	lifecycleStopBudget      = 5 * time.Second
	lifecycleDrainBatch      = 100
	lifecycleFailureLogEvery = 10
)

type LifecycleReconcilerConfig struct {
	Persist        persistence.Tables
	AdvisoryLocker persistence.AdvisoryLocker
	Subscribers    *lifecycle.Registry
	Clock          shared.Clock
	Logger         shared.Logger

	Interval time.Duration

	// @decision: service-delivery-stall-signal
	StallAfter time.Duration
}

// @decision: lifecycle-drain-per-role
type LifecycleReconciler struct {
	deps       LifecycleDeliveryDeps
	clock      shared.Clock
	logger     shared.Logger
	interval   time.Duration
	stallAfter time.Duration

	// @decision: service-delivery-stall-signal
	health *ServiceDeliveryHealth

	kick chan struct{}

	passes atomic.Uint64

	mu       sync.Mutex
	stop     chan struct{}
	stopOnce sync.Once
	done     chan struct{}
	started  bool

	failMu    sync.Mutex
	failCount map[string]int
}

func NewLifecycleReconciler(cfg LifecycleReconcilerConfig) *LifecycleReconciler {
	if cfg.Interval <= 0 {
		cfg.Interval = DefaultLifecycleReconcileInterval
	}
	if cfg.StallAfter <= 0 {
		cfg.StallAfter = DefaultServiceDeliveryStallAfter
	}
	if cfg.Clock == nil {
		cfg.Clock = shared.SystemClock{}
	}
	if cfg.Logger == nil {
		cfg.Logger = shared.SilentLogger{}
	}
	return &LifecycleReconciler{
		deps: LifecycleDeliveryDeps{
			Persist:        cfg.Persist,
			AdvisoryLocker: cfg.AdvisoryLocker,
			Subscribers:    cfg.Subscribers,
			Logger:         cfg.Logger,
		},
		clock:      cfg.Clock,
		logger:     cfg.Logger,
		interval:   cfg.Interval,
		stallAfter: cfg.StallAfter,
		health: NewServiceDeliveryHealth(cfg.Persist, persistence.ServiceDeliveryOutboxLifecycle,
			cfg.StallAfter, cfg.Logger),
		kick: make(chan struct{}, 1),
		stop: make(chan struct{}),
		done: make(chan struct{}),
	}
}

// @decision: lifecycle-drain-per-role
func (r *LifecycleReconciler) Kick() {
	if r == nil {
		return
	}
	select {
	case r.kick <- struct{}{}:
	default:
	}
}

func (r *LifecycleReconciler) Run(ctx context.Context) {
	r.mu.Lock()
	if r.started {
		r.mu.Unlock()
		return
	}
	r.started = true
	r.mu.Unlock()
	defer close(r.done)
	for {
		if ctx.Err() != nil {
			return
		}
		r.drainPass(ctx)
		r.passes.Add(1)
		if !r.waitForNextPass(ctx) {
			return
		}
	}
}

// @decision: polling-audit
func (r *LifecycleReconciler) PassesCompleted() uint64 {
	return r.passes.Load()
}

// @decision: lifecycle-drain-per-role
func (r *LifecycleReconciler) waitForNextPass(ctx context.Context) bool {
	sleepCtx, cancelSleep := context.WithCancel(ctx)
	defer cancelSleep()
	slept := make(chan struct{})
	go func() {
		_ = r.clock.Sleep(sleepCtx, r.interval)
		close(slept)
	}()
	select {
	case <-ctx.Done():
		return false
	case <-r.stop:
		return false
	case <-r.kick:
		return true
	case <-slept:
		return true
	}
}

func (r *LifecycleReconciler) Stop() {
	r.mu.Lock()
	started := r.started
	r.mu.Unlock()
	if !started {
		return
	}
	r.stopOnce.Do(func() { close(r.stop) })
	select {
	case <-r.done:
	case <-time.After(lifecycleStopBudget):
	}
}

func (r *LifecycleReconciler) drainPass(ctx context.Context) {
	drainCtx, cancel := context.WithTimeout(ctx, lifecycleDrainBudget)
	defer cancel()
	r.DrainOnce(drainCtx)
}

// @decision: lifecycle-subscriber-at-least-once-delivery
func (r *LifecycleReconciler) DrainOnce(ctx context.Context) int {
	blocked := map[stagedDeliveryStream]struct{}{}
	attempted := 0
	for attempted < lifecycleDrainBatch {
		now := r.clock.Now()
		rows, ok := r.listOldestPendingPerStream(ctx, now)
		if !ok || len(rows) == 0 {
			break
		}
		before := attempted
		for _, row := range rows {
			stream := streamOf(row)
			if _, held := blocked[stream]; held {
				continue
			}
			attempted++
			key := streamKey(row)
			if err := DeliverStagedLifecycleRow(ctx, r.deps, row); err != nil {
				blocked[stream] = struct{}{}
				r.recordDeliveryFailure(ctx, row, err, now)
				continue
			}
			r.clearFailure(key)
		}
		if attempted == before {
			break
		}
	}
	// @decision: service-delivery-stall-signal
	r.observeStalls(ctx)
	return attempted
}

// @decision: service-delivery-stall-signal
func (r *LifecycleReconciler) observeStalls(ctx context.Context) {
	var pending []persistence.ServiceOutboxPending
	if err := r.deps.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		out, err := r.deps.Persist.LifecycleOutbox().PendingSummaryByService(ctx, tx)
		pending = out
		return err
	}); err != nil {
		if n, log := r.recordFailure("lifecycle_outbox_pending_summary"); log {
			r.logger.Warn("LIFECYCLE.PENDINGSUMMARY.UNREAD",
				"error", err.Error(), "consecutive_failures", n)
		}
		return
	}
	r.clearFailure("lifecycle_outbox_pending_summary")
	r.health.ObservePending(ctx, pending, r.clock.Now())
}

func streamKey(row persistence.LifecycleOutboxRow) string {
	return string(row.ScopeKind) + "/" + row.ScopeID + "/" + row.ClaimProducerName
}

// @decision: service-delivery-stall-signal
func (r *LifecycleReconciler) recordDeliveryFailure(
	ctx context.Context, row persistence.LifecycleOutboxRow, deliveryErr error, now time.Time,
) {
	backoff := outboxRetryBackoff(row.AttemptCount+1, r.interval, r.stallAfter)
	nextAttemptAt := now.Add(backoff)
	if err := r.deps.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return r.deps.Persist.LifecycleOutbox().RecordAttempt(ctx, row.Seq, nextAttemptAt, deliveryErr.Error(), tx)
	}); err != nil {
		r.logger.Warn("LIFECYCLE.ATTEMPT.UNRECORDED",
			"seq", row.Seq,
			"service", row.ClaimProducerName,
			"error", err.Error(),
			"consequence", "the row keeps its previous next-attempt time, so the next pass retries it earlier than the backoff asks")
	}
	if n, log := r.recordFailure(streamKey(row)); log {
		r.logger.Warn("LIFECYCLE.DELIVERY.RESCHEDULED",
			"service", row.ClaimProducerName,
			"scope_kind", string(row.ScopeKind),
			"scope_id", row.ScopeID,
			"event", row.Event,
			"attempt_count", row.AttemptCount+1,
			"next_attempt_in", backoff.String(),
			"error", deliveryErr.Error(),
			"consecutive_failures", n)
	}
}

// @decision: lifecycle-subscriber-at-least-once-delivery
func (r *LifecycleReconciler) listOldestPendingPerStream(
	ctx context.Context, dueAt time.Time,
) ([]persistence.LifecycleOutboxRow, bool) {
	var rows []persistence.LifecycleOutboxRow
	if err := r.deps.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		out, err := r.deps.Persist.LifecycleOutbox().ListOldestPendingPerStream(ctx, lifecycleDrainBatch, dueAt, tx)
		rows = out
		return err
	}); err != nil {
		if n, log := r.recordFailure("lifecycle_outbox"); log {
			r.logger.Warn("LIFECYCLE.STAGEDDELIVERY.LISTFAILED",
				"error", err.Error(), "consecutive_failures", n)
		}
		return nil, false
	}
	r.clearFailure("lifecycle_outbox")
	return rows, true
}

func (r *LifecycleReconciler) recordFailure(key string) (attempt int, shouldLog bool) {
	r.failMu.Lock()
	defer r.failMu.Unlock()
	if r.failCount == nil {
		r.failCount = make(map[string]int)
	}
	r.failCount[key]++
	n := r.failCount[key]
	return n, n == 1 || n%lifecycleFailureLogEvery == 0
}

func (r *LifecycleReconciler) clearFailure(key string) {
	r.failMu.Lock()
	delete(r.failCount, key)
	r.failMu.Unlock()
}
