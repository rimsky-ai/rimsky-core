// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: run-scope
// @source: lib/foundation/persistence/postgres/run_scopes.go
// @diverged: true
// @reason: parallel driver — SQLite dialect (positional ? params, database/sql) vs Postgres (pgx, $-params)
// @constraint: SQLite is dev-only; multi-host deployments must use postgres. Do not add multi-host coordination to the sqlite driver.

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

// runScopesImpl is the SQLite-backed persistence.RunScopeTable — CRUD +
// tree-walks on rimsky_run_scopes, the first-class execution-context
// table backing concept:run-scope.
type runScopesImpl tablesImpl

var _ persistence.RunScopeTable = (*runScopesImpl)(nil)

// RunScopes exposes the rimsky_run_scopes accessor.
func (s *tablesImpl) RunScopes() persistence.RunScopeTable { return (*runScopesImpl)(s) }

func (b *runScopesImpl) q(tx persistence.Tx) querier { return (*tablesImpl)(b).q(tx) }

const sqliteRunScopeCols = `id, parent_run_scope_id, parent_run_id, graph_name, partition_key, instance_id, created_at, closed_at`

func (b *runScopesImpl) Create(ctx context.Context, tx persistence.Tx, row persistence.RunScopeRow) error {
	var parentScope any
	if row.ParentRunScopeID != nil {
		parentScope = row.ParentRunScopeID.String()
	}
	var parentRun any
	if row.ParentRunID != nil {
		parentRun = row.ParentRunID.String()
	}
	createdAt := row.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	var closedAt any
	if row.ClosedAt != nil {
		closedAt = formatTime(*row.ClosedAt)
	}
	_, err := b.q(tx).ExecContext(ctx,
		`INSERT INTO rimsky_run_scopes
		   (id, parent_run_scope_id, parent_run_id, graph_name, partition_key, instance_id, created_at, closed_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		row.ID.String(), parentScope, parentRun, row.GraphName, row.PartitionKey,
		row.InstanceID.String(), formatTime(createdAt), closedAt)
	if err != nil {
		return fmt.Errorf("sqlite.runScopes.Create: %w", err)
	}
	return nil
}

func (b *runScopesImpl) GetByID(ctx context.Context, tx persistence.Tx, id shared.UUID) (*persistence.RunScopeRow, error) {
	r, err := scanSqliteRunScopeRow(b.q(tx).QueryRowContext(ctx,
		`SELECT `+sqliteRunScopeCols+` FROM rimsky_run_scopes WHERE id = ?`, id.String()))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite.runScopes.GetByID: %w", err)
	}
	return r, nil
}

func (b *runScopesImpl) GetFanoutPartition(ctx context.Context, tx persistence.Tx, parentRunID shared.UUID, partitionKey string) (*persistence.RunScopeRow, error) {
	r, err := scanSqliteRunScopeRow(b.q(tx).QueryRowContext(ctx,
		`SELECT `+sqliteRunScopeCols+` FROM rimsky_run_scopes
		   WHERE parent_run_id = ? AND partition_key = ? AND closed_at IS NULL`,
		parentRunID.String(), partitionKey))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite.runScopes.GetFanoutPartition: %w", err)
	}
	return r, nil
}

func (b *runScopesImpl) Close(ctx context.Context, tx persistence.Tx, id shared.UUID) error {
	_, err := b.q(tx).ExecContext(ctx,
		`UPDATE rimsky_run_scopes SET closed_at = ? WHERE id = ? AND closed_at IS NULL`,
		formatTime(time.Now().UTC()), id.String())
	if err != nil {
		return fmt.Errorf("sqlite.runScopes.Close: %w", err)
	}
	return nil
}

func (b *runScopesImpl) ListChildScopes(ctx context.Context, tx persistence.Tx, parentRunID shared.UUID) ([]persistence.RunScopeRow, error) {
	rows, err := b.q(tx).QueryContext(ctx,
		`SELECT `+sqliteRunScopeCols+` FROM rimsky_run_scopes WHERE parent_run_id = ? ORDER BY created_at`,
		parentRunID.String())
	if err != nil {
		return nil, fmt.Errorf("sqlite.runScopes.ListChildScopes: %w", err)
	}
	defer rows.Close()
	var out []persistence.RunScopeRow
	for rows.Next() {
		r, err := scanSqliteRunScopeRow(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite.runScopes.ListChildScopes scan: %w", err)
		}
		out = append(out, *r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite.runScopes.ListChildScopes iter: %w", err)
	}
	return out, nil
}

func (b *runScopesImpl) ListParentChain(ctx context.Context, tx persistence.Tx, id shared.UUID) ([]persistence.RunScopeRow, error) {
	rows, err := b.q(tx).QueryContext(ctx,
		`WITH RECURSIVE chain AS (
		     SELECT `+sqliteRunScopeCols+`, 0 AS depth FROM rimsky_run_scopes WHERE id = ?
		   UNION ALL
		     SELECT rs.id, rs.parent_run_scope_id, rs.parent_run_id, rs.graph_name,
		            rs.partition_key, rs.instance_id, rs.created_at, rs.closed_at,
		            chain.depth + 1
		       FROM rimsky_run_scopes rs
		       JOIN chain ON rs.id = chain.parent_run_scope_id
		 )
		 SELECT `+sqliteRunScopeCols+` FROM chain ORDER BY depth`,
		id.String())
	if err != nil {
		return nil, fmt.Errorf("sqlite.runScopes.ListParentChain: %w", err)
	}
	defer rows.Close()
	var out []persistence.RunScopeRow
	for rows.Next() {
		r, err := scanSqliteRunScopeRow(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite.runScopes.ListParentChain scan: %w", err)
		}
		out = append(out, *r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite.runScopes.ListParentChain iter: %w", err)
	}
	return out, nil
}

func scanSqliteRunScopeRow(s scannable) (*persistence.RunScopeRow, error) {
	var (
		idStr               string
		parentRunScopeIDStr sql.NullString
		parentRunIDStr      sql.NullString
		graphName           string
		partitionKey        string
		instanceIDStr       string
		createdAtStr        string
		closedAtStr         sql.NullString
	)
	if err := s.Scan(&idStr, &parentRunScopeIDStr, &parentRunIDStr, &graphName,
		&partitionKey, &instanceIDStr, &createdAtStr, &closedAtStr); err != nil {
		return nil, err
	}
	out := &persistence.RunScopeRow{
		GraphName:    graphName,
		PartitionKey: partitionKey,
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		return nil, fmt.Errorf("parse id: %w", err)
	}
	out.ID = shared.UUID(id)
	if parentRunScopeIDStr.Valid {
		pid, err := uuid.Parse(parentRunScopeIDStr.String)
		if err != nil {
			return nil, fmt.Errorf("parse parent_run_scope_id: %w", err)
		}
		p := shared.UUID(pid)
		out.ParentRunScopeID = &p
	}
	if parentRunIDStr.Valid {
		pid, err := uuid.Parse(parentRunIDStr.String)
		if err != nil {
			return nil, fmt.Errorf("parse parent_run_id: %w", err)
		}
		p := shared.UUID(pid)
		out.ParentRunID = &p
	}
	iid, err := uuid.Parse(instanceIDStr)
	if err != nil {
		return nil, fmt.Errorf("parse instance_id: %w", err)
	}
	out.InstanceID = shared.UUID(iid)
	createdAt, err := parseTime(createdAtStr)
	if err != nil {
		return nil, fmt.Errorf("parse created_at: %w", err)
	}
	out.CreatedAt = createdAt
	if closedAtStr.Valid {
		closedAt, err := parseTime(closedAtStr.String)
		if err != nil {
			return nil, fmt.Errorf("parse closed_at: %w", err)
		}
		out.ClosedAt = &closedAt
	}
	return out, nil
}
