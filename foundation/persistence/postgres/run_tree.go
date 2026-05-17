// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// run_tree.go is the postgres impl of `persistence.RunTreeTable` —
// CRUD + locking on the run-tree extension of `rimsky_node_runs`. Spec
// .ok-planner/specs/2026-05-15-data-platform-extensions-design.md §Run-tree
// and aggregation.

package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/fallguy/rimsky/foundation/cascade"
	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/foundation/shared"
	"github.com/fallguy/rimsky/foundation/spec"
)

type runTreeImpl tablesImpl

var _ persistence.RunTreeTable = (*runTreeImpl)(nil)

// RunTree exposes the run-tree accessor. See `foundation/persistence/run_tree.go`.
func (s *tablesImpl) RunTree() persistence.RunTreeTable { return (*runTreeImpl)(s) }

func (b *runTreeImpl) q(tx persistence.Tx) querier { return (*tablesImpl)(b).q(tx) }

const runTreeCols = `
  id, node_id, frame_id, parent_run_id, child_key,
  state, last_outcome, aggregation_policy
`

func (b *runTreeImpl) CreateRootRun(ctx context.Context, tx persistence.Tx, in persistence.CreateRootRunInput) error {
	policy, err := persistence.MarshalAggregationPolicy(in.AggregationPolicy)
	if err != nil {
		return fmt.Errorf("run_tree.CreateRootRun: marshal policy: %w", err)
	}
	stores := in.RequiredStores
	if stores == nil {
		stores = []string{}
	}
	executor := nullableText(in.ExecutorName)
	_, err = b.q(tx).Exec(ctx,
		`INSERT INTO rimsky_node_runs (
		   id, node_id, executor_name, required_stores, enqueued_at, phase, frame_id,
		   parent_run_id, child_key, state, last_outcome, aggregation_policy
		 ) VALUES (
		   $1, $2, $3, $4, $5, 'pending', $6,
		   NULL, NULL, 'stale', 'fresh_unchanged', $7
		 )
		 ON CONFLICT (id) DO NOTHING`,
		in.RunID, in.NodeID, executor, stores, time.Now().UTC(), in.FrameID, nullableBytes(policy),
	)
	if err != nil {
		return fmt.Errorf("run_tree.CreateRootRun: %w", err)
	}
	return nil
}

func (b *runTreeImpl) CreateChildRun(ctx context.Context, tx persistence.Tx, in persistence.CreateChildRunInput) error {
	if in.ChildKey == "" {
		return errors.New("run_tree.CreateChildRun: child_key required")
	}
	if in.ParentRunID == (shared.UUID{}) {
		return errors.New("run_tree.CreateChildRun: parent_run_id required")
	}
	policy, err := persistence.MarshalAggregationPolicy(in.AggregationPolicy)
	if err != nil {
		return fmt.Errorf("run_tree.CreateChildRun: marshal policy: %w", err)
	}
	stores := in.RequiredStores
	if stores == nil {
		stores = []string{}
	}
	executor := nullableText(in.ExecutorName)
	// Idempotency check: a row already exists for (parent_run_id, child_key)?
	existing, err := b.GetByParentChildKey(ctx, tx, in.ParentRunID, in.ChildKey)
	if err != nil {
		return fmt.Errorf("run_tree.CreateChildRun: idempotency lookup: %w", err)
	}
	if existing != nil {
		return nil
	}
	_, err = b.q(tx).Exec(ctx,
		`INSERT INTO rimsky_node_runs (
		   id, node_id, executor_name, required_stores, enqueued_at, phase, frame_id,
		   parent_run_id, child_key, state, last_outcome, aggregation_policy
		 ) VALUES (
		   $1, $2, $3, $4, $5, 'pending', $6,
		   $7, $8, 'stale', 'fresh_unchanged', $9
		 )`,
		in.RunID, in.NodeID, executor, stores, time.Now().UTC(), in.FrameID,
		in.ParentRunID, in.ChildKey, nullableBytes(policy),
	)
	if err != nil {
		return fmt.Errorf("run_tree.CreateChildRun: %w", err)
	}
	return nil
}

func (b *runTreeImpl) GetByID(ctx context.Context, tx persistence.Tx, runID shared.UUID) (*persistence.RunTreeRow, error) {
	row, err := scanRunTreeRow(b.q(tx).QueryRow(ctx,
		`SELECT `+runTreeCols+` FROM rimsky_node_runs WHERE id = $1`,
		runID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("run_tree.GetByID: %w", err)
	}
	return row, nil
}

func (b *runTreeImpl) GetByParentChildKey(ctx context.Context, tx persistence.Tx, parentRunID shared.UUID, childKey string) (*persistence.RunTreeRow, error) {
	row, err := scanRunTreeRow(b.q(tx).QueryRow(ctx,
		`SELECT `+runTreeCols+` FROM rimsky_node_runs
		   WHERE parent_run_id = $1 AND child_key = $2`,
		parentRunID, childKey))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("run_tree.GetByParentChildKey: %w", err)
	}
	return row, nil
}

