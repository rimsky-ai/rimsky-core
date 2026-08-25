// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// @decision: postgres-pgx-v5
type stateDB struct {
	db *pgxpool.Pool
}

func openStateDB(ctx context.Context) (*stateDB, error) {
	dsn := os.Getenv("RIMSKY_SENSOR_HTTP_STATE_DSN")
	if dsn == "" {
		return nil, nil
	}
	db, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("open state db: %w", err)
	}
	if err := db.Ping(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping state db: %w", err)
	}
	s := &stateDB{db: db}
	if err := s.bootstrap(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("bootstrap state db: %w", err)
	}
	return s, nil
}

// @decision: secret-at-rest-posture
func (s *stateDB) bootstrap(ctx context.Context) error {
	const schema = `
		CREATE TABLE IF NOT EXISTS sensor_http_state (
		    publisher_subscription_id TEXT PRIMARY KEY,
		    last_poll_at              TIMESTAMPTZ,
		    last_hash                 TEXT,
		    started_at                TIMESTAMPTZ NOT NULL DEFAULT now()
		);
		ALTER TABLE sensor_http_state DROP COLUMN IF EXISTS auth;
		ALTER TABLE sensor_http_state DROP COLUMN IF EXISTS url;
		ALTER TABLE sensor_http_state DROP COLUMN IF EXISTS poll_interval;
		ALTER TABLE sensor_http_state DROP COLUMN IF EXISTS match_status;
		ALTER TABLE sensor_http_state DROP COLUMN IF EXISTS match_json_key;
		ALTER TABLE sensor_http_state DROP COLUMN IF EXISTS match_json_val;
		ALTER TABLE sensor_http_state DROP COLUMN IF EXISTS message_type;
		ALTER TABLE sensor_http_state DROP COLUMN IF EXISTS instance_id;
	`
	_, err := s.db.Exec(ctx, schema)
	return err
}

func (s *stateDB) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	s.db.Close()
	return nil
}

func (s *stateDB) UpsertSubscription(ctx context.Context, w *Watch) error {
	if s == nil {
		return nil
	}
	const q = `
		INSERT INTO sensor_http_state (publisher_subscription_id)
		VALUES ($1)
		ON CONFLICT (publisher_subscription_id) DO NOTHING
	`
	_, err := s.db.Exec(ctx, q, w.SubscriptionID)
	return err
}

func (s *stateDB) DeleteSubscription(ctx context.Context, subscriptionID string) error {
	if s == nil {
		return nil
	}
	_, err := s.db.Exec(ctx, `DELETE FROM sensor_http_state WHERE publisher_subscription_id = $1`, subscriptionID)
	return err
}

func (s *stateDB) UpdateLastHash(ctx context.Context, subscriptionID, hash string) error {
	if s == nil {
		return nil
	}
	_, err := s.db.Exec(ctx,
		`UPDATE sensor_http_state SET last_hash = $1, last_poll_at = now() WHERE publisher_subscription_id = $2`,
		hash, subscriptionID)
	return err
}

type SubscriptionWatermark struct {
	SubscriptionID string
	StartedAt      time.Time
	LastHash       string
	LastPollAt     time.Time
}

func (s *stateDB) ListWatermarks(ctx context.Context) ([]SubscriptionWatermark, error) {
	if s == nil {
		return nil, nil
	}
	rows, err := s.db.Query(ctx,
		`SELECT publisher_subscription_id, started_at, COALESCE(last_hash, ''), last_poll_at
		   FROM sensor_http_state`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SubscriptionWatermark{}
	for rows.Next() {
		w, err := scanWatermark(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func (s *stateDB) GetWatermark(ctx context.Context, subscriptionID string) (*SubscriptionWatermark, error) {
	if s == nil {
		return nil, nil
	}
	row := s.db.QueryRow(ctx,
		`SELECT publisher_subscription_id, started_at, COALESCE(last_hash, ''), last_poll_at
		   FROM sensor_http_state
		  WHERE publisher_subscription_id = $1`,
		subscriptionID)
	w, err := scanWatermark(row.Scan)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &w, nil
}

func scanWatermark(scan func(...any) error) (SubscriptionWatermark, error) {
	var (
		w          SubscriptionWatermark
		lastPollAt *time.Time
	)
	if err := scan(&w.SubscriptionID, &w.StartedAt, &w.LastHash, &lastPollAt); err != nil {
		return SubscriptionWatermark{}, err
	}
	if lastPollAt != nil {
		w.LastPollAt = *lastPollAt
	}
	return w, nil
}
