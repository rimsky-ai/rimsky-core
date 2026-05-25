// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Postgres impl of persistence.PublisherSubscriptionsTable —
// publisher-subscription lifecycle state per spec §Publisher protocol
// unification.

package postgres

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/fallguyconsulting/rimsky/foundation/persistence"
	"github.com/fallguyconsulting/rimsky/foundation/shared"
)

type publisherSubscriptionsImpl tablesImpl

var _ persistence.PublisherSubscriptionsTable = (*publisherSubscriptionsImpl)(nil)

// PublisherSubscriptions returns the postgres PublisherSubscriptionsTable impl.
func (s *tablesImpl) PublisherSubscriptions() persistence.PublisherSubscriptionsTable {
	return (*publisherSubscriptionsImpl)(s)
}

func (b *publisherSubscriptionsImpl) q(tx persistence.Tx) querier { return (*tablesImpl)(b).q(tx) }

const insertPublisherSubscriptionSQL = `
INSERT INTO rimsky_publisher_subscriptions (
    id, instance_id, publisher_name, kind, resolved_config,
    target_node, message_kind, started_at, state
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`

func (b *publisherSubscriptionsImpl) Insert(ctx context.Context, tx persistence.Tx, row persistence.PublisherSubscriptionRow) error {
	if row.State == "" {
		row.State = persistence.PublisherSubscriptionStateActive
	}
	if row.MessageKind == "" {
		row.MessageKind = "invalidate"
	}
	_, err := b.q(tx).Exec(ctx, insertPublisherSubscriptionSQL,
		row.ID, row.InstanceID, row.PublisherName, row.Kind,
		row.ResolvedConfig, row.TargetNode, row.MessageKind,
		row.StartedAt, row.State)
	if err != nil {
		return fmt.Errorf("postgres.PublisherSubscriptions.Insert: %w", err)
	}
	return nil
}

func (b *publisherSubscriptionsImpl) Update(ctx context.Context, tx persistence.Tx, id shared.UUID, upd persistence.PublisherSubscriptionUpdate) error {
	sets := []string{}
	args := []any{}
	if upd.State != nil {
		args = append(args, *upd.State)
		sets = append(sets, fmt.Sprintf("state = $%d", len(args)))
	}
	if upd.StartedAt != nil {
		args = append(args, *upd.StartedAt)
		sets = append(sets, fmt.Sprintf("started_at = $%d", len(args)))
	}
	if len(sets) == 0 {
		return nil
	}
	args = append(args, id)
	sql := fmt.Sprintf(`UPDATE rimsky_publisher_subscriptions SET %s WHERE id = $%d`,
		strings.Join(sets, ", "), len(args))
	if _, err := b.q(tx).Exec(ctx, sql, args...); err != nil {
		return fmt.Errorf("postgres.PublisherSubscriptions.Update: %w", err)
	}
	return nil
}

const deletePublisherSubscriptionSQL = `DELETE FROM rimsky_publisher_subscriptions WHERE id = $1`

func (b *publisherSubscriptionsImpl) Delete(ctx context.Context, tx persistence.Tx, id shared.UUID) error {
	if _, err := b.q(tx).Exec(ctx, deletePublisherSubscriptionSQL, id); err != nil {
		return fmt.Errorf("postgres.PublisherSubscriptions.Delete: %w", err)
	}
	return nil
}

const listPublisherSubscriptionsByInstanceSQL = `
SELECT id, instance_id, publisher_name, kind, resolved_config,
       target_node, message_kind, started_at, state
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
       target_node, message_kind, started_at, state
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
       target_node, message_kind, started_at, state
  FROM rimsky_publisher_subscriptions
 WHERE id = $1`

func (b *publisherSubscriptionsImpl) Get(ctx context.Context, tx persistence.Tx, id shared.UUID) (*persistence.PublisherSubscriptionRow, error) {
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

func collectPublisherSubscriptions(rows pgx.Rows) ([]persistence.PublisherSubscriptionRow, error) {
	out := []persistence.PublisherSubscriptionRow{}
	for rows.Next() {
		var w persistence.PublisherSubscriptionRow
		if err := rows.Scan(
			&w.ID, &w.InstanceID, &w.PublisherName, &w.Kind,
			&w.ResolvedConfig, &w.TargetNode, &w.MessageKind,
			&w.StartedAt, &w.State,
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