func (b *runTreeImpl) LockTreeForUpdate(ctx context.Context, tx persistence.Tx, runID shared.UUID) (*persistence.RunTreeRow, error) {
	if tx == nil {
		return nil, errors.New("run_tree.LockTreeForUpdate: tx required")
	}
	row, err := scanRunTreeRow(b.q(tx).QueryRow(ctx,
		`SELECT `+runTreeCols+` FROM rimsky_node_runs
		   WHERE id = $1 FOR UPDATE`,
		runID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("run_tree.LockTreeForUpdate: %w", err)
	}
	return row, nil
}

func (b *runTreeImpl) ListChildren(ctx context.Context, tx persistence.Tx, parentRunID shared.UUID) ([]persistence.RunTreeRow, error) {
	rows, err := b.q(tx).Query(ctx,
		`SELECT `+runTreeCols+` FROM rimsky_node_runs WHERE parent_run_id = $1 ORDER BY child_key`,
		parentRunID)
	if err != nil {
		return nil, fmt.Errorf("run_tree.ListChildren: %w", err)
	}
	defer rows.Close()
	var out []persistence.RunTreeRow
	for rows.Next() {
		row, err := scanRunTreeRowFromRows(rows)
		if err != nil {
			return nil, fmt.Errorf("run_tree.ListChildren: scan: %w", err)
		}
		out = append(out, *row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("run_tree.ListChildren: iter: %w", err)
	}
	return out, nil
}

func (b *runTreeImpl) UpdateStateAndOutcome(
	ctx context.Context, tx persistence.Tx, runID shared.UUID,
	state cascade.NodeState, lastOutcome cascade.LastOutcome,
) error {
	if lastOutcome == "" {
		_, err := b.q(tx).Exec(ctx,
			`UPDATE rimsky_node_runs SET state = $2 WHERE id = $1`,
			runID, string(state))
		if err != nil {
			return fmt.Errorf("run_tree.UpdateStateAndOutcome: %w", err)
		}
		return nil
	}
	_, err := b.q(tx).Exec(ctx,
		`UPDATE rimsky_node_runs SET state = $2, last_outcome = $3 WHERE id = $1`,
		runID, string(state), string(lastOutcome))
	if err != nil {
		return fmt.Errorf("run_tree.UpdateStateAndOutcome: %w", err)
	}
	return nil
}

func (b *runTreeImpl) UpdateAggregationPolicy(
	ctx context.Context, tx persistence.Tx, runID shared.UUID, policy spec.AggregationPolicy,
) error {
	bytes, err := persistence.MarshalAggregationPolicy(policy)
	if err != nil {
		return fmt.Errorf("run_tree.UpdateAggregationPolicy: marshal: %w", err)
	}
	_, err = b.q(tx).Exec(ctx,
		`UPDATE rimsky_node_runs SET aggregation_policy = $2 WHERE id = $1`,
		runID, nullableBytes(bytes))
	if err != nil {
		return fmt.Errorf("run_tree.UpdateAggregationPolicy: %w", err)
	}
	return nil
}

// scanRunTreeRow scans a pgx.Row into a *RunTreeRow.
func scanRunTreeRow(row pgx.Row) (*persistence.RunTreeRow, error) {
	var (
		out         persistence.RunTreeRow
		parentRunID *shared.UUID
		childKey    *string
		state       string
		outcome     *string
		policyBytes []byte
	)
	if err := row.Scan(
		&out.RunID, &out.NodeID, &out.FrameID, &parentRunID, &childKey,
		&state, &outcome, &policyBytes,
	); err != nil {
		return nil, err
	}
	out.State = cascade.NodeState(state)
	if outcome != nil {
		out.LastOutcome = cascade.LastOutcome(*outcome)
	}
	if parentRunID != nil {
		out.ParentRunID = parentRunID
	}
	if childKey != nil {
		out.ChildKey = *childKey
	}
	policy, err := persistence.UnmarshalAggregationPolicy(policyBytes)
	if err != nil {
		return nil, err
	}
	out.AggregationPolicy = policy
	return &out, nil
}

// scanRunTreeRowFromRows reuses the same scan against a pgx.Rows.
func scanRunTreeRowFromRows(rows pgx.Rows) (*persistence.RunTreeRow, error) {
	var (
		out         persistence.RunTreeRow
		parentRunID *shared.UUID
		childKey    *string
		state       string
		outcome     *string
		policyBytes []byte
	)
	if err := rows.Scan(
		&out.RunID, &out.NodeID, &out.FrameID, &parentRunID, &childKey,
		&state, &outcome, &policyBytes,
	); err != nil {
		return nil, err
	}
	out.State = cascade.NodeState(state)
	if outcome != nil {
		out.LastOutcome = cascade.LastOutcome(*outcome)
	}
	if parentRunID != nil {
		out.ParentRunID = parentRunID
	}
	if childKey != nil {
		out.ChildKey = *childKey
	}
	policy, err := persistence.UnmarshalAggregationPolicy(policyBytes)
	if err != nil {
		return nil, err
	}
	out.AggregationPolicy = policy
	return &out, nil
}
