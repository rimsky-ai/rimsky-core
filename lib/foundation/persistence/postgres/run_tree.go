// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

type runTreeImpl tablesImpl

var _ persistence.RunTreeTable = (*runTreeImpl)(nil)

func (s *tablesImpl) RunTree() persistence.RunTreeTable { return (*runTreeImpl)(s) }

func (b *runTreeImpl) q(tx persistence.Tx) querier { return (*tablesImpl)(b).q(tx) }

const runTreeCols = `
  id, node_id, frame_id, run_scope_id, phase,
  state, settling_signal_type, aggregation_policy
`

func (b *runTreeImpl) CreateRootRun(ctx context.Context, tx persistence.Tx, in persistence.CreateRootRunInput) error {
	policy, err := persistence.MarshalAggregationPolicy(in.AggregationPolicy)
	if err != nil {
		return fmt.Errorf("run_tree.CreateRootRun: marshal policy: %w", err)
	}
	if in.RunScopeID == (shared.UUID{}) {
		return errors.New("run_tree.CreateRootRun: run_scope_id required")
	}
	stores := in.RequiredStores
	if stores == nil {
		stores = []string{}
	}
	executor := nullableText(in.ExecutorName)
	_, err = b.q(tx).Exec(ctx,
		`INSERT INTO rimsky_node_runs (
		   id, node_id, executor_name, required_stores, enqueued_at, phase, frame_id,
		   run_scope_id, state, aggregation_policy
		 ) VALUES (
		   $1, $2, $3, $4, $5, 'pending', $6,
		   $7, 'stale', $8
		 )
		 ON CONFLICT (id) DO NOTHING`,
		in.RunID, in.NodeID, executor, stores, time.Now().UTC(), in.FrameID, in.RunScopeID, nullableBytes(policy),
	)
	if err != nil {
		return fmt.Errorf("run_tree.CreateRootRun: %w", err)
	}
	return nil
}

func (b *runTreeImpl) CreateChildRun(ctx context.Context, tx persistence.Tx, in persistence.CreateChildRunInput) error {
	if in.RunScopeID == (shared.UUID{}) {
		return errors.New("run_tree.CreateChildRun: run_scope_id required")
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
	_, err = b.q(tx).Exec(ctx,
		`INSERT INTO rimsky_node_runs (
		   id, node_id, executor_name, required_stores, enqueued_at, phase, frame_id,
		   run_scope_id, state, aggregation_policy
		 )
		 SELECT $1, $2, $3, $4, $5, 'pending', $6, $7, 'stale', $8
		  WHERE NOT EXISTS (
		    SELECT 1 FROM rimsky_node_runs
		     WHERE node_id = $2 AND run_scope_id = $7
		       AND phase IN ('pending','active','held','parked')
		  )`,
		in.RunID, in.NodeID, executor, stores, time.Now().UTC(), in.FrameID, in.RunScopeID, nullableBytes(policy),
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
		`SELECT nr.id, nr.node_id, nr.frame_id, nr.run_scope_id, nr.phase,
		        nr.state, nr.settling_signal_type, nr.aggregation_policy
		   FROM rimsky_node_runs nr
		   JOIN rimsky_run_scopes rs ON rs.id = nr.run_scope_id
		  WHERE rs.parent_run_id = $1
		  ORDER BY rs.partition_key, nr.enqueued_at`,
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
	state cascade.NodeState, settlingSignalType *string,
) error {
	if settlingSignalType == nil {
		_, err := b.q(tx).Exec(ctx,
			`UPDATE rimsky_node_runs SET state = $2 WHERE id = $1`,
			runID, string(state))
		if err != nil {
			return fmt.Errorf("run_tree.UpdateStateAndOutcome: %w", err)
		}
		return nil
	}
	_, err := b.q(tx).Exec(ctx,
		`UPDATE rimsky_node_runs SET state = $2, settling_signal_type = $3 WHERE id = $1`,
		runID, string(state), *settlingSignalType)
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

func scanRunTreeRow(row pgx.Row) (*persistence.RunTreeRow, error) {
	var (
		out            persistence.RunTreeRow
		phase          string
		state          string
		settlingSignal *string
		policyBytes    []byte
	)
	if err := row.Scan(
		&out.RunID, &out.NodeID, &out.FrameID, &out.RunScopeID, &phase,
		&state, &settlingSignal, &policyBytes,
	); err != nil {
		return nil, err
	}
	out.Phase = phase
	out.State = cascade.NodeState(state)
	out.SettlingSignalType = settlingSignal
	policy, err := persistence.UnmarshalAggregationPolicy(policyBytes)
	if err != nil {
		return nil, err
	}
	out.AggregationPolicy = policy
	return &out, nil
}

func scanRunTreeRowFromRows(rows pgx.Rows) (*persistence.RunTreeRow, error) {
	var (
		out            persistence.RunTreeRow
		phase          string
		state          string
		settlingSignal *string
		policyBytes    []byte
	)
	if err := rows.Scan(
		&out.RunID, &out.NodeID, &out.FrameID, &out.RunScopeID, &phase,
		&state, &settlingSignal, &policyBytes,
	); err != nil {
		return nil, err
	}
	out.Phase = phase
	out.State = cascade.NodeState(state)
	out.SettlingSignalType = settlingSignal
	policy, err := persistence.UnmarshalAggregationPolicy(policyBytes)
	if err != nil {
		return nil, err
	}
	out.AggregationPolicy = policy
	return &out, nil
}
