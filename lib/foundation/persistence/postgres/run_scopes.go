// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// run_scopes.go is the postgres impl of `persistence.RunScopeTable` —
// CRUD + tree-walks on rimsky_run_scopes, the first-class execution-
// context table backing concept:run-scope. Spec
// .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md.
//
// @concept: run-scope

package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

type runScopesImpl tablesImpl

var _ persistence.RunScopeTable = (*runScopesImpl)(nil)

// RunScopes exposes the rimsky_run_scopes accessor.
func (s *tablesImpl) RunScopes() persistence.RunScopeTable { return (*runScopesImpl)(s) }

func (b *runScopesImpl) q(tx persistence.Tx) querier { return (*tablesImpl)(b).q(tx) }

const runScopeCols = `id, parent_run_scope_id, parent_run_id, graph_name, partition_key, instance_id, created_at, closed_at`

func (b *runScopesImpl) Create(ctx context.Context, tx persistence.Tx, row persistence.RunScopeRow) error {
	// CreatedAt: zero value → fall through to DB default NOW() via COALESCE.
	var createdAt any
	if !row.CreatedAt.IsZero() {
		createdAt = row.CreatedAt
	}
	_, err := b.q(tx).Exec(ctx,
		`INSERT INTO rimsky_run_scopes
		   (id, parent_run_scope_id, parent_run_id, graph_name, partition_key, instance_id, created_at, closed_at)
		 VALUES ($1, $2, $3, $4, $5, $6, COALESCE($7, NOW()), $8)`,
		row.ID, row.ParentRunScopeID, row.ParentRunID, row.GraphName, row.PartitionKey,
		row.InstanceID, createdAt, row.ClosedAt)
	if err != nil {
		return fmt.Errorf("runScopes.Create: %w", err)
	}
	return nil
}

func (b *runScopesImpl) GetByID(ctx context.Context, tx persistence.Tx, id shared.UUID) (*persistence.RunScopeRow, error) {
	r, err := scanRunScopeRow(b.q(tx).QueryRow(ctx,
		`SELECT `+runScopeCols+` FROM rimsky_run_scopes WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("runScopes.GetByID: %w", err)
	}
	return &r, nil
}

func (b *runScopesImpl) GetFanoutPartition(ctx context.Context, tx persistence.Tx, parentRunID shared.UUID, partitionKey string) (*persistence.RunScopeRow, error) {
	r, err := scanRunScopeRow(b.q(tx).QueryRow(ctx,
		`SELECT `+runScopeCols+` FROM rimsky_run_scopes
		   WHERE parent_run_id = $1 AND partition_key = $2 AND closed_at IS NULL`,
		parentRunID, partitionKey))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("runScopes.GetFanoutPartition: %w", err)
	}
	return &r, nil
}

func (b *runScopesImpl) Close(ctx context.Context, tx persistence.Tx, id shared.UUID) error {
	_, err := b.q(tx).Exec(ctx,
		`UPDATE rimsky_run_scopes SET closed_at = NOW() WHERE id = $1 AND closed_at IS NULL`, id)
	if err != nil {
		return fmt.Errorf("runScopes.Close: %w", err)
	}
	return nil
}

func (b *runScopesImpl) ListChildScopes(ctx context.Context, tx persistence.Tx, parentRunID shared.UUID) ([]persistence.RunScopeRow, error) {
	rows, err := b.q(tx).Query(ctx,
		`SELECT `+runScopeCols+` FROM rimsky_run_scopes WHERE parent_run_id = $1 ORDER BY created_at`, parentRunID)
	if err != nil {
		return nil, fmt.Errorf("runScopes.ListChildScopes: %w", err)
	}
	defer rows.Close()
	var out []persistence.RunScopeRow
	for rows.Next() {
		r, err := scanRunScopeRow(rows)
		if err != nil {
			return nil, fmt.Errorf("runScopes.ListChildScopes scan: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("runScopes.ListChildScopes iter: %w", err)
	}
	return out, nil
}

func (b *runScopesImpl) ListParentChain(ctx context.Context, tx persistence.Tx, id shared.UUID) ([]persistence.RunScopeRow, error) {
	rows, err := b.q(tx).Query(ctx,
		`WITH RECURSIVE chain AS (
		     SELECT `+runScopeCols+`, 0 AS depth FROM rimsky_run_scopes WHERE id = $1
		   UNION ALL
		     SELECT rs.id, rs.parent_run_scope_id, rs.parent_run_id, rs.graph_name,
		            rs.partition_key, rs.instance_id, rs.created_at, rs.closed_at,
		            chain.depth + 1
		       FROM rimsky_run_scopes rs
		       JOIN chain ON rs.id = chain.parent_run_scope_id
		 )
		 SELECT `+runScopeCols+` FROM chain ORDER BY depth`, id)
	if err != nil {
		return nil, fmt.Errorf("runScopes.ListParentChain: %w", err)
	}
	defer rows.Close()
	var out []persistence.RunScopeRow
	for rows.Next() {
		r, err := scanRunScopeRow(rows)
		if err != nil {
			return nil, fmt.Errorf("runScopes.ListParentChain scan: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("runScopes.ListParentChain iter: %w", err)
	}
	return out, nil
}

type runScopeScanner interface {
	Scan(dest ...any) error
}

func scanRunScopeRow(s runScopeScanner) (persistence.RunScopeRow, error) {
	var r persistence.RunScopeRow
	err := s.Scan(&r.ID, &r.ParentRunScopeID, &r.ParentRunID, &r.GraphName,
		&r.PartitionKey, &r.InstanceID, &r.CreatedAt, &r.ClosedAt)
	return r, err
}
