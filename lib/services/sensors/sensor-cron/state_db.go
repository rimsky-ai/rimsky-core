// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// state_db.go — sensor-cron per-binary state persistence.
//
// When env RIMSKY_SENSOR_CRON_STATE_DSN is set, active cron
// publisher-subscriptions and their next-fire watermarks persist across
// restarts. When empty, the binary runs in-memory (subscriptions
// reconstructed via Publisher.Subscribe replay from rimsky's
// runtime.ResyncPublisherSubscriptions at control-api startup).
//
// The load-bearing durable state is `next_fire_at`: recovering it from
// the DB lets a restarted binary fire on the ORIGINALLY-scheduled window
// rather than recomputing sched.Next(restartTime) (which would skip the
// in-flight window and miss a fire). That distinction — recover the
// watermark, do not recompute it — is the durability property this layer
// protects; it is why next_fire_at is persisted as a TIMESTAMPTZ and read
// back as a time.Time rather than rederived from the cron expression.
//
// Driver constraint: the DSN MUST be a Postgres DSN (the schema uses
// `now()` and `TIMESTAMPTZ`, which are Postgres-only). Operators wanting
// per-sensor isolation typically point this at a dedicated schema or
// database on the shared rimsky Postgres; SQLite is not supported. If
// lightweight dev-only persistence is needed, leave the env var empty and
// rely on the in-memory mode + Publisher.Subscribe resync at control-api
// startup.
//
// The table shape is sensor-cron-specific (cron expression, next-fire
// watermark, missed-fires hint) — it is NOT shared with rimsky's
// foundation/persistence layer (per
// .ok-planner/specs/2026-05-17-sensor-messaging-unification-design.md
// §Tension 2 resolution: each publisher owns its own state schema).
package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"time"

	// @constraint: blank import registers the "pgx" driver name with database/sql so sql.Open("pgx", dsn) resolves.
	_ "github.com/jackc/pgx/v5/stdlib"
)

// stateDB is sensor-cron's per-binary state persistence.
type stateDB struct {
	db *sql.DB
}

// openStateDB opens the state database when the env var is set.
// Returns (nil, nil) when no DSN is configured — callers run in
// in-memory mode.
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

// bootstrap creates the sensor_cron_state table if absent and prunes
// the obsolete `missed_fires` column from any pre-existing dev table.
// Missed-fire backfill was never implemented (see the sensor package
// doc); the column persisted an operator hint that no code path
// consumed, so it is dropped at bootstrap rather than carried forward
// as dead schema. Idempotent across restarts; safe to run as part of
// openStateDB. Pre-v1 there is no production data to migrate.
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
	// @deliberate: IF EXISTS keeps the drop idempotent across restarts and a no-op on fresh installs where the obsolete missed_fires column was never created.
	_, err := s.db.ExecContext(ctx,
		`ALTER TABLE sensor_cron_state DROP COLUMN IF EXISTS missed_fires`)
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
// Subscribe. The persisted next_fire_at is the watermark recovered on
// restart, so the row is written with the Watch's current NextFireAt.
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

// DeleteSubscription removes a publisher-subscription row. Called on
// Unsubscribe.
func (s *stateDB) DeleteSubscription(ctx context.Context, subscriptionID string) error {
	if s == nil {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM sensor_cron_state WHERE publisher_subscription_id = $1`, subscriptionID)
	return err
}

// UpdateNextFire advances the durable next-fire watermark after a fire.
// Called from fireOne so the persisted watermark tracks the in-memory one;
// a restart between fires resumes from the last advanced window, never a
// re-fired or skipped one.
func (s *stateDB) UpdateNextFire(ctx context.Context, subscriptionID string, nextFireAt time.Time, lastFireAt *time.Time) error {
	if s == nil {
		return nil
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE sensor_cron_state SET next_fire_at = $1, last_fire_at = $2 WHERE publisher_subscription_id = $3`,
		nextFireAt, lastFireAt, subscriptionID)
	return err
}

// SubscriptionState is the persisted shape returned by ListAll. It carries
// every column needed to rebuild a Watch — crucially the persisted
// NextFireAt watermark and CronExpr — so a restarted service resumes on the
// originally-scheduled window.
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

// ListAll returns every persisted subscription. Used at startup to rebuild
// in-memory state from durable storage.
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
