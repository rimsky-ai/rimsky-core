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
	dsn := os.Getenv("RIMSKY_SENSOR_OBJECT_STORE_STATE_DSN")
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

func (s *stateDB) bootstrap(ctx context.Context) error {
	const schema = `
		CREATE TABLE IF NOT EXISTS sensor_object_store_state (
		    publisher_subscription_id TEXT PRIMARY KEY,
		    instance_id               TEXT NOT NULL,
		    backend                   TEXT NOT NULL,
		    bucket                    TEXT NOT NULL,
		    prefix                    TEXT NOT NULL,
		    poll_interval             TEXT NOT NULL,
		    watermark_field           TEXT NOT NULL,
		    message_type              TEXT NOT NULL,
		    last_poll_at              TIMESTAMPTZ,
		    watermark_name            TEXT,
		    watermark_time            TIMESTAMPTZ,
		    started_at                TIMESTAMPTZ NOT NULL DEFAULT now()
		);
	`
	if _, err := s.db.Exec(ctx, schema); err != nil {
		return err
	}
	const seenNamesSchema = `
		CREATE TABLE IF NOT EXISTS sensor_object_store_seen_names (
		    publisher_subscription_id TEXT NOT NULL REFERENCES sensor_object_store_state (publisher_subscription_id) ON DELETE CASCADE,
		    object_name               TEXT NOT NULL,
		    PRIMARY KEY (publisher_subscription_id, object_name)
		);
	`
	_, err := s.db.Exec(ctx, seenNamesSchema)
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
		INSERT INTO sensor_object_store_state (
		    publisher_subscription_id, instance_id, backend, bucket, prefix,
		    poll_interval, watermark_field, message_type
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (publisher_subscription_id) DO UPDATE SET
		    instance_id     = EXCLUDED.instance_id,
		    backend         = EXCLUDED.backend,
		    bucket          = EXCLUDED.bucket,
		    prefix          = EXCLUDED.prefix,
		    poll_interval   = EXCLUDED.poll_interval,
		    watermark_field = EXCLUDED.watermark_field,
		    message_type    = EXCLUDED.message_type
	`
	_, err := s.db.Exec(ctx, q,
		w.SubscriptionID, w.InstanceID, w.Backend, w.Bucket, w.Prefix,
		w.PollInterval.String(), w.WatermarkField, w.MessageType)
	return err
}

func (s *stateDB) TouchLastPoll(ctx context.Context, subscriptionID string, at time.Time) error {
	if s == nil {
		return nil
	}
	_, err := s.db.Exec(ctx,
		`UPDATE sensor_object_store_state SET last_poll_at = $1 WHERE publisher_subscription_id = $2`,
		at.UTC(), subscriptionID)
	return err
}

func (s *stateDB) ResetWatermark(ctx context.Context, subscriptionID string) error {
	if s == nil {
		return nil
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx,
		`UPDATE sensor_object_store_state SET watermark_name = NULL, watermark_time = NULL WHERE publisher_subscription_id = $1`,
		subscriptionID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM sensor_object_store_seen_names WHERE publisher_subscription_id = $1`,
		subscriptionID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *stateDB) DeleteSubscription(ctx context.Context, subscriptionID string) error {
	if s == nil {
		return nil
	}
	_, err := s.db.Exec(ctx, `DELETE FROM sensor_object_store_state WHERE publisher_subscription_id = $1`, subscriptionID)
	return err
}

func (s *stateDB) UpdateWatermarkName(ctx context.Context, subscriptionID, name string) error {
	if s == nil {
		return nil
	}
	_, err := s.db.Exec(ctx,
		`UPDATE sensor_object_store_state SET watermark_name = $1, last_poll_at = now() WHERE publisher_subscription_id = $2`,
		name, subscriptionID)
	return err
}

func (s *stateDB) AdvanceWatermarkTime(ctx context.Context, subscriptionID string, t time.Time, name string) error {
	if s == nil {
		return nil
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx,
		`UPDATE sensor_object_store_state SET watermark_time = $1, last_poll_at = now() WHERE publisher_subscription_id = $2`,
		t.UTC(), subscriptionID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM sensor_object_store_seen_names WHERE publisher_subscription_id = $1`,
		subscriptionID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO sensor_object_store_seen_names (publisher_subscription_id, object_name) VALUES ($1, $2)`,
		subscriptionID, name); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *stateDB) AddSeenName(ctx context.Context, subscriptionID, name string) error {
	if s == nil {
		return nil
	}
	_, err := s.db.Exec(ctx,
		`INSERT INTO sensor_object_store_seen_names (publisher_subscription_id, object_name)
		 VALUES ($1, $2) ON CONFLICT (publisher_subscription_id, object_name) DO NOTHING`,
		subscriptionID, name)
	return err
}

func (s *stateDB) ListSeenNames(ctx context.Context, subscriptionID string) ([]string, error) {
	if s == nil {
		return nil, nil
	}
	rows, err := s.db.Query(ctx,
		`SELECT object_name FROM sensor_object_store_seen_names WHERE publisher_subscription_id = $1`,
		subscriptionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

type SubscriptionState struct {
	SubscriptionID string
	InstanceID     string
	Backend        string
	Bucket         string
	Prefix         string
	PollInterval   string
	WatermarkField string
	MessageType    string
	WatermarkName  string
	WatermarkTime  *time.Time
}

func (s *stateDB) ListAll(ctx context.Context) ([]SubscriptionState, error) {
	if s == nil {
		return nil, nil
	}
	rows, err := s.db.Query(ctx,
		`SELECT publisher_subscription_id, instance_id, backend, bucket, prefix,
		        poll_interval, watermark_field, message_type,
		        COALESCE(watermark_name, ''), watermark_time
		   FROM sensor_object_store_state`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SubscriptionState{}
	for rows.Next() {
		var w SubscriptionState
		var wmTime *time.Time
		if err := rows.Scan(&w.SubscriptionID, &w.InstanceID, &w.Backend, &w.Bucket, &w.Prefix,
			&w.PollInterval, &w.WatermarkField, &w.MessageType,
			&w.WatermarkName, &wmTime); err != nil {
			return nil, err
		}
		w.WatermarkTime = wmTime
		out = append(out, w)
	}
	return out, rows.Err()
}

func (s *stateDB) GetSubscription(ctx context.Context, subscriptionID string) (*SubscriptionState, error) {
	if s == nil {
		return nil, nil
	}
	row := s.db.QueryRow(ctx,
		`SELECT publisher_subscription_id, instance_id, backend, bucket, prefix,
		        poll_interval, watermark_field, message_type,
		        COALESCE(watermark_name, ''), watermark_time
		   FROM sensor_object_store_state
		  WHERE publisher_subscription_id = $1`,
		subscriptionID)
	var w SubscriptionState
	var wmTime *time.Time
	if err := row.Scan(&w.SubscriptionID, &w.InstanceID, &w.Backend, &w.Bucket, &w.Prefix,
		&w.PollInterval, &w.WatermarkField, &w.MessageType,
		&w.WatermarkName, &wmTime); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	w.WatermarkTime = wmTime
	return &w, nil
}
