// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

type runTreeImpl tablesImpl

var _ persistence.NodeRunTreeTable = (*runTreeImpl)(nil)

func (s *tablesImpl) NodeRunTree() persistence.NodeRunTreeTable { return (*runTreeImpl)(s) }

func (b *runTreeImpl) q(tx persistence.Tx) querier { return (*tablesImpl)(b).q(tx) }

const sqliteRunTreeCols = `
  id, node_id, frame_id, run_scope_id,
  state, settling_signal_type, aggregation_policy, changed`

func (b *runTreeImpl) CreateRootNodeRun(ctx context.Context, in persistence.CreateRootNodeRunInput, tx persistence.Tx) error {
	if in.RunScopeID == (shared.UUID{}) {
		return errors.New("sqlite.run_tree.CreateRootNodeRun: run_scope_id required")
	}
	policy, err := persistence.MarshalAggregationPolicy(in.AggregationPolicy)
	if err != nil {
		return fmt.Errorf("sqlite.run_tree.CreateRootNodeRun: marshal policy: %w", err)
	}
	claimProducers := in.RequiredClaimProducers
	if claimProducers == nil {
		claimProducers = []string{}
	}
	var executor any
	if in.ExecutorName != "" {
		executor = in.ExecutorName
	}
	var policyArg any
	if len(policy) > 0 {
		policyArg = string(policy)
	}
	enqueuedAt := in.EnqueuedAt
	if enqueuedAt.IsZero() {
		enqueuedAt = time.Now().UTC()
	}
	res, err := b.q(tx).ExecContext(ctx,
		`INSERT INTO rimsky_node_runs (
		   id, node_id, executor_name, required_claim_producers, enqueued_at, frame_id,
		   run_scope_id, state, creation_reason, sequence, aggregation_policy
		 ) VALUES (?, ?, ?, ?, ?, ?, ?, 'stale', 'cascade',
		   COALESCE((SELECT MAX(sequence) FROM rimsky_node_runs WHERE node_id = ? AND run_scope_id = ?), 0) + 1,
		   ?)
		 ON CONFLICT(id) DO NOTHING`,
		in.NodeRunID.String(), in.NodeID.String(), executor, marshalStringArray(claimProducers),
		formatTime(enqueuedAt), in.FrameID.String(), in.RunScopeID.String(),
		in.NodeID.String(), in.RunScopeID.String(),
		policyArg,
	)
	if err != nil {
		return fmt.Errorf("sqlite.run_tree.CreateRootNodeRun: %w", err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 1 {
		if err := (*nodeAttributesImpl)((*tablesImpl)(b)).SnapshotBagForNewRun(ctx, in.NodeRunID, in.NodeID, in.RunScopeID, tx); err != nil {
			return fmt.Errorf("sqlite.run_tree.CreateRootNodeRun: snapshot bag: %w", err)
		}
	}
	return nil
}

func (b *runTreeImpl) CreateChildNodeRun(ctx context.Context, in persistence.CreateChildNodeRunInput, tx persistence.Tx) error {
	if in.RunScopeID == (shared.UUID{}) {
		return errors.New("sqlite.run_tree.CreateChildNodeRun: run_scope_id required")
	}
	policy, err := persistence.MarshalAggregationPolicy(in.AggregationPolicy)
	if err != nil {
		return fmt.Errorf("sqlite.run_tree.CreateChildNodeRun: marshal policy: %w", err)
	}
	claimProducers := in.RequiredClaimProducers
	if claimProducers == nil {
		claimProducers = []string{}
	}
	var executor any
	if in.ExecutorName != "" {
		executor = in.ExecutorName
	}
	var policyArg any
	if len(policy) > 0 {
		policyArg = string(policy)
	}
	enqueuedAt := in.EnqueuedAt
	if enqueuedAt.IsZero() {
		enqueuedAt = time.Now().UTC()
	}
	res, err := b.q(tx).ExecContext(ctx,
		`INSERT INTO rimsky_node_runs (
		   id, node_id, executor_name, required_claim_producers, enqueued_at, frame_id,
		   run_scope_id, state, creation_reason, sequence, aggregation_policy
		 )
		 SELECT ?, ?, ?, ?, ?, ?, ?, 'stale', 'cascade',
		   COALESCE((SELECT MAX(sequence) FROM rimsky_node_runs WHERE node_id = ? AND run_scope_id = ?), 0) + 1,
		   ?
		  WHERE NOT EXISTS (
		    SELECT 1 FROM rimsky_node_runs
		     WHERE node_id = ? AND run_scope_id = ?
		       AND state IN ('running','held','parked')
		  )
		    AND EXISTS (
		      SELECT 1 FROM rimsky_run_scopes rs
		       WHERE rs.id = ? AND rs.closed_at IS NULL
		    )`,
		in.NodeRunID.String(), in.NodeID.String(), executor, marshalStringArray(claimProducers),
		formatTime(enqueuedAt), in.FrameID.String(), in.RunScopeID.String(),
		in.NodeID.String(), in.RunScopeID.String(),
		policyArg,
		in.NodeID.String(), in.RunScopeID.String(),
		in.RunScopeID.String(),
	)
	if err != nil {
		return fmt.Errorf("sqlite.run_tree.CreateChildNodeRun: %w", err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 1 {
		if err := (*nodeAttributesImpl)((*tablesImpl)(b)).SnapshotBagForNewRun(ctx, in.NodeRunID, in.NodeID, in.RunScopeID, tx); err != nil {
			return fmt.Errorf("sqlite.run_tree.CreateChildNodeRun: snapshot bag: %w", err)
		}
	}
	return nil
}

func (b *runTreeImpl) GetByID(ctx context.Context, runID shared.UUID, tx persistence.Tx) (*persistence.NodeRunTreeRow, error) {
	row, err := scanSqliteRunTreeRow(b.q(tx).QueryRowContext(ctx,
		`SELECT `+sqliteRunTreeCols+` FROM rimsky_node_runs WHERE id = ?`, runID.String()))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("sqlite.run_tree.GetByID: %w", err)
	}
	return row, nil
}

func (b *runTreeImpl) LockTreeForUpdate(ctx context.Context, runID shared.UUID, tx persistence.Tx) (*persistence.NodeRunTreeRow, error) {
	if tx == nil {
		return nil, errors.New("sqlite.run_tree.LockTreeForUpdate: tx required")
	}
	return b.GetByID(ctx, runID, tx)
}

func (b *runTreeImpl) ListChildren(ctx context.Context, parentNodeRunID shared.UUID, tx persistence.Tx) ([]persistence.NodeRunTreeRow, error) {
	rows, err := b.q(tx).QueryContext(ctx,
		`SELECT nr.id, nr.node_id, nr.frame_id, nr.run_scope_id,
		        nr.state, nr.settling_signal_type, nr.aggregation_policy, nr.changed
		   FROM rimsky_node_runs nr
		   JOIN rimsky_run_scopes rs ON rs.id = nr.run_scope_id
		  WHERE rs.parent_run_id = ?
		  ORDER BY rs.partition_key, nr.enqueued_at`,
		parentNodeRunID.String())
	if err != nil {
		return nil, fmt.Errorf("sqlite.run_tree.ListChildren: %w", err)
	}
	defer rows.Close()
	var out []persistence.NodeRunTreeRow
	for rows.Next() {
		row, err := scanSqliteRunTreeRow(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite.run_tree.ListChildren: scan: %w", err)
		}
		out = append(out, *row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite.run_tree.ListChildren: iter: %w", err)
	}
	return out, nil
}

// @concept: transition-reason
func (b *runTreeImpl) UpdateAggregateState(
	ctx context.Context, runID shared.UUID, reason cascade.TransitionReason, settlingSignalType *string, changed bool, tx persistence.Tx,
) error {
	var stateScan string
	if err := b.q(tx).QueryRowContext(ctx,
		`SELECT state FROM rimsky_node_runs WHERE id = ?`, runID.String(),
	).Scan(&stateScan); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("sqlite.run_tree.UpdateAggregateState: %w", persistence.ErrNotFound)
		}
		return fmt.Errorf("sqlite.run_tree.UpdateAggregateState: %w", err)
	}
	target, err := cascade.NextStateParent(cascade.NodeState(stateScan), reason)
	if err != nil {
		return fmt.Errorf("sqlite.run_tree.UpdateAggregateState: %s: %w", runID, err)
	}
	res, err := b.q(tx).ExecContext(ctx,
		`UPDATE rimsky_node_runs
		    SET state = ?,
		        settling_signal_type = COALESCE(?, settling_signal_type),
		        changed = ?
		  WHERE id = ?`,
		string(target), settlingSignalType, changedFlag(changed), runID.String())
	if err != nil {
		return fmt.Errorf("sqlite.run_tree.UpdateAggregateState: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite.run_tree.UpdateAggregateState: rows-affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("sqlite.run_tree.UpdateAggregateState: %w", persistence.ErrNotFound)
	}
	return nil
}

func (b *runTreeImpl) UpdateOutcome(
	ctx context.Context, runID shared.UUID, settlingSignalType *string, changed bool, tx persistence.Tx,
) error {
	res, err := b.q(tx).ExecContext(ctx,
		`UPDATE rimsky_node_runs
		    SET settling_signal_type = COALESCE(?, settling_signal_type),
		        changed = ?
		  WHERE id = ?`,
		settlingSignalType, changedFlag(changed), runID.String())
	if err != nil {
		return fmt.Errorf("sqlite.run_tree.UpdateOutcome: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite.run_tree.UpdateOutcome: rows-affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("sqlite.run_tree.UpdateOutcome: %w", persistence.ErrNotFound)
	}
	return nil
}

func changedFlag(changed bool) int {
	if changed {
		return 1
	}
	return 0
}

func (b *runTreeImpl) UpdateAggregationPolicy(
	ctx context.Context, runID shared.UUID, policy spec.AggregationPolicy, tx persistence.Tx,
) error {
	bytes, err := persistence.MarshalAggregationPolicy(policy)
	if err != nil {
		return fmt.Errorf("sqlite.run_tree.UpdateAggregationPolicy: marshal: %w", err)
	}
	var arg any
	if len(bytes) > 0 {
		arg = string(bytes)
	}
	res, err := b.q(tx).ExecContext(ctx,
		`UPDATE rimsky_node_runs SET aggregation_policy = ? WHERE id = ?`, arg, runID.String())
	if err != nil {
		return fmt.Errorf("sqlite.run_tree.UpdateAggregationPolicy: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite.run_tree.UpdateAggregationPolicy: rows-affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("sqlite.run_tree.UpdateAggregationPolicy: %w", persistence.ErrNotFound)
	}
	return nil
}

func scanSqliteRunTreeRow(s scannable) (*persistence.NodeRunTreeRow, error) {
	var (
		idStr, nodeIDStr, frameIDStr, runScopeIDStr string
		state                                       string
		settlingSignal                              sql.NullString
		policyText                                  sql.NullString
		changedInt                                  int
	)
	if err := s.Scan(&idStr, &nodeIDStr, &frameIDStr, &runScopeIDStr, &state, &settlingSignal, &policyText, &changedInt); err != nil {
		return nil, err
	}
	out := &persistence.NodeRunTreeRow{
		State:   cascade.NodeState(state),
		Changed: changedInt != 0,
	}
	if settlingSignal.Valid {
		v := settlingSignal.String
		out.SettlingSignalType = &v
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		return nil, fmt.Errorf("parse id: %w", err)
	}
	out.NodeRunID = shared.UUID(id)
	nodeID, err := uuid.Parse(nodeIDStr)
	if err != nil {
		return nil, fmt.Errorf("parse node_id: %w", err)
	}
	out.NodeID = shared.UUID(nodeID)
	frameID, err := uuid.Parse(frameIDStr)
	if err != nil {
		return nil, fmt.Errorf("parse frame_id: %w", err)
	}
	out.FrameID = shared.UUID(frameID)
	runScopeID, err := uuid.Parse(runScopeIDStr)
	if err != nil {
		return nil, fmt.Errorf("parse run_scope_id: %w", err)
	}
	out.RunScopeID = shared.UUID(runScopeID)
	var policyBytes []byte
	if policyText.Valid {
		policyBytes = []byte(policyText.String)
	}
	policy, err := persistence.UnmarshalAggregationPolicy(policyBytes)
	if err != nil {
		return nil, fmt.Errorf("decode policy: %w", err)
	}
	out.AggregationPolicy = policy
	return out, nil
}
