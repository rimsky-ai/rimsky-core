// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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

func (s *tablesImpl) RunScopes() persistence.RunScopeTable { return (*runScopesImpl)(s) }

func (b *runScopesImpl) q(tx persistence.Tx) querier { return (*tablesImpl)(b).q(tx) }

const runScopeCols = `id, parent_run_scope_id, parent_run_id, graph_name, partition_key, instance_id, created_at, closed_at`

func (b *runScopesImpl) Create(ctx context.Context, row persistence.RunScopeRow, tx persistence.Tx) error {
	var createdAt any
	if !row.CreatedAt.IsZero() {
		createdAt = row.CreatedAt
	}
	_, err := b.q(tx).Exec(ctx,
		`INSERT INTO rimsky_run_scopes
		   (id, parent_run_scope_id, parent_run_id, graph_name, partition_key, instance_id, created_at, closed_at)
		 VALUES ($1, $2, $3, $4, $5, $6, COALESCE($7, NOW()), $8)`,
		row.ID, row.ParentRunScopeID, row.ParentNodeRunID, row.GraphName, row.PartitionKey,
		row.InstanceID, createdAt, row.ClosedAt)
	if err != nil {
		return fmt.Errorf("runScopes.Create: %w", err)
	}
	return nil
}

func (b *runScopesImpl) GetByID(ctx context.Context, id shared.UUID, tx persistence.Tx) (*persistence.RunScopeRow, error) {
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

func (b *runScopesImpl) GetFanoutPartition(ctx context.Context, parentNodeRunID shared.UUID, partitionKey string, tx persistence.Tx) (*persistence.RunScopeRow, error) {
	r, err := scanRunScopeRow(b.q(tx).QueryRow(ctx,
		`SELECT `+runScopeCols+` FROM rimsky_run_scopes
		   WHERE parent_run_id = $1 AND partition_key = $2 AND closed_at IS NULL`,
		parentNodeRunID, partitionKey))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("runScopes.GetFanoutPartition: %w", err)
	}
	return &r, nil
}

func (b *runScopesImpl) Close(ctx context.Context, id shared.UUID, tx persistence.Tx) error {
	_, err := b.q(tx).Exec(ctx,
		`UPDATE rimsky_run_scopes SET closed_at = NOW() WHERE id = $1 AND closed_at IS NULL`, id)
	if err != nil {
		return fmt.Errorf("runScopes.Close: %w", err)
	}
	return nil
}

func (b *runScopesImpl) ListParentChain(ctx context.Context, id shared.UUID, tx persistence.Tx) ([]persistence.RunScopeRow, error) {
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

func (b *runScopesImpl) ListTreeDeepestFirst(ctx context.Context, rootRunScopeID shared.UUID, tx persistence.Tx) ([]persistence.RunScopeRow, error) {
	rows, err := b.q(tx).Query(ctx,
		`WITH RECURSIVE tree AS (
		     SELECT `+runScopeCols+`, 0 AS depth FROM rimsky_run_scopes WHERE id = $1
		   UNION ALL
		     SELECT rs.id, rs.parent_run_scope_id, rs.parent_run_id, rs.graph_name,
		            rs.partition_key, rs.instance_id, rs.created_at, rs.closed_at,
		            tree.depth + 1
		       FROM rimsky_run_scopes rs
		       JOIN tree ON rs.parent_run_scope_id = tree.id
		 )
		 SELECT `+runScopeCols+` FROM tree
		  ORDER BY depth DESC, created_at DESC, id DESC`, rootRunScopeID)
	if err != nil {
		return nil, fmt.Errorf("runScopes.ListTreeDeepestFirst: %w", err)
	}
	defer rows.Close()
	var out []persistence.RunScopeRow
	for rows.Next() {
		r, err := scanRunScopeRow(rows)
		if err != nil {
			return nil, fmt.Errorf("runScopes.ListTreeDeepestFirst scan: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("runScopes.ListTreeDeepestFirst iter: %w", err)
	}
	return out, nil
}

func scanRunScopeRow(s scannable) (persistence.RunScopeRow, error) {
	var r persistence.RunScopeRow
	err := s.Scan(&r.ID, &r.ParentRunScopeID, &r.ParentNodeRunID, &r.GraphName,
		&r.PartitionKey, &r.InstanceID, &r.CreatedAt, &r.ClosedAt)
	return r, err
}
