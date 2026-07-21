// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

type publisherSubscriptionsImpl tablesImpl

var _ persistence.PublisherSubscriptionTable = (*publisherSubscriptionsImpl)(nil)

func (s *tablesImpl) PublisherSubscriptions() persistence.PublisherSubscriptionTable {
	return (*publisherSubscriptionsImpl)(s)
}

func (b *publisherSubscriptionsImpl) q(tx persistence.Tx) querier { return (*tablesImpl)(b).q(tx) }

const insertPublisherSubscriptionSQL = `
INSERT INTO rimsky_publisher_subscriptions (
    id, instance_id, publisher_name, kind, resolved_config,
    message_type, started_at, state, failure_reason
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NULLIF($9, ''))`

func (b *publisherSubscriptionsImpl) Insert(ctx context.Context, row persistence.PublisherSubscriptionRow, tx persistence.Tx) error {
	if row.State == "" {
		row.State = persistence.PublisherSubscriptionStateMounting
	}
	_, err := b.q(tx).Exec(ctx, insertPublisherSubscriptionSQL,
		row.ID, row.InstanceID, row.PublisherName, row.Kind,
		row.ResolvedConfig, row.MessageType,
		row.StartedAt, row.State, row.FailureReason)
	if err != nil {
		return fmt.Errorf("postgres.PublisherSubscriptions.Insert: %w", err)
	}
	return nil
}

const deletePublisherSubscriptionSQL = `DELETE FROM rimsky_publisher_subscriptions WHERE id = $1`

func (b *publisherSubscriptionsImpl) Delete(ctx context.Context, id shared.UUID, tx persistence.Tx) error {
	if _, err := b.q(tx).Exec(ctx, deletePublisherSubscriptionSQL, id); err != nil {
		return fmt.Errorf("postgres.PublisherSubscriptions.Delete: %w", err)
	}
	return nil
}

const listPublisherSubscriptionsByInstanceSQL = `
SELECT id, instance_id, publisher_name, kind, resolved_config,
       message_type, started_at, state,
       COALESCE(failure_reason, '')
  FROM rimsky_publisher_subscriptions
 WHERE instance_id = $1
 ORDER BY publisher_name ASC`

func (b *publisherSubscriptionsImpl) ListByInstance(ctx context.Context, instanceID shared.UUID) ([]persistence.PublisherSubscriptionRow, error) {
	rows, err := (*tablesImpl)(b).pool.Query(ctx, listPublisherSubscriptionsByInstanceSQL, instanceID)
	if err != nil {
		return nil, fmt.Errorf("postgres.PublisherSubscriptions.ListByInstance: %w", err)
	}
	defer rows.Close()
	return collectPublisherSubscriptions(rows)
}

const listPublisherSubscriptionsByStateSQL = `
SELECT id, instance_id, publisher_name, kind, resolved_config,
       message_type, started_at, state,
       COALESCE(failure_reason, '')
  FROM rimsky_publisher_subscriptions
 WHERE state = $1`

func (b *publisherSubscriptionsImpl) ListByState(ctx context.Context, state string) ([]persistence.PublisherSubscriptionRow, error) {
	rows, err := (*tablesImpl)(b).pool.Query(ctx, listPublisherSubscriptionsByStateSQL, state)
	if err != nil {
		return nil, fmt.Errorf("postgres.PublisherSubscriptions.ListByState: %w", err)
	}
	defer rows.Close()
	return collectPublisherSubscriptions(rows)
}

const getPublisherSubscriptionSQL = `
SELECT id, instance_id, publisher_name, kind, resolved_config,
       message_type, started_at, state,
       COALESCE(failure_reason, '')
  FROM rimsky_publisher_subscriptions
 WHERE id = $1`

func (b *publisherSubscriptionsImpl) Get(ctx context.Context, id shared.UUID, tx persistence.Tx) (*persistence.PublisherSubscriptionRow, error) {
	rows, err := b.q(tx).Query(ctx, getPublisherSubscriptionSQL, id)
	if err != nil {
		return nil, fmt.Errorf("postgres.PublisherSubscriptions.Get: %w", err)
	}
	defer rows.Close()
	out, err := collectPublisherSubscriptions(rows)
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, nil
	}
	return &out[0], nil
}

const casPublisherSubscriptionStateSQL = `
UPDATE rimsky_publisher_subscriptions
   SET state = $1, failure_reason = NULLIF($2, '')
 WHERE id = $3 AND state = $4`

func (b *publisherSubscriptionsImpl) CompareAndSetState(ctx context.Context, id shared.UUID, from, to, failureReason string) (bool, error) {
	tag, err := (*tablesImpl)(b).pool.Exec(ctx, casPublisherSubscriptionStateSQL,
		to, failureReason, id, from)
	if err != nil {
		return false, fmt.Errorf("postgres.PublisherSubscriptions.CompareAndSetState: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

func collectPublisherSubscriptions(rows pgx.Rows) ([]persistence.PublisherSubscriptionRow, error) {
	out := []persistence.PublisherSubscriptionRow{}
	for rows.Next() {
		var w persistence.PublisherSubscriptionRow
		if err := rows.Scan(
			&w.ID, &w.InstanceID, &w.PublisherName, &w.Kind,
			&w.ResolvedConfig, &w.MessageType,
			&w.StartedAt, &w.State, &w.FailureReason,
		); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
