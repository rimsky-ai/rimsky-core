// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package observability

import (
	"context"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

func MetricsHookOf(reg *MetricsRegistry) *RegistryHook {
	if reg == nil {
		return nil
	}
	return &RegistryHook{reg: reg}
}

type RegistryHook struct {
	reg *MetricsRegistry
}

func (h *RegistryHook) IncDispatch(executor, terminalClass string) {
	h.reg.Dispatches.WithLabelValues(executor, terminalClass).Inc()
}

func (h *RegistryHook) IncTerminal(terminalClass, errorClass string) {
	h.reg.TerminalVerdicts.WithLabelValues(terminalClass, errorClass).Inc()
}

func (h *RegistryHook) IncInvalidate(sourceKind string) {
	h.reg.Invalidates.WithLabelValues(sourceKind).Inc()
}

func (h *RegistryHook) IncClaimAcquisition(producer, intent string) {
	h.reg.ClaimAcquisitions.WithLabelValues(producer, intent).Inc()
}

// @decision: named-lock-metric
func (h *RegistryHook) IncNamedLockAcquisition(lockName, intent string) {
	h.reg.NamedLockAcquisitions.WithLabelValues(lockName, intent).Inc()
}

func (h *RegistryHook) ObserveDispatchLatency(executor string, seconds float64) {
	h.reg.DispatchLatencySeconds.WithLabelValues(executor).Observe(seconds)
}

func (h *RegistryHook) ObserveClaimAcquisitionLatency(producer string, seconds float64) {
	h.reg.ClaimAcquisitionLatencySeconds.WithLabelValues(producer).Observe(seconds)
}

func (h *RegistryHook) ObserveFrameDuration(seconds float64) {
	h.reg.FrameDurationSeconds.Observe(seconds)
}

func (h *RegistryHook) ObserveParkedDurationOnResume(seconds float64) {
	h.reg.ParkedDurationOnResumeSeconds.Observe(seconds)
}

func (h *RegistryHook) SetNodesByState(state string, count float64) {
	h.reg.NodesByState.WithLabelValues(state).Set(count)
}

func (h *RegistryHook) SetParkedNodes(count float64) {
	h.reg.ParkedNodes.Set(count)
}

func (h *RegistryHook) SetHeldFrames(count float64) {
	h.reg.HeldFrames.Set(count)
}

func (h *RegistryHook) SetNodeRunsPending(count float64) {
	h.reg.NodeRunsPending.Set(count)
}

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

func (h *RegistryHook) refreshGauges(ctx context.Context, persist persistence.Tables, queue persistence.Queue, log shared.Logger) {
	if persist == nil || queue == nil {
		return
	}
	if depth, err := queue.CountLive(ctx, persistence.DispatchListFilter{State: "pending"}); err == nil {
		h.SetNodeRunsPending(float64(depth))
	} else {
		log.Debug("metrics gauge refresh: CountLive failed", "error", err.Error())
	}
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
	if n, err := queue.CountParked(ctx); err == nil {
		h.SetParkedNodes(float64(n))
	} else {
		log.Debug("metrics gauge refresh: CountParked failed", "error", err.Error())
	}
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
