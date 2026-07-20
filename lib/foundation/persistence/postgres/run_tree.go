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

func (s *tablesImpl) NodeRunTree() persistence.RunTreeTable { return (*runTreeImpl)(s) }

func (b *runTreeImpl) q(tx persistence.Tx) querier { return (*tablesImpl)(b).q(tx) }

const runTreeCols = `
  id, node_id, frame_id, run_scope_id,
  state, settling_signal_type, aggregation_policy, changed
`

func (b *runTreeImpl) CreateRootNodeRun(ctx context.Context, tx persistence.Tx, in persistence.CreateRootNodeRunInput) error {
	policy, err := persistence.MarshalAggregationPolicy(in.AggregationPolicy)
	if err != nil {
		return fmt.Errorf("run_tree.CreateRootNodeRun: marshal policy: %w", err)
	}
	if in.RunScopeID == (shared.UUID{}) {
		return errors.New("run_tree.CreateRootNodeRun: run_scope_id required")
	}
	stores := in.RequiredClaimProducers
	if stores == nil {
		stores = []string{}
	}
	executor := nullableText(in.ExecutorName)
	tag, err := b.q(tx).Exec(ctx,
		`INSERT INTO rimsky_node_runs (
		   id, node_id, executor_name, required_stores, enqueued_at, frame_id,
		   run_scope_id, state, creation_reason, sequence, aggregation_policy
		 ) VALUES (
		   $1, $2, $3, $4, $5, $6,
		   $7, 'stale', 'cascade',
		   COALESCE((SELECT MAX(sequence) FROM rimsky_node_runs WHERE node_id = $2 AND run_scope_id = $7), 0) + 1,
		   $8
		 )
		 ON CONFLICT (id) DO NOTHING`,
		in.NodeRunID, in.NodeID, executor, stores, time.Now().UTC(), in.FrameID, in.RunScopeID, nullableBytes(policy),
	)
	if err != nil {
		return fmt.Errorf("run_tree.CreateRootNodeRun: %w", err)
	}
	if tag.RowsAffected() == 1 {
		if err := (*nodeAttributesImpl)((*tablesImpl)(b)).SnapshotBagForNewRun(ctx, tx, in.NodeRunID, in.NodeID, in.RunScopeID); err != nil {
			return fmt.Errorf("run_tree.CreateRootNodeRun: snapshot bag: %w", err)
		}
	}
	return nil
}

func (b *runTreeImpl) CreateChildNodeRun(ctx context.Context, tx persistence.Tx, in persistence.CreateChildNodeRunInput) error {
	if in.RunScopeID == (shared.UUID{}) {
		return errors.New("run_tree.CreateChildNodeRun: run_scope_id required")
	}
	policy, err := persistence.MarshalAggregationPolicy(in.AggregationPolicy)
	if err != nil {
		return fmt.Errorf("run_tree.CreateChildNodeRun: marshal policy: %w", err)
	}
	stores := in.RequiredClaimProducers
	if stores == nil {
		stores = []string{}
	}
	executor := nullableText(in.ExecutorName)
	tag, err := b.q(tx).Exec(ctx,
		`INSERT INTO rimsky_node_runs (
		   id, node_id, executor_name, required_stores, enqueued_at, frame_id,
		   run_scope_id, state, creation_reason, sequence, aggregation_policy
		 )
		 SELECT $1, $2, $3, $4, $5, $6, $7, 'stale', 'cascade',
		        COALESCE((SELECT MAX(sequence) FROM rimsky_node_runs WHERE node_id = $2 AND run_scope_id = $7), 0) + 1,
		        $8
		  WHERE NOT EXISTS (
		    SELECT 1 FROM rimsky_node_runs
		     WHERE node_id = $2 AND run_scope_id = $7
		       AND state IN ('running','held','parked')
		  )`,
		in.NodeRunID, in.NodeID, executor, stores, time.Now().UTC(), in.FrameID, in.RunScopeID, nullableBytes(policy),
	)
	if err != nil {
		return fmt.Errorf("run_tree.CreateChildNodeRun: %w", err)
	}
	if tag.RowsAffected() == 1 {
		if err := (*nodeAttributesImpl)((*tablesImpl)(b)).SnapshotBagForNewRun(ctx, tx, in.NodeRunID, in.NodeID, in.RunScopeID); err != nil {
			return fmt.Errorf("run_tree.CreateChildNodeRun: snapshot bag: %w", err)
		}
	}
	return nil
}

