// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// state_db.go — sensor-http per-binary state persistence.
//
// When env RIMSKY_SENSOR_HTTP_STATE_DSN is set, publisher-subscription
// rows + body-hash watermarks persist across restarts. When empty,
// the binary runs in-memory (subscriptions reconstructed via
// Publisher.Subscribe replay from rimsky's
// runtime.ResyncPublisherSubscriptions at control-api startup).
//
// Driver constraint: the DSN MUST be a Postgres DSN (the schema uses
// `now()` and `TIMESTAMPTZ`, which are Postgres-only). Operators
// wanting per-sensor isolation typically point this at a dedicated
// schema or database on the shared rimsky Postgres; SQLite is not
// supported. If lightweight dev-only persistence is needed, leave the
// env var empty and rely on the in-memory mode + Publisher.Subscribe
// resync at control-api startup.
//
// The table shape is sensor-http-specific (URL, poll interval, match
// predicate, body hash) — it is NOT shared with rimsky's
// foundation/persistence layer (per
// .ok-planner/specs/2026-05-17-sensor-messaging-unification-design.md
// §Tension 2 resolution: each publisher owns its own state schema).
package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"

	// jackc/pgx/v5 stdlib driver; registers the "pgx" driver name.
	_ "github.com/jackc/pgx/v5/stdlib"
)

// stateDB is sensor-http's per-binary state persistence.
type stateDB struct {
	db *sql.DB
}

// openStateDB opens the state database when the env var is set.
// Returns (nil, nil) when no DSN is configured — callers run in
// in-memory mode.
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

// bootstrap creates the sensor_http_state table if absent. Idempotent
// across restarts; safe to run as part of openStateDB.
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
		    message_kind              TEXT NOT NULL,
		    last_poll_at              TIMESTAMPTZ,
		    last_hash                 TEXT,
		    started_at                TIMESTAMPTZ NOT NULL DEFAULT now()
		);
	`
	_, err := s.db.ExecContext(ctx, schema)
	return err
}

// Close releases the database connection.
func (s *stateDB) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// UpsertSubscription persists a publisher-subscription row. Called on
// Subscribe.
func (s *stateDB) UpsertSubscription(ctx context.Context, w *Watch) error {
	if s == nil {
		return nil
	}
	const q = `
		INSERT INTO sensor_http_state (
		    publisher_subscription_id, instance_id, url, poll_interval,
		    match_status, match_json_key, match_json_val,
		    target_node, message_kind
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (publisher_subscription_id) DO UPDATE SET
		    instance_id     = EXCLUDED.instance_id,
		    url             = EXCLUDED.url,
		    poll_interval   = EXCLUDED.poll_interval,
		    match_status    = EXCLUDED.match_status,
		    match_json_key  = EXCLUDED.match_json_key,
		    match_json_val  = EXCLUDED.match_json_val,
		    target_node     = EXCLUDED.target_node,
		    message_kind    = EXCLUDED.message_kind
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
		w.TargetNode, w.MessageKind)
	return err
}

// DeleteSubscription removes a publisher-subscription row. Called on
// Unsubscribe.
func (s *stateDB) DeleteSubscription(ctx context.Context, subscriptionID string) error {
	if s == nil {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM sensor_http_state WHERE publisher_subscription_id = $1`, subscriptionID)
	return err
}

// UpdateLastHash persists the body-hash watermark after a successful
// emit. Called from pollOne.
func (s *stateDB) UpdateLastHash(ctx context.Context, subscriptionID, hash string) error {
	if s == nil {
		return nil
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE sensor_http_state SET last_hash = $1, last_poll_at = now() WHERE publisher_subscription_id = $2`,
		hash, subscriptionID)
	return err
}

// SubscriptionState is the persisted shape returned by ListAll /
// GetSubscription.
type SubscriptionState struct {
	SubscriptionID string
	InstanceID     string
	URL            string
	PollInterval   string
	TargetNode     string
	MessageKind    string
	LastHash       string
}

// ListAll returns every persisted subscription. Used at startup to
// rebuild in-memory state from durable storage.
func (s *stateDB) ListAll(ctx context.Context) ([]SubscriptionState, error) {
	if s == nil {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT publisher_subscription_id, instance_id, url, poll_interval,
		        target_node, message_kind, COALESCE(last_hash, '')
		   FROM sensor_http_state`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SubscriptionState{}
	for rows.Next() {
		var w SubscriptionState
		if err := rows.Scan(&w.SubscriptionID, &w.InstanceID, &w.URL, &w.PollInterval,
			&w.TargetNode, &w.MessageKind, &w.LastHash); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// GetSubscription returns the persisted state for a single subscription,
// or (nil, nil) if no row exists. Used by Subscribe to pre-populate the
// in-memory Watch with the body-hash watermark across restarts.
func (s *stateDB) GetSubscription(ctx context.Context, subscriptionID string) (*SubscriptionState, error) {
	if s == nil {
		return nil, nil
	}
	row := s.db.QueryRowContext(ctx,
		`SELECT publisher_subscription_id, instance_id, url, poll_interval,
		        target_node, message_kind, COALESCE(last_hash, '')
		   FROM sensor_http_state
		  WHERE publisher_subscription_id = $1`,
		subscriptionID)
	var w SubscriptionState
	if err := row.Scan(&w.SubscriptionID, &w.InstanceID, &w.URL, &w.PollInterval,
		&w.TargetNode, &w.MessageKind, &w.LastHash); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &w, nil
}
