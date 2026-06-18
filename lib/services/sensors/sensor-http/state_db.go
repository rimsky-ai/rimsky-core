// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

type stateDB struct {
	db *sql.DB
}

func openStateDB(ctx context.Context) (*stateDB, error) {
	dsn := os.Getenv("RIMSKY_SENSOR_HTTP_STATE_DSN")
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
		CREATE TABLE IF NOT EXISTS sensor_http_state (
		    publisher_subscription_id TEXT PRIMARY KEY,
		    instance_id               TEXT NOT NULL,
		    url                       TEXT NOT NULL,
		    poll_interval             TEXT NOT NULL,
		    match_status              TEXT NOT NULL,
		    match_json_key            TEXT,
		    match_json_val            TEXT,
		    target_node               TEXT NOT NULL,
		    message_type              TEXT NOT NULL,
		    last_poll_at              TIMESTAMPTZ,
		    last_hash                 TEXT,
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
		INSERT INTO sensor_http_state (
		    publisher_subscription_id, instance_id, url, poll_interval,
		    match_status, match_json_key, match_json_val,
		    target_node, message_type
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (publisher_subscription_id) DO UPDATE SET
		    instance_id     = EXCLUDED.instance_id,
		    url             = EXCLUDED.url,
		    poll_interval   = EXCLUDED.poll_interval,
		    match_status    = EXCLUDED.match_status,
		    match_json_key  = EXCLUDED.match_json_key,
		    match_json_val  = EXCLUDED.match_json_val,
		    target_node     = EXCLUDED.target_node,
		    message_type    = EXCLUDED.message_type
	`
	matchStatus := ""
	for i, c := range w.MatchStatus {
		if i > 0 {
			matchStatus += ","
		}
		matchStatus += fmt.Sprintf("%d", c)
	}
	_, err := s.db.ExecContext(ctx, q,
		w.SubscriptionID, w.InstanceID, w.URL, w.PollInterval.String(),
		matchStatus, w.MatchJSONKey, w.MatchJSONVal,
		w.TargetNode, w.MessageType)
	return err
}

func (s *stateDB) DeleteSubscription(ctx context.Context, subscriptionID string) error {
	if s == nil {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM sensor_http_state WHERE publisher_subscription_id = $1`, subscriptionID)
	return err
}

func (s *stateDB) UpdateLastHash(ctx context.Context, subscriptionID, hash string) error {
	if s == nil {
		return nil
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE sensor_http_state SET last_hash = $1, last_poll_at = now() WHERE publisher_subscription_id = $2`,
		hash, subscriptionID)
	return err
}

type SubscriptionState struct {
	SubscriptionID string
	InstanceID     string
	URL            string
	PollInterval   time.Duration
	MatchStatus    []int
	MatchJSONKey   string
	MatchJSONVal   string
	TargetNode     string
	MessageType    string
	LastHash       string
}

func (s *stateDB) ListAll(ctx context.Context) ([]SubscriptionState, error) {
	if s == nil {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT publisher_subscription_id, instance_id, url, poll_interval,
		        COALESCE(match_status, ''),
		        COALESCE(match_json_key, ''),
		        COALESCE(match_json_val, ''),
		        target_node, message_type, COALESCE(last_hash, '')
		   FROM sensor_http_state`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SubscriptionState{}
	for rows.Next() {
		w, err := scanSubscriptionState(rows.Scan)
		if err != nil {
			return nil, err
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
		`SELECT publisher_subscription_id, instance_id, url, poll_interval,
		        COALESCE(match_status, ''),
		        COALESCE(match_json_key, ''),
		        COALESCE(match_json_val, ''),
		        target_node, message_type, COALESCE(last_hash, '')
		   FROM sensor_http_state
		  WHERE publisher_subscription_id = $1`,
		subscriptionID)
	w, err := scanSubscriptionState(row.Scan)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &w, nil
}

func scanSubscriptionState(scan func(...any) error) (SubscriptionState, error) {
	var (
		w            SubscriptionState
		pollInterval string
		matchStatus  string
	)
	if err := scan(&w.SubscriptionID, &w.InstanceID, &w.URL, &pollInterval,
		&matchStatus, &w.MatchJSONKey, &w.MatchJSONVal,
		&w.TargetNode, &w.MessageType, &w.LastHash); err != nil {
		return SubscriptionState{}, err
	}
	if pollInterval != "" {
		d, err := time.ParseDuration(pollInterval)
		if err != nil {
			return SubscriptionState{}, fmt.Errorf("parse persisted poll_interval %q for %s: %w",
				pollInterval, w.SubscriptionID, err)
		}
		w.PollInterval = d
	}
	if matchStatus != "" {
		parts := strings.Split(matchStatus, ",")
		w.MatchStatus = make([]int, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			n, err := strconv.Atoi(p)
			if err != nil {
				return SubscriptionState{}, fmt.Errorf("parse persisted match_status %q for %s: %w",
					matchStatus, w.SubscriptionID, err)
			}
			w.MatchStatus = append(w.MatchStatus, n)
		}
	}
	return w, nil
}
