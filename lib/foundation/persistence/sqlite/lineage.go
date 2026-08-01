// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package sqlite

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

type lineageImpl tablesImpl

var _ persistence.LineageTable = (*lineageImpl)(nil)

func (s *tablesImpl) Lineage() persistence.LineageTable { return (*lineageImpl)(s) }

func (b *lineageImpl) q(tx persistence.Tx) querier { return (*tablesImpl)(b).q(tx) }

const sqliteInsertLineageSQL = `
INSERT INTO rimsky_lineage (
    id, record_kind, instance_id, frame_id, observed_at, record, outcome
) VALUES (?, ?, ?, ?, ?, ?, ?)`

func (b *lineageImpl) Insert(ctx context.Context, row persistence.LineageRow, tx persistence.Tx) error {
	if row.ObservedAt.IsZero() {
		row.ObservedAt = time.Now().UTC()
	}
	_, err := b.q(tx).ExecContext(ctx, sqliteInsertLineageSQL,
		row.ID.String(), row.RecordKind, row.InstanceID.String(),
		row.FrameID.String(), formatTime(row.ObservedAt), row.Record, row.Outcome)
	if err != nil {
		return fmt.Errorf("sqlite.Lineage.Insert: %w", err)
	}
	return nil
}

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
	if pag.Cursor != "" {
		cursorObserved, cursorID, err := decodeLineageCursor(pag.Cursor)
		if err != nil {
			return persistence.PaginatedListResult[persistence.LineageRow]{}, fmt.Errorf("sqlite.Lineage.Query: bad cursor: %w", err)
		}
		args = append(args, formatTime(cursorObserved), cursorID.String())
		conds = append(conds, "(observed_at, id) < (?, ?)")
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
	var nextCursor string
	if len(out) == limit && len(out) > 0 {
		last := out[len(out)-1]
		nextCursor = encodeLineageCursor(last.ObservedAt, last.ID)
	}
	return persistence.PaginatedListResult[persistence.LineageRow]{Rows: out, NextCursor: nextCursor}, nil
}

type lineageCursor struct {
	O time.Time `json:"o"`
	I string    `json:"i"`
}

func encodeLineageCursor(observedAt time.Time, id shared.UUID) string {
	c := lineageCursor{O: observedAt, I: id.String()}
	b, _ := json.Marshal(c)
	return base64.StdEncoding.EncodeToString(b)
}

func decodeLineageCursor(s string) (time.Time, shared.UUID, error) {
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return time.Time{}, shared.UUID{}, err
	}
	var c lineageCursor
	if err := json.Unmarshal(raw, &c); err != nil {
		return time.Time{}, shared.UUID{}, err
	}
	id, err := uuid.Parse(c.I)
	if err != nil {
		return time.Time{}, shared.UUID{}, err
	}
	return c.O, shared.UUID(id), nil
}

const sqliteQueryByParentRunIDSQL = `
SELECT id, record_kind, instance_id, frame_id, observed_at, record, outcome
  FROM rimsky_lineage
 WHERE record_kind = 'leaf_run' AND json_extract(record, '$.parent_run_id') = ?
 ORDER BY observed_at ASC, id ASC
 LIMIT ?`

func (b *lineageImpl) QueryByParentNodeRunID(ctx context.Context, parentNodeRunID shared.UUID, limit int) ([]persistence.LineageRow, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := (*tablesImpl)(b).db.QueryContext(ctx, sqliteQueryByParentRunIDSQL, parentNodeRunID.String(), limit)
	if err != nil {
		return nil, fmt.Errorf("sqlite.Lineage.QueryByParentNodeRunID: %w", err)
	}
	defer rows.Close()
	return scanLineage(rows)
}

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
