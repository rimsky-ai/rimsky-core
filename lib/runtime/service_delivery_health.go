// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @decision: service-delivery-stall-signal
// @concept: event-log

package runtime

import (
	"context"
	"fmt"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/eventpayload"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/events"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

// @decision: service-delivery-stall-signal
type ServiceDeliveryHealth struct {
	persist    persistence.Tables
	outbox     persistence.ServiceDeliveryOutbox
	stallAfter time.Duration
	logger     shared.Logger
}

func NewServiceDeliveryHealth(
	persist persistence.Tables, outbox persistence.ServiceDeliveryOutbox,
	stallAfter time.Duration, logger shared.Logger,
) *ServiceDeliveryHealth {
	if stallAfter <= 0 {
		stallAfter = DefaultServiceDeliveryStallAfter
	}
	if logger == nil {
		logger = shared.SilentLogger{}
	}
	return &ServiceDeliveryHealth{persist: persist, outbox: outbox, stallAfter: stallAfter, logger: logger}
}

func (h *ServiceDeliveryHealth) StallAfter() time.Duration {
	if h == nil {
		return 0
	}
	return h.stallAfter
}

// @decision: service-delivery-stall-signal
func (h *ServiceDeliveryHealth) ObservePending(
	ctx context.Context, pending []persistence.ServiceOutboxPending, now time.Time,
) {
	if h == nil || h.persist == nil {
		return
	}
	if err := h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		marked, err := h.persist.ServiceDeliveryStalls().ListStalled(ctx, h.outbox, tx)
		if err != nil {
			return fmt.Errorf("list the services marked stalled on the %s outbox: %w", h.outbox, err)
		}
		stalled := map[string]bool{}
		for _, p := range pending {
			age := now.Sub(p.OldestPendingAt)
			if age < h.stallAfter {
				continue
			}
			stalled[p.Service] = true
			if err := h.markStalled(ctx, p, now, age, tx); err != nil {
				return err
			}
		}
		for _, service := range marked {
			if stalled[service] {
				continue
			}
			if err := h.clearStalled(ctx, service, now, tx); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		h.logger.Warn("SERVICEDELIVERY.HEALTH.UNRECORDED",
			"outbox", string(h.outbox),
			"error", err.Error(),
			"consequence", "no event-log entry marks this pass's stall or recovery. The next pass tries again")
	}
}

// @decision: service-delivery-stall-signal
func (h *ServiceDeliveryHealth) markStalled(
	ctx context.Context, p persistence.ServiceOutboxPending, now time.Time, age time.Duration, tx persistence.Tx,
) error {
	fresh, err := h.persist.ServiceDeliveryStalls().MarkStalled(ctx, p.Service, h.outbox, p.OldestPendingAt, tx)
	if err != nil {
		return fmt.Errorf("mark service %q stalled on the %s outbox: %w", p.Service, h.outbox, err)
	}
	if !fresh {
		return nil
	}
	payload := &genv1.ServiceDeliveryStalledPayload{
		Service:                 p.Service,
		Outbox:                  string(h.outbox),
		PendingCount:            int32(p.PendingCount),
		OldestPendingAgeSeconds: int32(age.Seconds()),
	}
	return h.persist.Events().Append(ctx, persistence.EventAppendInput{
		Kind:       events.KindServiceDeliveryStalled(),
		Payload:    eventpayload.New(payload),
		OccurredAt: &now,
	}, tx)
}

// @decision: service-delivery-stall-signal
func (h *ServiceDeliveryHealth) clearStalled(
	ctx context.Context, service string, now time.Time, tx persistence.Tx,
) error {
	wasStalled, err := h.persist.ServiceDeliveryStalls().ClearStalled(ctx, service, h.outbox, tx)
	if err != nil {
		return fmt.Errorf("clear the stall marker of service %q on the %s outbox: %w", service, h.outbox, err)
	}
	if !wasStalled {
		return nil
	}
	payload := &genv1.ServiceDeliveryRecoveredPayload{Service: service, Outbox: string(h.outbox)}
	return h.persist.Events().Append(ctx, persistence.EventAppendInput{
		Kind:       events.KindServiceDeliveryRecovered(),
		Payload:    eventpayload.New(payload),
		OccurredAt: &now,
	}, tx)
}

// @decision: service-delivery-stall-signal
func pendingSummaryFromProducerVerbRows(
	rows []persistence.ProducerVerbOutboxRow, settled map[int64]bool,
) []persistence.ServiceOutboxPending {
	byService := map[string]*persistence.ServiceOutboxPending{}
	order := []string{}
	for _, row := range rows {
		if settled[row.Seq] {
			continue
		}
		e, ok := byService[row.ProducerName]
		if !ok {
			e = &persistence.ServiceOutboxPending{Service: row.ProducerName, OldestPendingAt: row.EnqueuedAt}
			byService[row.ProducerName] = e
			order = append(order, row.ProducerName)
		}
		e.PendingCount++
		if row.EnqueuedAt.Before(e.OldestPendingAt) {
			e.OldestPendingAt = row.EnqueuedAt
		}
	}
	out := make([]persistence.ServiceOutboxPending, 0, len(order))
	for _, name := range order {
		out = append(out, *byService[name])
	}
	return out
}
