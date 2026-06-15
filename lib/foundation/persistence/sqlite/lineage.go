// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @source: lib/foundation/persistence/postgres/lineage.go
// @diverged: true
// @reason: parallel driver — SQLite dialect (positional ? params, database/sql) vs Postgres (pgx, $-params)

package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

// lineageImpl is the SQLite-backed persistence.LineageTable — the
// content lineage projection per spec §Content lineage.
type lineageImpl tablesImpl

var _ persistence.LineageTable = (*lineageImpl)(nil)

func (s *tablesImpl) Lineage() persistence.LineageTable { return (*lineageImpl)(s) }

func (b *lineageImpl) q(tx persistence.Tx) querier { return (*tablesImpl)(b).q(tx) }

const sqliteInsertLineageSQL = `
INSERT INTO rimsky_lineage (
    id, record_kind, instance_id, frame_id, observed_at, record, outcome
) VALUES (?, ?, ?, ?, ?, ?, ?)`

func (b *lineageImpl) Insert(ctx context.Context, tx persistence.Tx, row persistence.LineageRow) error {
	if row.ObservedAt.IsZero() {
		row.ObservedAt = time.Now().UTC()
	}
	// @constraint: `leaf_run` rows carry outcome="" by design; `claim_terminal`
	// writers (`runtime.WriteClaimTerminalLineage`) reject empty outcome at the
	// call site. Pass row.Outcome through verbatim.
	// @constraint: observed_at goes through formatTime (fixed-width UTC text) —
	// a raw time.Time bind would store modernc's zone-embedded t.String() form,
	// which breaks the retention DELETE / ORDER BY / range filters on non-UTC
	// hosts.
	_, err := b.q(tx).ExecContext(ctx, sqliteInsertLineageSQL,
		row.ID.String(), row.RecordKind, row.InstanceID.String(),
		row.FrameID.String(), formatTime(row.ObservedAt), row.Record, row.Outcome)
	if err != nil {
		return fmt.Errorf("sqlite.Lineage.Insert: %w", err)
	}
	return nil
}

// sqliteGetByRunIDSQL queries by JSON extraction; SQLite supports json_extract.
const sqliteGetByRunIDSQL = `
SELECT id, record_kind, instance_id, frame_id, observed_at, record, outcome
  FROM rimsky_lineage
 WHERE record_kind = 'leaf_run' AND json_extract(record, '$.run_id') = ?
 ORDER BY observed_at ASC, id ASC`

func (b *lineageImpl) GetByRunID(ctx context.Context, runID shared.UUID) ([]persistence.LineageRow, error) {
	rows, err := (*tablesImpl)(b).db.QueryContext(ctx, sqliteGetByRunIDSQL, runID.String())
	if err != nil {
		return nil, fmt.Errorf("sqlite.Lineage.GetByRunID: %w", err)
	}
	defer rows.Close()
	return scanLineage(rows)
}

const sqliteGetByClaimHandleIDSQL = `
SELECT id, record_kind, instance_id, frame_id, observed_at, record, outcome
  FROM rimsky_lineage
 WHERE record_kind = 'claim_terminal' AND json_extract(record, '$.claim_handle_id') = ?
 ORDER BY observed_at ASC, id ASC`

func (b *lineageImpl) GetByClaimHandleID(ctx context.Context, handleID shared.UUID) ([]persistence.LineageRow, error) {
	rows, err := (*tablesImpl)(b).db.QueryContext(ctx, sqliteGetByClaimHandleIDSQL, handleID.String())
	if err != nil {
		return nil, fmt.Errorf("sqlite.Lineage.GetByClaimHandleID: %w", err)
	}
	defer rows.Close()
	return scanLineage(rows)
}

func (b *lineageImpl) Query(ctx context.Context, q persistence.LineageQuery, pag persistence.ListPagination) (persistence.PaginatedListResult[persistence.LineageRow], error) {
	args := []any{}
	conds := []string{"1 = 1"}
	if q.InstanceID != nil {
		args = append(args, q.InstanceID.String())
		conds = append(conds, "instance_id = ?")
	}
	if q.Kind != "" {
		args = append(args, q.Kind)
		conds = append(conds, "record_kind = ?")
	}
	if q.ObservedAfter != nil {
		args = append(args, formatTime(*q.ObservedAfter))
		conds = append(conds, "observed_at > ?")
	}
	if q.ObservedBefore != nil {
		args = append(args, formatTime(*q.ObservedBefore))
		conds = append(conds, "observed_at < ?")
	}
	limit := pag.Limit
	if limit <= 0 {
		limit = 100
	}
	args = append(args, limit)
	sql := fmt.Sprintf(`SELECT id, record_kind, instance_id, frame_id, observed_at, record, outcome
  FROM rimsky_lineage
 WHERE %s
 ORDER BY observed_at DESC, id DESC
 LIMIT ?`, strings.Join(conds, " AND "))
	rows, err := (*tablesImpl)(b).db.QueryContext(ctx, sql, args...)
	if err != nil {
		return persistence.PaginatedListResult[persistence.LineageRow]{}, fmt.Errorf("sqlite.Lineage.Query: %w", err)
	}
	defer rows.Close()
	out, err := scanLineage(rows)
	if err != nil {
		return persistence.PaginatedListResult[persistence.LineageRow]{}, err
	}
	return persistence.PaginatedListResult[persistence.LineageRow]{Rows: out}, nil
}

