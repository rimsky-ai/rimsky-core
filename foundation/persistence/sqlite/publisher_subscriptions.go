// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// SQLite impl of persistence.PublisherSubscriptionsTable — mirror of the
// postgres impl. SQLite is dev-only.

package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/foundation/shared"
)

type publisherSubscriptionsImpl tablesImpl

var _ persistence.PublisherSubscriptionsTable = (*publisherSubscriptionsImpl)(nil)

func (s *tablesImpl) PublisherSubscriptions() persistence.PublisherSubscriptionsTable {
	return (*publisherSubscriptionsImpl)(s)
}

func (b *publisherSubscriptionsImpl) q(tx persistence.Tx) querier {
	return (*tablesImpl)(b).q(tx)
}

const sqliteInsertPublisherSubscriptionSQL = `
INSERT INTO rimsky_publisher_subscriptions (
    id, instance_id, publisher_name, kind, resolved_config,
    target_node, message_kind, started_at, state
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`

func (b *publisherSubscriptionsImpl) Insert(ctx context.Context, tx persistence.Tx, row persistence.PublisherSubscriptionRow) error {
	if row.State == "" {
		row.State = persistence.PublisherSubscriptionStateActive
	}
	if row.MessageKind == "" {
		row.MessageKind = "invalidate"
	}
	_, err := b.q(tx).ExecContext(ctx, sqliteInsertPublisherSubscriptionSQL,
		row.ID.String(), row.InstanceID.String(), row.PublisherName, row.Kind,
		row.ResolvedConfig, row.TargetNode, row.MessageKind,
		row.StartedAt, row.State)
	if err != nil {
		return fmt.Errorf("sqlite.PublisherSubscriptions.Insert: %w", err)
	}
	return nil
}

func (b *publisherSubscriptionsImpl) Update(ctx context.Context, tx persistence.Tx, id shared.UUID, upd persistence.PublisherSubscriptionUpdate) error {
	sets := []string{}
	args := []any{}
	if upd.State != nil {
		args = append(args, *upd.State)
		sets = append(sets, "state = ?")
	}
	if upd.StartedAt != nil {
		args = append(args, *upd.StartedAt)
		sets = append(sets, "started_at = ?")
	}
	if len(sets) == 0 {
		return nil
	}
	args = append(args, id.String())
	sql := fmt.Sprintf(`UPDATE rimsky_publisher_subscriptions SET %s WHERE id = ?`,
		strings.Join(sets, ", "))
	if _, err := b.q(tx).ExecContext(ctx, sql, args...); err != nil {
		return fmt.Errorf("sqlite.PublisherSubscriptions.Update: %w", err)
	}
	return nil
}

const sqliteDeletePublisherSubscriptionSQL = `DELETE FROM rimsky_publisher_subscriptions WHERE id = ?`

func (b *publisherSubscriptionsImpl) Delete(ctx context.Context, tx persistence.Tx, id shared.UUID) error {
	if _, err := b.q(tx).ExecContext(ctx, sqliteDeletePublisherSubscriptionSQL, id.String()); err != nil {
		return fmt.Errorf("sqlite.PublisherSubscriptions.Delete: %w", err)
	}
	return nil
}

const sqliteListPublisherSubscriptionsByInstanceSQL = `
SELECT id, instance_id, publisher_name, kind, resolved_config,
       target_node, message_kind, started_at, state
  FROM rimsky_publisher_subscriptions
 WHERE instance_id = ?
 ORDER BY publisher_name ASC`

func (b *publisherSubscriptionsImpl) ListByInstance(ctx context.Context, instanceID shared.UUID) ([]persistence.PublisherSubscriptionRow, error) {
	rows, err := (*tablesImpl)(b).db.QueryContext(ctx, sqliteListPublisherSubscriptionsByInstanceSQL, instanceID.String())
	if err != nil {
		return nil, fmt.Errorf("sqlite.PublisherSubscriptions.ListByInstance: %w", err)
	}
	defer rows.Close()
	return scanPublisherSubscriptions(rows)
}

const sqliteListPublisherSubscriptionsByStateSQL = `
SELECT id, instance_id, publisher_name, kind, resolved_config,
       target_node, message_kind, started_at, state
  FROM rimsky_publisher_subscriptions
 WHERE state = ?`

func (b *publisherSubscriptionsImpl) ListByState(ctx context.Context, state string) ([]persistence.PublisherSubscriptionRow, error) {
	rows, err := (*tablesImpl)(b).db.QueryContext(ctx, sqliteListPublisherSubscriptionsByStateSQL, state)
	if err != nil {
		return nil, fmt.Errorf("sqlite.PublisherSubscriptions.ListByState: %w", err)
	}
	defer rows.Close()
	return scanPublisherSubscriptions(rows)
}

const sqliteGetPublisherSubscriptionSQL = `
SELECT id, instance_id, publisher_name, kind, resolved_config,
       target_node, message_kind, started_at, state
  FROM rimsky_publisher_subscriptions
 WHERE id = ?`

func (b *publisherSubscriptionsImpl) Get(ctx context.Context, tx persistence.Tx, id shared.UUID) (*persistence.PublisherSubscriptionRow, error) {
	rows, err := b.q(tx).QueryContext(ctx, sqliteGetPublisherSubscriptionSQL, id.String())
	if err != nil {
		return nil, fmt.Errorf("sqlite.PublisherSubscriptions.Get: %w", err)
	}
	defer rows.Close()
	out, err := scanPublisherSubscriptions(rows)
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, nil
	}
	return &out[0], nil
}

func scanPublisherSubscriptions(rows *sql.Rows) ([]persistence.PublisherSubscriptionRow, error) {
	out := []persistence.PublisherSubscriptionRow{}
	for rows.Next() {
		var w persistence.PublisherSubscriptionRow
		var idStr, instanceStr string
		var startedAt sql.NullTime
		if err := rows.Scan(
			&idStr, &instanceStr, &w.PublisherName, &w.Kind,
			&w.ResolvedConfig, &w.TargetNode, &w.MessageKind,
			&startedAt, &w.State,
		); err != nil {
			return nil, err
		}
		if u, err := uuid.Parse(idStr); err == nil {
			w.ID = u
		}
		if u, err := uuid.Parse(instanceStr); err == nil {
			w.InstanceID = u
		}
		if startedAt.Valid {
			w.StartedAt = startedAt.Time
		}
		out = append(out, w)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
