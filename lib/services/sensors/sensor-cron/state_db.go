// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

type stateDB struct {
	db *sql.DB
}

func openStateDB(ctx context.Context) (*stateDB, error) {
	dsn := os.Getenv("RIMSKY_SENSOR_CRON_STATE_DSN")
	if dsn == "" {
		return nil, nil
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open state db: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping state db: %w", err)
	}
	s := &stateDB{db: db}
	if err := s.bootstrap(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("bootstrap state db: %w", err)
	}
	return s, nil
}

func (s *stateDB) bootstrap(ctx context.Context) error {
	const schema = `
		CREATE TABLE IF NOT EXISTS sensor_cron_state (
		    publisher_subscription_id TEXT PRIMARY KEY,
		    instance_id               TEXT NOT NULL,
		    cron_expr                 TEXT NOT NULL,
		    target_node               TEXT NOT NULL,
		    message_type              TEXT NOT NULL,
		    next_fire_at              TIMESTAMPTZ NOT NULL,
		    started_at                TIMESTAMPTZ NOT NULL DEFAULT now(),
		    last_fire_at              TIMESTAMPTZ
		);
	`
	if _, err := s.db.ExecContext(ctx, schema); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx,
		`ALTER TABLE sensor_cron_state DROP COLUMN IF EXISTS missed_fires`)
	return err
}

func (s *stateDB) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *stateDB) UpsertSubscription(ctx context.Context, w *Watch) error {
	if s == nil {
		return nil
	}
	const q = `
		INSERT INTO sensor_cron_state (
		    publisher_subscription_id, instance_id, cron_expr,
		    target_node, message_type, next_fire_at, started_at,
		    last_fire_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (publisher_subscription_id) DO UPDATE SET
		    instance_id   = EXCLUDED.instance_id,
		    cron_expr     = EXCLUDED.cron_expr,
		    target_node   = EXCLUDED.target_node,
		    message_type  = EXCLUDED.message_type,
		    next_fire_at  = EXCLUDED.next_fire_at
	`
	_, err := s.db.ExecContext(ctx, q,
		w.SubscriptionID, w.InstanceID, w.CronExpr,
		w.TargetNode, w.MessageType, w.NextFireAt, w.StartedAt,
		w.LastFireAt)
	return err
}

func (s *stateDB) DeleteSubscription(ctx context.Context, subscriptionID string) error {
	if s == nil {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM sensor_cron_state WHERE publisher_subscription_id = $1`, subscriptionID)
	return err
}

func (s *stateDB) UpdateNextFire(ctx context.Context, subscriptionID string, nextFireAt time.Time, lastFireAt *time.Time) error {
	if s == nil {
		return nil
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE sensor_cron_state SET next_fire_at = $1, last_fire_at = $2 WHERE publisher_subscription_id = $3`,
		nextFireAt, lastFireAt, subscriptionID)
	return err
}

type SubscriptionState struct {
	SubscriptionID string
	InstanceID     string
	CronExpr       string
	TargetNode     string
	MessageType    string
	NextFireAt     time.Time
	StartedAt      time.Time
	LastFireAt     *time.Time
}

func (s *stateDB) ListAll(ctx context.Context) ([]SubscriptionState, error) {
	if s == nil {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT publisher_subscription_id, instance_id, cron_expr,
		        target_node, message_type, next_fire_at, started_at,
		        last_fire_at
		   FROM sensor_cron_state`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SubscriptionState{}
	for rows.Next() {
		var st SubscriptionState
		if err := rows.Scan(&st.SubscriptionID, &st.InstanceID, &st.CronExpr,
			&st.TargetNode, &st.MessageType, &st.NextFireAt, &st.StartedAt,
			&st.LastFireAt); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}
