// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// SQLite impl of persistence.BreakpointTable — runtime-installed
// pause/notify breakpoints per concept:breakpoint. Mirror of the
// postgres impl.
//
// @concept: breakpoint

package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

// breakpointsImpl is the per-row-type aspect of *tablesImpl, exposing
// the BreakpointTable method set. Mirrors the postgres-side pattern.
type breakpointsImpl tablesImpl

var _ persistence.BreakpointTable = (*breakpointsImpl)(nil)

// Breakpoints returns the sqlite BreakpointTable impl.
func (s *tablesImpl) Breakpoints() persistence.BreakpointTable { return (*breakpointsImpl)(s) }

func (b *breakpointsImpl) q(tx persistence.Tx) querier { return (*tablesImpl)(b).q(tx) }

// breakpointCols mirrors the postgres-side column list.
const sqliteBreakpointCols = `id, instance_id, matcher, checkpoint, signal_type, mode,
	overflow_policy, hit_ttl_seconds, ttl_seconds, dropped_count,
	created_by_key, created_at, expires_at`

// Create inserts a new row. SQLite has no DB-side UUID default, so the
// caller-supplied ID is required; if zero, we generate one here. Mirrors
// the postgres impl's behavior of returning the row id.
func (b *breakpointsImpl) Create(ctx context.Context, bp persistence.BreakpointRow, tx persistence.Tx) (shared.UUID, error) {
	ex := b.q(tx)
	id := bp.ID
	if id == (shared.UUID{}) {
		id = uuid.New()
	}
	matcherMap := bp.Matcher
	if matcherMap == nil {
		matcherMap = map[string]any{}
	}
	matcherBytes, err := json.Marshal(matcherMap)
	if err != nil {
		return shared.UUID{}, fmt.Errorf("sqlite.breakpoints.create: marshal matcher: %w", err)
	}
	createdAt := time.Now().UTC().Format(time.RFC3339Nano)
	var (
		ttlArg     any
		expiresArg any
		sigArg     any
	)
	if bp.TTLSeconds != nil {
		ttlArg = *bp.TTLSeconds
		// Materialize expires_at at write time so SweepExpired's predicate
		// is a simple `expires_at <= ?` comparison.
		expiresArg = time.Now().UTC().
			Add(time.Duration(*bp.TTLSeconds) * time.Second).
			Format(time.RFC3339Nano)
	}
	if bp.SignalType != nil {
		sigArg = *bp.SignalType
	}
	if _, err := ex.ExecContext(ctx,
		`INSERT INTO rimsky_instance_breakpoints
		   (id, instance_id, matcher, checkpoint, signal_type, mode,
		    overflow_policy, hit_ttl_seconds, ttl_seconds, dropped_count,
		    created_by_key, created_at, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id.String(), bp.InstanceID.String(), string(matcherBytes),
		string(bp.Checkpoint), sigArg, string(bp.Mode),
		string(bp.OverflowPolicy), bp.HitTTLSeconds, ttlArg, bp.DroppedCount,
		bp.CreatedByKey, createdAt, expiresArg,
	); err != nil {
		return shared.UUID{}, fmt.Errorf("sqlite.breakpoints.create: %w", err)
	}
	return id, nil
}

// Get returns the breakpoint by id; (nil, nil) on not-found.
func (b *breakpointsImpl) Get(ctx context.Context, id shared.UUID, tx persistence.Tx) (*persistence.BreakpointRow, error) {
	ex := b.q(tx)
	row := ex.QueryRowContext(ctx,
		`SELECT `+sqliteBreakpointCols+`
		   FROM rimsky_instance_breakpoints
		  WHERE id = ?`, id.String())
	out, err := scanSqliteBreakpoint(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("sqlite.breakpoints.get: %w", err)
	}
	return &out, nil
}

// ListForInstance mirrors the postgres-side filter semantics.
func (b *breakpointsImpl) ListForInstance(ctx context.Context, instanceID shared.UUID, includeExpired bool, tx persistence.Tx) ([]persistence.BreakpointRow, error) {
	ex := b.q(tx)
	sqlStr := `SELECT ` + sqliteBreakpointCols + `
		   FROM rimsky_instance_breakpoints
		  WHERE instance_id = ?`
	args := []any{instanceID.String()}
	if !includeExpired {
		// SQLite stores expires_at as ISO-8601 text; lexicographic
		// comparison agrees with chronological order for RFC3339Nano.
		nowStr := time.Now().UTC().Format(time.RFC3339Nano)
		sqlStr += ` AND (expires_at IS NULL OR expires_at > ?)`
		args = append(args, nowStr)
	}
	sqlStr += ` ORDER BY created_at ASC`
	rows, err := ex.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite.breakpoints.listForInstance: %w", err)
	}
	defer rows.Close()
	out := []persistence.BreakpointRow{}
	for rows.Next() {
		r, err := scanSqliteBreakpoint(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite.breakpoints.listForInstance.scan: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// Delete cascades to rimsky_breakpoint_hits via FK ON DELETE CASCADE.
func (b *breakpointsImpl) Delete(ctx context.Context, id shared.UUID, tx persistence.Tx) error {
	ex := b.q(tx)
	if _, err := ex.ExecContext(ctx,
		`DELETE FROM rimsky_instance_breakpoints WHERE id = ?`, id.String()); err != nil {
		return fmt.Errorf("sqlite.breakpoints.delete: %w", err)
	}
	return nil
}

// IncrementDropped bumps dropped_count atomically.
func (b *breakpointsImpl) IncrementDropped(ctx context.Context, id shared.UUID, tx persistence.Tx) error {
	ex := b.q(tx)
	if _, err := ex.ExecContext(ctx,
		`UPDATE rimsky_instance_breakpoints
		    SET dropped_count = dropped_count + 1
		  WHERE id = ?`, id.String()); err != nil {
		return fmt.Errorf("sqlite.breakpoints.incrementDropped: %w", err)
	}
	return nil
}

// SweepExpired deletes past-expiry rows. Returns rowcount.
func (b *breakpointsImpl) SweepExpired(ctx context.Context, now time.Time, tx persistence.Tx) (int, error) {
	ex := b.q(tx)
	res, err := ex.ExecContext(ctx,
		`DELETE FROM rimsky_instance_breakpoints
		  WHERE expires_at IS NOT NULL AND expires_at <= ?`,
		now.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, fmt.Errorf("sqlite.breakpoints.sweepExpired: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("sqlite.breakpoints.sweepExpired.rowsAffected: %w", err)
	}
	return int(n), nil
}

func scanSqliteBreakpoint(sc scannable) (persistence.BreakpointRow, error) {
	var (
		idStr          string
		instanceIDStr  string
		matcherStr     string
		checkpoint     string
		signalType     sql.NullString
		mode           string
		overflowPolicy string
		hitTTLSeconds  int
		ttlSeconds     sql.NullInt64
		droppedCount   int64
		createdByKey   string
		createdAtStr   string
		expiresAtStr   sql.NullString
	)
	if err := sc.Scan(&idStr, &instanceIDStr, &matcherStr, &checkpoint, &signalType, &mode,
		&overflowPolicy, &hitTTLSeconds, &ttlSeconds, &droppedCount,
		&createdByKey, &createdAtStr, &expiresAtStr); err != nil {
		return persistence.BreakpointRow{}, err
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		return persistence.BreakpointRow{}, fmt.Errorf("scan breakpoint id: %w", err)
	}
	instID, err := uuid.Parse(instanceIDStr)
	if err != nil {
		return persistence.BreakpointRow{}, fmt.Errorf("scan breakpoint instance_id: %w", err)
	}
	matcherMap := map[string]any{}
	if matcherStr != "" {
		if err := json.Unmarshal([]byte(matcherStr), &matcherMap); err != nil {
			return persistence.BreakpointRow{}, fmt.Errorf("unmarshal matcher: %w", err)
		}
	}
	createdAt, err := parseSQLiteTime(createdAtStr)
	if err != nil {
		return persistence.BreakpointRow{}, fmt.Errorf("scan created_at: %w", err)
	}
	out := persistence.BreakpointRow{
		ID:             id,
		InstanceID:     instID,
		Matcher:        matcherMap,
		Checkpoint:     persistence.BreakpointCheckpoint(checkpoint),
		Mode:           persistence.BreakpointMode(mode),
		OverflowPolicy: persistence.BreakpointOverflowPolicy(overflowPolicy),
		HitTTLSeconds:  hitTTLSeconds,
		DroppedCount:   droppedCount,
		CreatedByKey:   createdByKey,
		CreatedAt:      createdAt,
	}
	if signalType.Valid {
		s := signalType.String
		out.SignalType = &s
	}
	if ttlSeconds.Valid {
		n := int(ttlSeconds.Int64)
		out.TTLSeconds = &n
	}
	if expiresAtStr.Valid {
		t, err := parseSQLiteTime(expiresAtStr.String)
		if err != nil {
			return persistence.BreakpointRow{}, fmt.Errorf("scan expires_at: %w", err)
		}
		out.ExpiresAt = &t
	}
	return out, nil
}