func (b *runTreeImpl) GetByID(ctx context.Context, tx persistence.Tx, runID shared.UUID) (*persistence.NodeRunTreeRow, error) {
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

func (b *runTreeImpl) LockTreeForUpdate(ctx context.Context, tx persistence.Tx, runID shared.UUID) (*persistence.NodeRunTreeRow, error) {
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

func (b *runTreeImpl) ListChildren(ctx context.Context, tx persistence.Tx, parentNodeRunID shared.UUID) ([]persistence.NodeRunTreeRow, error) {
	rows, err := b.q(tx).Query(ctx,
		`SELECT nr.id, nr.node_id, nr.frame_id, nr.run_scope_id,
		        nr.state, nr.settling_signal_type, nr.aggregation_policy, nr.changed
		   FROM rimsky_node_runs nr
		   JOIN rimsky_run_scopes rs ON rs.id = nr.run_scope_id
		  WHERE rs.parent_run_id = $1
		  ORDER BY rs.partition_key, nr.enqueued_at`,
		parentNodeRunID)
	if err != nil {
		return nil, fmt.Errorf("run_tree.ListChildren: %w", err)
	}
	defer rows.Close()
	var out []persistence.NodeRunTreeRow
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
	state cascade.NodeState, settlingSignalType *string, changed bool,
) error {
	if settlingSignalType == nil {
		_, err := b.q(tx).Exec(ctx,
			`UPDATE rimsky_node_runs SET state = $2, changed = $3 WHERE id = $1`,
			runID, string(state), changed)
		if err != nil {
			return fmt.Errorf("run_tree.UpdateStateAndOutcome: %w", err)
		}
		return nil
	}
	_, err := b.q(tx).Exec(ctx,
		`UPDATE rimsky_node_runs SET state = $2, settling_signal_type = $3, changed = $4 WHERE id = $1`,
		runID, string(state), *settlingSignalType, changed)
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

func scanRunTreeRow(row pgx.Row) (*persistence.NodeRunTreeRow, error) {
	var (
		out            persistence.NodeRunTreeRow
		state          string
		settlingSignal *string
		policyBytes    []byte
	)
	if err := row.Scan(
		&out.NodeRunID, &out.NodeID, &out.FrameID, &out.RunScopeID,
		&state, &settlingSignal, &policyBytes, &out.Changed,
	); err != nil {
		return nil, err
	}
	out.State = cascade.NodeState(state)
	out.SettlingSignalType = settlingSignal
	policy, err := persistence.UnmarshalAggregationPolicy(policyBytes)
	if err != nil {
		return nil, err
	}
	out.AggregationPolicy = policy
	return &out, nil
}

func scanRunTreeRowFromRows(rows pgx.Rows) (*persistence.NodeRunTreeRow, error) {
	var (
		out            persistence.NodeRunTreeRow
		state          string
		settlingSignal *string
		policyBytes    []byte
	)
	if err := rows.Scan(
		&out.NodeRunID, &out.NodeID, &out.FrameID, &out.RunScopeID,
		&state, &settlingSignal, &policyBytes, &out.Changed,
	); err != nil {
		return nil, err
	}
	out.State = cascade.NodeState(state)
	out.SettlingSignalType = settlingSignal
	policy, err := persistence.UnmarshalAggregationPolicy(policyBytes)
	if err != nil {
		return nil, err
	}
	out.AggregationPolicy = policy
	return &out, nil
}
