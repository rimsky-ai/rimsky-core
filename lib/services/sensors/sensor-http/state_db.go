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
	"strconv"
	"strings"
	"time"

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
// GetSubscription. It carries every field the in-memory Watch needs,
// so AttachStateDB can rebuild watches end-to-end on restart without
// requiring rimsky to re-Subscribe (rimsky's ResyncPublisherSubscriptions
// runs only at control-api startup, not on demand; sensor process
// restarts on a still-running rimsky would otherwise silently drop the
// in-memory watches).
type SubscriptionState struct {
	SubscriptionID string
	InstanceID     string
	URL            string
	// PollInterval is the in-memory Watch's PollInterval. Stored as a
	// duration-string in the table; surfaced here as a parsed Duration so
	// AttachStateDB rebuild can populate Watch.PollInterval directly.
	PollInterval time.Duration
	MatchStatus  []int
	MatchJSONKey string
	MatchJSONVal string
	TargetNode   string
	MessageKind  string
	LastHash     string
}

// ListAll returns every persisted subscription. Used at startup to
// rebuild in-memory state from durable storage. The returned slice
// carries every field the in-memory Watch needs (URL, poll interval,
// match predicate, body-hash watermark), so a restarted binary resumes
// polling without losing the body-filter or the watermark.
func (s *stateDB) ListAll(ctx context.Context) ([]SubscriptionState, error) {
	if s == nil {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT publisher_subscription_id, instance_id, url, poll_interval,
		        COALESCE(match_status, ''),
		        COALESCE(match_json_key, ''),
		        COALESCE(match_json_val, ''),
		        target_node, message_kind, COALESCE(last_hash, '')
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

// GetSubscription returns the persisted state for a single subscription,
// or (nil, nil) if no row exists. Used by Subscribe to pre-populate the
// in-memory Watch with the body-hash watermark across restarts.
func (s *stateDB) GetSubscription(ctx context.Context, subscriptionID string) (*SubscriptionState, error) {
	if s == nil {
		return nil, nil
	}
	row := s.db.QueryRowContext(ctx,
		`SELECT publisher_subscription_id, instance_id, url, poll_interval,
		        COALESCE(match_status, ''),
		        COALESCE(match_json_key, ''),
		        COALESCE(match_json_val, ''),
		        target_node, message_kind, COALESCE(last_hash, '')
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

// scanSubscriptionState centralizes the row → SubscriptionState
// projection used by ListAll and GetSubscription. The SELECT column
// order in both callers MUST match the Scan order here.
//
// `poll_interval` is stored as a duration-string (e.g. "30s") and
// parsed back to time.Duration; an unparseable value is reported as an
// error rather than silently zeroed so the sensor doesn't quietly poll
// every nanosecond after a restart against a corrupted row.
//
// `match_status` is stored as a comma-joined integer list (e.g.
// "200,201") to keep the schema simple (TEXT column); an empty string
// means "no explicit set declared," matching the in-memory Watch's
// MatchStatus=nil semantics ("any 2xx").
func scanSubscriptionState(scan func(...any) error) (SubscriptionState, error) {
	var (
		w            SubscriptionState
		pollInterval string
		matchStatus  string
	)
	if err := scan(&w.SubscriptionID, &w.InstanceID, &w.URL, &pollInterval,
		&matchStatus, &w.MatchJSONKey, &w.MatchJSONVal,
		&w.TargetNode, &w.MessageKind, &w.LastHash); err != nil {
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
