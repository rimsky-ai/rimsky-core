// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// state_db.go — sensor-webhook per-binary state persistence.
//
// When env RIMSKY_SENSOR_WEBHOOK_STATE_DSN is set, publisher-
// subscription rows + per-subscription last-idempotency-key persist
// across restarts. This is what allows webhook providers' retry semantics
// to keep working across sensor-webhook restarts.
//
// Driver constraint: the DSN MUST be a Postgres DSN (the schema uses
// `now()` and `TIMESTAMPTZ`, which are Postgres-only). SQLite is not
// supported; leave the env var empty for in-memory mode.
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
		    instance_id               TEXT NOT NULL,
		    path_prefix               TEXT NOT NULL,
		    idempotency_header        TEXT,
		    target_node               TEXT NOT NULL,
		    message_type              TEXT NOT NULL,
		    last_idempotency_key      TEXT,
		    last_seen_at              TIMESTAMPTZ,
		    started_at                TIMESTAMPTZ NOT NULL DEFAULT now()
		);
	`
	_, err := s.db.ExecContext(ctx, schema)
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
		INSERT INTO sensor_webhook_state (
		    publisher_subscription_id, instance_id, path_prefix,
		    idempotency_header, target_node, message_type
		) VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (publisher_subscription_id) DO UPDATE SET
		    instance_id        = EXCLUDED.instance_id,
		    path_prefix        = EXCLUDED.path_prefix,
		    idempotency_header = EXCLUDED.idempotency_header,
		    target_node        = EXCLUDED.target_node,
		    message_type       = EXCLUDED.message_type
	`
	_, err := s.db.ExecContext(ctx, q,
		w.SubscriptionID, w.InstanceID, w.PathPrefix,
		w.IdempotencyHeader, w.TargetNode, w.MessageType)
	return err
}

func (s *stateDB) DeleteSubscription(ctx context.Context, subscriptionID string) error {
	if s == nil {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM sensor_webhook_state WHERE publisher_subscription_id = $1`, subscriptionID)
	return err
}

// UpdateLastIdempotency persists the most-recent idempotency-key seen.
// Webhook providers' retry semantics are preserved across restarts.
func (s *stateDB) UpdateLastIdempotency(ctx context.Context, subscriptionID, key string) error {
	if s == nil {
		return nil
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE sensor_webhook_state SET last_idempotency_key = $1, last_seen_at = now() WHERE publisher_subscription_id = $2`,
		key, subscriptionID)
	return err
}

// SubscriptionState is the persisted shape returned by ListAll.
type SubscriptionState struct {
	SubscriptionID     string
	InstanceID         string
	PathPrefix         string
	IdempotencyHeader  string
	TargetNode         string
	MessageType        string
	LastIdempotencyKey string
}

func (s *stateDB) ListAll(ctx context.Context) ([]SubscriptionState, error) {
	if s == nil {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT publisher_subscription_id, instance_id, path_prefix,
		        COALESCE(idempotency_header, ''), target_node, message_type,
		        COALESCE(last_idempotency_key, '')
		   FROM sensor_webhook_state`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SubscriptionState{}
	for rows.Next() {
		var w SubscriptionState
		if err := rows.Scan(&w.SubscriptionID, &w.InstanceID, &w.PathPrefix,
			&w.IdempotencyHeader, &w.TargetNode, &w.MessageType, &w.LastIdempotencyKey); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// GetSubscription returns the persisted state for a single subscription,
// or (nil, nil) if no row exists. Used by Subscribe to pre-populate the
// most-recent inbound idempotency key so deduping continues to function
// across restarts.
func (s *stateDB) GetSubscription(ctx context.Context, subscriptionID string) (*SubscriptionState, error) {
	if s == nil {
		return nil, nil
	}
	row := s.db.QueryRowContext(ctx,
		`SELECT publisher_subscription_id, instance_id, path_prefix,
		        COALESCE(idempotency_header, ''), target_node, message_type,
		        COALESCE(last_idempotency_key, '')
		   FROM sensor_webhook_state
		  WHERE publisher_subscription_id = $1`,
		subscriptionID)
	var w SubscriptionState
	if err := row.Scan(&w.SubscriptionID, &w.InstanceID, &w.PathPrefix,
		&w.IdempotencyHeader, &w.TargetNode, &w.MessageType, &w.LastIdempotencyKey); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &w, nil
}
