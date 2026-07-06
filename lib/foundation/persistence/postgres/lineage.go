// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

type lineageImpl tablesImpl

var _ persistence.LineageTable = (*lineageImpl)(nil)

func (s *tablesImpl) Lineage() persistence.LineageTable { return (*lineageImpl)(s) }

func (b *lineageImpl) q(tx persistence.Tx) querier { return (*tablesImpl)(b).q(tx) }

const insertLineageSQL = `
INSERT INTO rimsky_lineage (
    id, record_kind, instance_id, frame_id, observed_at, record, outcome
) VALUES ($1, $2, $3, $4, $5, $6, $7)`

func (b *lineageImpl) Insert(ctx context.Context, tx persistence.Tx, row persistence.LineageRow) error {
	if row.ObservedAt.IsZero() {
		row.ObservedAt = time.Now().UTC()
	}
	_, err := b.q(tx).Exec(ctx, insertLineageSQL,
		row.ID, row.RecordKind, row.InstanceID, row.FrameID,
		row.ObservedAt, row.Record, row.Outcome)
	if err != nil {
		return fmt.Errorf("postgres.Lineage.Insert: %w", err)
	}
	return nil
}

const getByRunIDSQL = `
SELECT id, record_kind, instance_id, frame_id, observed_at, record, outcome
  FROM rimsky_lineage
 WHERE record_kind = 'leaf_run' AND record->>'run_id' = $1
 ORDER BY observed_at ASC, id ASC`

func (b *lineageImpl) GetByRunID(ctx context.Context, runID shared.UUID) ([]persistence.LineageRow, error) {
	rows, err := (*tablesImpl)(b).pool.Query(ctx, getByRunIDSQL, runID.String())
	if err != nil {
		return nil, fmt.Errorf("postgres.Lineage.GetByRunID: %w", err)
	}
	defer rows.Close()
	return collectLineage(rows)
}

const getByClaimHandleIDSQL = `
SELECT id, record_kind, instance_id, frame_id, observed_at, record, outcome
  FROM rimsky_lineage
 WHERE record_kind = 'claim_terminal' AND record->>'claim_handle_id' = $1
 ORDER BY observed_at ASC, id ASC`

func (b *lineageImpl) GetByClaimHandleID(ctx context.Context, handleID shared.UUID) ([]persistence.LineageRow, error) {
	rows, err := (*tablesImpl)(b).pool.Query(ctx, getByClaimHandleIDSQL, handleID.String())
	if err != nil {
		return nil, fmt.Errorf("postgres.Lineage.GetByClaimHandleID: %w", err)
	}
	defer rows.Close()
	return collectLineage(rows)
}

func (b *lineageImpl) Query(ctx context.Context, q persistence.LineageQuery, pag persistence.ListPagination) (persistence.PaginatedListResult[persistence.LineageRow], error) {
	args := []any{}
	where := "WHERE TRUE"
	if q.InstanceID != nil {
		args = append(args, *q.InstanceID)
		where += fmt.Sprintf(" AND instance_id = $%d", len(args))
	}
	if q.Kind != "" {
		args = append(args, q.Kind)
		where += fmt.Sprintf(" AND record_kind = $%d", len(args))
	}
	if q.ObservedAfter != nil {
		args = append(args, *q.ObservedAfter)
		where += fmt.Sprintf(" AND observed_at > $%d", len(args))
	}
	if q.ObservedBefore != nil {
		args = append(args, *q.ObservedBefore)
		where += fmt.Sprintf(" AND observed_at < $%d", len(args))
	}
	limit := pag.Limit
	if limit <= 0 {
		limit = 100
	}
	args = append(args, limit)
	sql := fmt.Sprintf(`SELECT id, record_kind, instance_id, frame_id, observed_at, record, outcome
  FROM rimsky_lineage %s
 ORDER BY observed_at DESC, id DESC
 LIMIT $%d`, where, len(args))
	rows, err := (*tablesImpl)(b).pool.Query(ctx, sql, args...)
	if err != nil {
		return persistence.PaginatedListResult[persistence.LineageRow]{}, fmt.Errorf("postgres.Lineage.Query: %w", err)
	}
	defer rows.Close()
	out, err := collectLineage(rows)
	if err != nil {
		return persistence.PaginatedListResult[persistence.LineageRow]{}, err
	}
	return persistence.PaginatedListResult[persistence.LineageRow]{Rows: out}, nil
}

const queryByParentRunIDSQL = `
SELECT id, record_kind, instance_id, frame_id, observed_at, record, outcome
  FROM rimsky_lineage
 WHERE record_kind = 'leaf_run' AND record->>'parent_run_id' = $1
 ORDER BY observed_at ASC, id ASC
 LIMIT $2`

func (b *lineageImpl) QueryByParentNodeRunID(ctx context.Context, parentNodeRunID shared.UUID, limit int) ([]persistence.LineageRow, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := (*tablesImpl)(b).pool.Query(ctx, queryByParentRunIDSQL, parentNodeRunID.String(), limit)
	if err != nil {
		return nil, fmt.Errorf("postgres.Lineage.QueryByParentNodeRunID: %w", err)
	}
	defer rows.Close()
	return collectLineage(rows)
}

const lineagePruneWhereSQL = `
 WHERE observed_at < $1
   AND NOT EXISTS (
       SELECT 1 FROM rimsky_node_runs r WHERE r.id::text = l.record->>'run_id'
   )
   AND NOT EXISTS (
       SELECT 1 FROM rimsky_claim_handles c WHERE c.id::text = l.record->>'claim_handle_id'
   )`

const deleteOlderThanSQL = `DELETE FROM rimsky_lineage l` + lineagePruneWhereSQL

func (b *lineageImpl) DeleteOlderThan(ctx context.Context, cutoff time.Time) (int, error) {
	tag, err := (*tablesImpl)(b).pool.Exec(ctx, deleteOlderThanSQL, cutoff)
	if err != nil {
		return 0, fmt.Errorf("postgres.Lineage.DeleteOlderThan: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

const countOlderThanSQL = `SELECT count(*) FROM rimsky_lineage l` + lineagePruneWhereSQL

func (b *lineageImpl) CountOlderThan(ctx context.Context, cutoff time.Time) (int, error) {
	var n int
	if err := (*tablesImpl)(b).pool.QueryRow(ctx, countOlderThanSQL, cutoff).Scan(&n); err != nil {
		return 0, fmt.Errorf("postgres.Lineage.CountOlderThan: %w", err)
	}
	return n, nil
}

func collectLineage(rows pgx.Rows) ([]persistence.LineageRow, error) {
	out := []persistence.LineageRow{}
	for rows.Next() {
		var r persistence.LineageRow
		if err := rows.Scan(
			&r.ID, &r.RecordKind, &r.InstanceID, &r.FrameID,
			&r.ObservedAt, &r.Record, &r.Outcome,
		); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
