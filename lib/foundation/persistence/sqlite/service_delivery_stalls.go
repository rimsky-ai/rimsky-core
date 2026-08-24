// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @decision: service-delivery-stall-signal

package sqlite

import (
	"context"
	"fmt"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

type serviceDeliveryStallsImpl tablesImpl

var _ persistence.ServiceDeliveryStallTable = (*serviceDeliveryStallsImpl)(nil)

func (s *tablesImpl) ServiceDeliveryStalls() persistence.ServiceDeliveryStallTable {
	return (*serviceDeliveryStallsImpl)(s)
}

func (b *serviceDeliveryStallsImpl) q(tx persistence.Tx) querier { return (*tablesImpl)(b).q(tx) }

// @decision: service-delivery-stall-signal
func (b *serviceDeliveryStallsImpl) MarkStalled(
	ctx context.Context, service string, outbox persistence.ServiceDeliveryOutbox, since time.Time, tx persistence.Tx,
) (bool, error) {
	res, err := b.q(tx).ExecContext(ctx,
		`INSERT INTO rimsky_service_delivery_stalls (service, outbox, stalled_since)
		 VALUES (?, ?, ?)
		 ON CONFLICT (service, outbox) DO NOTHING`,
		service, string(outbox), formatTime(since),
	)
	if err != nil {
		return false, fmt.Errorf("servicedeliverystalls.markStalled: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("servicedeliverystalls.markStalled: rows affected: %w", err)
	}
	return n == 1, nil
}

// @decision: service-delivery-stall-signal
func (b *serviceDeliveryStallsImpl) ClearStalled(
	ctx context.Context, service string, outbox persistence.ServiceDeliveryOutbox, tx persistence.Tx,
) (bool, error) {
	res, err := b.q(tx).ExecContext(ctx,
		`DELETE FROM rimsky_service_delivery_stalls WHERE service = ? AND outbox = ?`,
		service, string(outbox),
	)
	if err != nil {
		return false, fmt.Errorf("servicedeliverystalls.clearStalled: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("servicedeliverystalls.clearStalled: rows affected: %w", err)
	}
	return n == 1, nil
}

// @decision: service-delivery-stall-signal
func (b *serviceDeliveryStallsImpl) ListStalled(
	ctx context.Context, outbox persistence.ServiceDeliveryOutbox, tx persistence.Tx,
) ([]string, error) {
	rows, err := b.q(tx).QueryContext(ctx,
		`SELECT service FROM rimsky_service_delivery_stalls WHERE outbox = ? ORDER BY service ASC`,
		string(outbox),
	)
	if err != nil {
		return nil, fmt.Errorf("servicedeliverystalls.listStalled: %w", err)
	}
	defer rows.Close()

	out := []string{}
	for rows.Next() {
		var service string
		if err := rows.Scan(&service); err != nil {
			return nil, fmt.Errorf("servicedeliverystalls.listStalled: %w", err)
		}
		out = append(out, service)
	}
	return out, rows.Err()
}
