// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"

	_ "github.com/jackc/pgx/v5/stdlib"
)

type stateDB struct {
	db *sql.DB
}

func openStateDB(ctx context.Context) (*stateDB, error) {
	dsn := os.Getenv("RIMSKY_SENSOR_WEBHOOK_STATE_DSN")
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
		CREATE TABLE IF NOT EXISTS sensor_webhook_state (
		    publisher_subscription_id TEXT PRIMARY KEY,
		    last_idempotency_key      TEXT,
		    last_seen_at              TIMESTAMPTZ NOT NULL DEFAULT now()
		);
	`
	if _, err := s.db.ExecContext(ctx, schema); err != nil {
		return err
	}
	droppedConfigColumns := []string{
		"instance_id", "path_prefix", "idempotency_header",
		"message_type", "auth_config", "target_node", "started_at",
	}
	for _, col := range droppedConfigColumns {
		if _, err := s.db.ExecContext(ctx,
			fmt.Sprintf("ALTER TABLE sensor_webhook_state DROP COLUMN IF EXISTS %s", col)); err != nil {
			return err
		}
	}
	return nil
}

func (s *stateDB) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *stateDB) DeleteSubscription(ctx context.Context, subscriptionID string) error {
	if s == nil {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM sensor_webhook_state WHERE publisher_subscription_id = $1`, subscriptionID)
	return err
}

func (s *stateDB) UpdateLastIdempotency(ctx context.Context, subscriptionID, key string) error {
	if s == nil {
		return nil
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sensor_webhook_state (publisher_subscription_id, last_idempotency_key, last_seen_at)
		      VALUES ($1, $2, now())
		 ON CONFLICT (publisher_subscription_id) DO UPDATE SET
		     last_idempotency_key = EXCLUDED.last_idempotency_key,
		     last_seen_at         = now()`,
		subscriptionID, key)
	return err
}

func (s *stateDB) GetLastIdempotency(ctx context.Context, subscriptionID string) (string, error) {
	if s == nil {
		return "", nil
	}
	row := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(last_idempotency_key, '')
		   FROM sensor_webhook_state
		  WHERE publisher_subscription_id = $1`,
		subscriptionID)
	var key string
	if err := row.Scan(&key); err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", err
	}
	return key, nil
}