const sqliteQueryByParentRunIDSQL = `
SELECT id, record_kind, instance_id, frame_id, observed_at, record, outcome
  FROM rimsky_lineage
 WHERE record_kind = 'leaf_run' AND json_extract(record, '$.parent_run_id') = ?
 ORDER BY observed_at ASC, id ASC
 LIMIT ?`

func (b *lineageImpl) QueryByParentRunID(ctx context.Context, parentRunID shared.UUID, limit int) ([]persistence.LineageRow, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := (*tablesImpl)(b).db.QueryContext(ctx, sqliteQueryByParentRunIDSQL, parentRunID.String(), limit)
	if err != nil {
		return nil, fmt.Errorf("sqlite.Lineage.QueryByParentRunID: %w", err)
	}
	defer rows.Close()
	return scanLineage(rows)
}

// sqliteLineagePruneWhereSQL is the shared predicate for both
// DeleteOlderThan (the live prune) and CountOlderThan (the dry-run
// preview): rows older than the cutoff whose corresponding run AND
// claim_handle are no longer present. Defined once so the dry-run count
// is a true preview of the live delete — keeping the WHERE clause
// byte-identical across the two statements is the load-bearing
// invariant (see persistence.LineageTable.CountOlderThan doc).
const sqliteLineagePruneWhereSQL = `
 WHERE observed_at < ?
   AND NOT EXISTS (
       SELECT 1 FROM rimsky_node_runs r
        WHERE r.id = json_extract(rimsky_lineage.record, '$.run_id')
   )
   AND NOT EXISTS (
       SELECT 1 FROM rimsky_claim_handles c
        WHERE c.id = json_extract(rimsky_lineage.record, '$.claim_handle_id')
   )`

const sqliteDeleteOlderThanSQL = `DELETE FROM rimsky_lineage` + sqliteLineagePruneWhereSQL

func (b *lineageImpl) DeleteOlderThan(ctx context.Context, cutoff time.Time) (int, error) {
	res, err := (*tablesImpl)(b).db.ExecContext(ctx, sqliteDeleteOlderThanSQL, formatTime(cutoff))
	if err != nil {
		return 0, fmt.Errorf("sqlite.Lineage.DeleteOlderThan: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

const sqliteCountOlderThanSQL = `SELECT count(*) FROM rimsky_lineage` + sqliteLineagePruneWhereSQL

func (b *lineageImpl) CountOlderThan(ctx context.Context, cutoff time.Time) (int, error) {
	var n int
	if err := (*tablesImpl)(b).db.QueryRowContext(ctx, sqliteCountOlderThanSQL, formatTime(cutoff)).Scan(&n); err != nil {
		return 0, fmt.Errorf("sqlite.Lineage.CountOlderThan: %w", err)
	}
	return n, nil
}

func scanLineage(rows *sql.Rows) ([]persistence.LineageRow, error) {
	out := []persistence.LineageRow{}
	for rows.Next() {
		var r persistence.LineageRow
		var idStr, instanceStr, frameStr string
		// @constraint: observed_at is fixed-width UTC TEXT; scan as a string and
		// run through parseTime (per queue_park.go::LoadResumeMetadataInTx)
		// instead of scanning into time.Time.
		var observedAtStr string
		if err := rows.Scan(
			&idStr, &r.RecordKind, &instanceStr, &frameStr,
			&observedAtStr, &r.Record, &r.Outcome,
		); err != nil {
			return nil, err
		}
		observedAt, err := parseTime(observedAtStr)
		if err != nil {
			return nil, fmt.Errorf("scan lineage observed_at: %w", err)
		}
		r.ObservedAt = observedAt
		u, err := uuid.Parse(idStr)
		if err != nil {
			return nil, fmt.Errorf("scan lineage uuid (id): %w", err)
		}
		r.ID = u
		u, err = uuid.Parse(instanceStr)
		if err != nil {
			return nil, fmt.Errorf("scan lineage uuid (instance_id): %w", err)
		}
		r.InstanceID = u
		u, err = uuid.Parse(frameStr)
		if err != nil {
			return nil, fmt.Errorf("scan lineage uuid (frame_id): %w", err)
		}
		r.FrameID = u
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
