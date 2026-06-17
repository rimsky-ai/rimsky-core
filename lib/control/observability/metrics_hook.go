// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Adapter exposing the prometheus-backed MetricsRegistry as a foundation/
// runtime.MetricsHook. Foundation has no prometheus dependency, so it
// declares the hook interface and consumes any conforming implementation.
// Plan I2.

package observability

import (
	"context"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

// MetricsHookOf returns a *RegistryHook backed by the supplied
// MetricsRegistry. Returns nil when registry is nil so call sites can
// pass nil through to integration unchanged.
func MetricsHookOf(reg *MetricsRegistry) *RegistryHook {
	if reg == nil {
		return nil
	}
	return &RegistryHook{reg: reg}
}

// RegistryHook is the prometheus-backed MetricsHook implementation.
type RegistryHook struct {
	reg *MetricsRegistry
}

// IncDispatch records a dispatch start.
func (h *RegistryHook) IncDispatch(executor, terminalClass string) {
	h.reg.Dispatches.WithLabelValues(executor, terminalClass).Inc()
}

// IncTerminal records a resolved terminal verdict.
func (h *RegistryHook) IncTerminal(terminalClass, errorClass string) {
	h.reg.TerminalVerdicts.WithLabelValues(terminalClass, errorClass).Inc()
}

// IncInvalidate records an invalidate fired.
func (h *RegistryHook) IncInvalidate(sourceKind string) {
	h.reg.Invalidates.WithLabelValues(sourceKind).Inc()
}

// IncClaimAcquisition records a claim acquisition.
func (h *RegistryHook) IncClaimAcquisition(producer, intent string) {
	h.reg.ClaimAcquisitions.WithLabelValues(producer, intent).Inc()
}

// IncNamedLockAcquisition records a named-lock acquisition attempt.
func (h *RegistryHook) IncNamedLockAcquisition(lockName, intent string) {
	h.reg.NamedLockAcquisitions.WithLabelValues(lockName, intent).Inc()
}

// ObserveDispatchLatency observes the wall-clock dispatch duration.
func (h *RegistryHook) ObserveDispatchLatency(executor string, seconds float64) {
	h.reg.DispatchLatencySeconds.WithLabelValues(executor).Observe(seconds)
}

// ObserveClaimAcquisitionLatency observes the wall-clock claim
// acquisition tx duration.
func (h *RegistryHook) ObserveClaimAcquisitionLatency(producer string, seconds float64) {
	h.reg.ClaimAcquisitionLatencySeconds.WithLabelValues(producer).Observe(seconds)
}

// ObserveFrameDuration observes a frame's wall-clock duration.
func (h *RegistryHook) ObserveFrameDuration(seconds float64) {
	h.reg.FrameDurationSeconds.Observe(seconds)
}

// ObserveParkedDurationOnResume observes how long a node spent parked.
func (h *RegistryHook) ObserveParkedDurationOnResume(seconds float64) {
	h.reg.ParkedDurationOnResumeSeconds.Observe(seconds)
}

// SetNodesByState sets the node-count gauge for a state.
func (h *RegistryHook) SetNodesByState(state string, count float64) {
	h.reg.NodesByState.WithLabelValues(state).Set(count)
}

// SetParkedByReason sets the parked-count gauge for a reason.
func (h *RegistryHook) SetParkedByReason(reason string, count float64) {
	h.reg.ParkedByReason.WithLabelValues(reason).Set(count)
}

// SetHeldFrames sets the held-frames gauge.
func (h *RegistryHook) SetHeldFrames(count float64) {
	h.reg.HeldFrames.Set(count)
}

// SetNodeRunsPending sets the pending-node-runs gauge.
func (h *RegistryHook) SetNodeRunsPending(count float64) {
	h.reg.NodeRunsPending.Set(count)
}

// StartGaugeRefresher launches a goroutine that periodically queries the
// persistence layer for gauge inputs (NodesByState, ParkedByReason,
// HeldFrames, NodeRunsPending) and writes them to the registry.
// Returns a cancel func that stops the loop. Pass interval=0 to use the
// default 5s. Plan I2.
func (h *RegistryHook) StartGaugeRefresher(ctx context.Context, persist persistence.Tables, queue persistence.Queue, interval time.Duration, log shared.Logger) func() {
	if interval == 0 {
		interval = 5 * time.Second
	}
	if log == nil {
		log = shared.SilentLogger{}
	}
	loopCtx, cancel := context.WithCancel(ctx)
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-loopCtx.Done():
				return
			case <-ticker.C:
				h.refreshGauges(loopCtx, persist, queue, log)
			}
		}
	}()
	return cancel
}

// refreshGauges pulls the current gauge values from the persistence
// layer. Each query failure is logged at Debug and skipped — the gauge
// stays at its prior value, which is harmless (Prometheus rate-of-
// change graphs ignore stale gauges).
func (h *RegistryHook) refreshGauges(ctx context.Context, persist persistence.Tables, queue persistence.Queue, log shared.Logger) {
	if persist == nil || queue == nil {
		return
	}
	if depth, err := queue.CountLive(ctx, persistence.DispatchListFilter{State: "pending"}); err == nil {
		h.SetNodeRunsPending(float64(depth))
	} else {
		log.Debug("metrics gauge refresh: CountLive failed", "error", err.Error())
	}
	// @constraint: per-state node count gauge.
	if err := persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		counts, err := persist.Nodes().CountByState(ctx, tx)
		if err != nil {
			return err
		}
		for state, n := range counts {
			h.SetNodesByState(string(state), float64(n))
		}
		return nil
	}); err != nil {
		log.Debug("metrics gauge refresh: Nodes.CountByState failed", "error", err.Error())
	}
	// @constraint: parked-by-reason gauge.
	if reasons, err := queue.CountParkedByReason(ctx); err == nil {
		for reason, n := range reasons {
			h.SetParkedByReason(reason, float64(n))
		}
	} else {
		log.Debug("metrics gauge refresh: CountParkedByReason failed", "error", err.Error())
	}
	// @constraint: held-frames gauge.
	if err := persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		n, err := persist.Frames().CountHeldFrames(ctx, tx)
		if err != nil {
			return err
		}
		h.SetHeldFrames(float64(n))
		return nil
	}); err != nil {
		log.Debug("metrics gauge refresh: Frames.CountHeldFrames failed", "error", err.Error())
	}
}
