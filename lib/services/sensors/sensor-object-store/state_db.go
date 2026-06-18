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
	dsn := os.Getenv("RIMSKY_SENSOR_OBJECT_STORE_STATE_DSN")
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
		CREATE TABLE IF NOT EXISTS sensor_object_store_state (
		    publisher_subscription_id TEXT PRIMARY KEY,
		    instance_id               TEXT NOT NULL,
		    backend                   TEXT NOT NULL,
		    bucket                    TEXT NOT NULL,
		    prefix                    TEXT NOT NULL,
		    poll_interval             TEXT NOT NULL,
		    watermark_field           TEXT NOT NULL,
		    target_node               TEXT NOT NULL,
		    message_type              TEXT NOT NULL,
		    last_poll_at              TIMESTAMPTZ,
		    watermark_name            TEXT,
		    watermark_time            TIMESTAMPTZ,
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
		INSERT INTO sensor_object_store_state (
		    publisher_subscription_id, instance_id, backend, bucket, prefix,
		    poll_interval, watermark_field, target_node, message_type
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (publisher_subscription_id) DO UPDATE SET
		    instance_id     = EXCLUDED.instance_id,
		    backend         = EXCLUDED.backend,
		    bucket          = EXCLUDED.bucket,
		    prefix          = EXCLUDED.prefix,
		    poll_interval   = EXCLUDED.poll_interval,
		    watermark_field = EXCLUDED.watermark_field,
		    target_node     = EXCLUDED.target_node,
		    message_type    = EXCLUDED.message_type
	`
	_, err := s.db.ExecContext(ctx, q,
		w.SubscriptionID, w.InstanceID, w.Backend, w.Bucket, w.Prefix,
		w.PollInterval.String(), w.WatermarkField, w.TargetNode, w.MessageType)
	return err
}

func (s *stateDB) DeleteSubscription(ctx context.Context, subscriptionID string) error {
	if s == nil {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM sensor_object_store_state WHERE publisher_subscription_id = $1`, subscriptionID)
	return err
}

func (s *stateDB) UpdateWatermarkName(ctx context.Context, subscriptionID, name string) error {
	if s == nil {
		return nil
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE sensor_object_store_state SET watermark_name = $1, last_poll_at = now() WHERE publisher_subscription_id = $2`,
		name, subscriptionID)
	return err
}

func (s *stateDB) UpdateWatermarkTime(ctx context.Context, subscriptionID string, t time.Time) error {
	if s == nil {
		return nil
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE sensor_object_store_state SET watermark_time = $1, last_poll_at = now() WHERE publisher_subscription_id = $2`,
		t.UTC(), subscriptionID)
	return err
}

type SubscriptionState struct {
	SubscriptionID string
	InstanceID     string
	Backend        string
	Bucket         string
	Prefix         string
	PollInterval   string
	WatermarkField string
	TargetNode     string
	MessageType    string
	WatermarkName  string
	WatermarkTime  *time.Time
}

func (s *stateDB) ListAll(ctx context.Context) ([]SubscriptionState, error) {
	if s == nil {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT publisher_subscription_id, instance_id, backend, bucket, prefix,
		        poll_interval, watermark_field, target_node, message_type,
		        COALESCE(watermark_name, ''), watermark_time
		   FROM sensor_object_store_state`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SubscriptionState{}
	for rows.Next() {
		var w SubscriptionState
		var wmTime sql.NullTime
		if err := rows.Scan(&w.SubscriptionID, &w.InstanceID, &w.Backend, &w.Bucket, &w.Prefix,
			&w.PollInterval, &w.WatermarkField, &w.TargetNode, &w.MessageType,
			&w.WatermarkName, &wmTime); err != nil {
			return nil, err
		}
		if wmTime.Valid {
			t := wmTime.Time
			w.WatermarkTime = &t
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func (s *stateDB) GetSubscription(ctx context.Context, subscriptionID string) (*SubscriptionState, error) {
	if s == nil {
		return nil, nil
	}
	row := s.db.QueryRowContext(ctx,
		`SELECT publisher_subscription_id, instance_id, backend, bucket, prefix,
		        poll_interval, watermark_field, target_node, message_type,
		        COALESCE(watermark_name, ''), watermark_time
		   FROM sensor_object_store_state
		  WHERE publisher_subscription_id = $1`,
		subscriptionID)
	var w SubscriptionState
	var wmTime sql.NullTime
	if err := row.Scan(&w.SubscriptionID, &w.InstanceID, &w.Backend, &w.Bucket, &w.Prefix,
		&w.PollInterval, &w.WatermarkField, &w.TargetNode, &w.MessageType,
		&w.WatermarkName, &wmTime); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if wmTime.Valid {
		t := wmTime.Time
		w.WatermarkTime = &t
	}
	return &w, nil
}
