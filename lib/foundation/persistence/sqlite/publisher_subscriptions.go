// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

type publisherSubscriptionsImpl tablesImpl

var _ persistence.PublisherSubscriptionTable = (*publisherSubscriptionsImpl)(nil)

func (s *tablesImpl) PublisherSubscriptions() persistence.PublisherSubscriptionTable {
	return (*publisherSubscriptionsImpl)(s)
}

func (b *publisherSubscriptionsImpl) q(tx persistence.Tx) querier {
	return (*tablesImpl)(b).q(tx)
}

const sqliteInsertPublisherSubscriptionSQL = `
INSERT INTO rimsky_publisher_subscriptions (
    id, instance_id, publisher_name, kind, resolved_config,
    message_type, started_at, state, failure_reason
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, NULLIF(?, ''))`

func (b *publisherSubscriptionsImpl) Insert(ctx context.Context, tx persistence.Tx, row persistence.PublisherSubscriptionRow) error {
	if row.State == "" {
		row.State = persistence.PublisherSubscriptionStateMounting
	}
	_, err := b.q(tx).ExecContext(ctx, sqliteInsertPublisherSubscriptionSQL,
		row.ID.String(), row.InstanceID.String(), row.PublisherName, row.Kind,
		row.ResolvedConfig, row.MessageType,
		formatTime(row.StartedAt), row.State, row.FailureReason)
	if err != nil {
		return fmt.Errorf("sqlite.PublisherSubscriptions.Insert: %w", err)
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
       message_type, started_at, state,
       COALESCE(failure_reason, '')
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
       message_type, started_at, state,
       COALESCE(failure_reason, '')
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
       message_type, started_at, state,
       COALESCE(failure_reason, '')
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

const sqliteCASPublisherSubscriptionStateSQL = `
UPDATE rimsky_publisher_subscriptions
   SET state = ?, failure_reason = NULLIF(?, '')
 WHERE id = ? AND state = ?`

func (b *publisherSubscriptionsImpl) CompareAndSetState(ctx context.Context, id shared.UUID, from, to, failureReason string) (bool, error) {
	res, err := (*tablesImpl)(b).db.ExecContext(ctx, sqliteCASPublisherSubscriptionStateSQL,
		to, failureReason, id.String(), from)
	if err != nil {
		return false, fmt.Errorf("sqlite.PublisherSubscriptions.CompareAndSetState: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("sqlite.PublisherSubscriptions.CompareAndSetState: rows affected: %w", err)
	}
	return n > 0, nil
}

func scanPublisherSubscriptions(rows *sql.Rows) ([]persistence.PublisherSubscriptionRow, error) {
	out := []persistence.PublisherSubscriptionRow{}
	for rows.Next() {
		var w persistence.PublisherSubscriptionRow
		var idStr, instanceStr string
		var startedAtStr sql.NullString
		if err := rows.Scan(
			&idStr, &instanceStr, &w.PublisherName, &w.Kind,
			&w.ResolvedConfig, &w.MessageType,
			&startedAtStr, &w.State, &w.FailureReason,
		); err != nil {
			return nil, err
		}
		u, err := uuid.Parse(idStr)
		if err != nil {
			return nil, fmt.Errorf("sqlite.PublisherSubscriptions: parse id: %w", err)
		}
		w.ID = u
		if u, err = uuid.Parse(instanceStr); err != nil {
			return nil, fmt.Errorf("sqlite.PublisherSubscriptions: parse instance_id: %w", err)
		}
		w.InstanceID = u
		if startedAtStr.Valid {
			t, err := parseTime(startedAtStr.String)
			if err != nil {
				return nil, fmt.Errorf("sqlite.PublisherSubscriptions: parse started_at: %w", err)
			}
			w.StartedAt = t
		}
		out = append(out, w)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
