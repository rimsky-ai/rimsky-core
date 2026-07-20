// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: breakpoint

package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

type breakpointsImpl tablesImpl

var _ persistence.BreakpointTable = (*breakpointsImpl)(nil)

func (s *tablesImpl) Breakpoints() persistence.BreakpointTable { return (*breakpointsImpl)(s) }

func (b *breakpointsImpl) q(tx persistence.Tx) querier { return (*tablesImpl)(b).q(tx) }

const breakpointCols = `id, instance_id, matcher, checkpoint, signal_type, mode,
	overflow_policy, hit_ttl_seconds, ttl_seconds, dropped_count,
	created_by_key, created_at, expires_at`

func (b *breakpointsImpl) Create(ctx context.Context, bp persistence.BreakpointRow, tx persistence.Tx) (shared.UUID, error) {
	ex := b.q(tx)
	matcher := bp.Matcher
	if matcher == nil {
		matcher = map[string]any{}
	}
	matcherBytes, err := json.Marshal(matcher)
	if err != nil {
		return shared.UUID{}, fmt.Errorf("breakpoints.create: marshal matcher: %w", err)
	}
	var idArg any
	if bp.ID != (shared.UUID{}) {
		idArg = bp.ID
	}
	var ttlArg any
	if bp.TTLSeconds != nil {
		ttlArg = *bp.TTLSeconds
	}
	var sigArg any
	if bp.SignalType != nil {
		sigArg = *bp.SignalType
	}
	createdAt := bp.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	var expiresAtArg any
	if bp.TTLSeconds != nil {
		expiresAtArg = createdAt.Add(time.Duration(*bp.TTLSeconds) * time.Second)
	}
	var id shared.UUID
	err = ex.QueryRow(ctx,
		`INSERT INTO rimsky_instance_breakpoints
		   (id, instance_id, matcher, checkpoint, signal_type, mode,
		    overflow_policy, hit_ttl_seconds, ttl_seconds, dropped_count,
		    created_by_key, created_at, expires_at)
		 VALUES (
		    COALESCE($1::uuid, gen_random_uuid()),
		    $2, $3, $4, $5, $6,
		    $7, $8, $9, $10,
		    $11, $12, $13
		 )
		 RETURNING id`,
		idArg, bp.InstanceID, matcherBytes, string(bp.Checkpoint), sigArg, string(bp.Mode),
		string(bp.OverflowPolicy), bp.HitTTLSeconds, ttlArg, bp.DroppedCount,
		bp.CreatedByKey, createdAt, expiresAtArg,
	).Scan(&id)
	if err != nil {
		return shared.UUID{}, fmt.Errorf("breakpoints.create: %w", err)
	}
	return id, nil
}

func (b *breakpointsImpl) Get(ctx context.Context, id shared.UUID, tx persistence.Tx) (*persistence.BreakpointRow, error) {
	ex := b.q(tx)
	row := ex.QueryRow(ctx,
		`SELECT `+breakpointCols+`
		   FROM rimsky_instance_breakpoints
		  WHERE id = $1`, id)
	out, err := scanBreakpoint(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("breakpoints.get: %w", err)
	}
	return &out, nil
}

func (b *breakpointsImpl) ListForInstance(ctx context.Context, instanceID shared.UUID, includeExpired bool, now time.Time, tx persistence.Tx) ([]persistence.BreakpointRow, error) {
	ex := b.q(tx)
	args := []any{instanceID}
	sql := `SELECT ` + breakpointCols + `
		    FROM rimsky_instance_breakpoints
		   WHERE instance_id = $1`
	if !includeExpired {
		args = append(args, now)
		sql += fmt.Sprintf(` AND (expires_at IS NULL OR expires_at > $%d)`, len(args))
	}
	sql += ` ORDER BY created_at ASC`
	rows, err := ex.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("breakpoints.listForInstance: %w", err)
	}
	defer rows.Close()
	out := []persistence.BreakpointRow{}
	for rows.Next() {
		r, err := scanBreakpoint(rows)
		if err != nil {
			return nil, fmt.Errorf("breakpoints.listForInstance.scan: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (b *breakpointsImpl) Delete(ctx context.Context, id shared.UUID, tx persistence.Tx) error {
	ex := b.q(tx)
	if _, err := ex.Exec(ctx,
		`DELETE FROM rimsky_instance_breakpoints WHERE id = $1`, id); err != nil {
		return fmt.Errorf("breakpoints.delete: %w", err)
	}
	return nil
}

func (b *breakpointsImpl) Lock(ctx context.Context, id shared.UUID, tx persistence.Tx) error {
	ex := b.q(tx)
	if _, err := ex.Exec(ctx,
		`SELECT id FROM rimsky_instance_breakpoints WHERE id = $1 FOR UPDATE`, id); err != nil {
		return fmt.Errorf("breakpoints.lock: %w", err)
	}
	return nil
}

func (b *breakpointsImpl) IncrementDropped(ctx context.Context, id shared.UUID, tx persistence.Tx) error {
	ex := b.q(tx)
	if _, err := ex.Exec(ctx,
		`UPDATE rimsky_instance_breakpoints
		    SET dropped_count = dropped_count + 1
		  WHERE id = $1`, id); err != nil {
		return fmt.Errorf("breakpoints.incrementDropped: %w", err)
	}
	return nil
}

func (b *breakpointsImpl) SweepExpired(ctx context.Context, now time.Time, tx persistence.Tx) (int, error) {
	ex := b.q(tx)
	tag, err := ex.Exec(ctx,
		`DELETE FROM rimsky_instance_breakpoints
		  WHERE expires_at IS NOT NULL AND expires_at <= $1`, now)
	if err != nil {
		return 0, fmt.Errorf("breakpoints.sweepExpired: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

func scanBreakpoint(sc scannable) (persistence.BreakpointRow, error) {
	var (
		id             shared.UUID
		instanceID     shared.UUID
		matcherBytes   []byte
		checkpoint     string
		signalType     *string
		mode           string
		overflowPolicy string
		hitTTLSeconds  int
		ttlSeconds     *int
		droppedCount   int64
		createdByKey   string
		createdAt      time.Time
		expiresAt      *time.Time
	)
	if err := sc.Scan(&id, &instanceID, &matcherBytes, &checkpoint, &signalType, &mode,
		&overflowPolicy, &hitTTLSeconds, &ttlSeconds, &droppedCount,
		&createdByKey, &createdAt, &expiresAt); err != nil {
		return persistence.BreakpointRow{}, err
	}
	matcher := map[string]any{}
	if len(matcherBytes) > 0 {
		if err := json.Unmarshal(matcherBytes, &matcher); err != nil {
			return persistence.BreakpointRow{}, fmt.Errorf("unmarshal matcher: %w", err)
		}
	}
	return persistence.BreakpointRow{
		ID:             id,
		InstanceID:     instanceID,
		Matcher:        matcher,
		Checkpoint:     persistence.BreakpointCheckpoint(checkpoint),
		SignalType:     signalType,
		Mode:           persistence.BreakpointMode(mode),
		OverflowPolicy: persistence.BreakpointOverflowPolicy(overflowPolicy),
		HitTTLSeconds:  hitTTLSeconds,
		TTLSeconds:     ttlSeconds,
		DroppedCount:   droppedCount,
		CreatedByKey:   createdByKey,
		CreatedAt:      createdAt,
		ExpiresAt:      expiresAt,
	}, nil
}
