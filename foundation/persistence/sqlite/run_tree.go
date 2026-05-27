// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// SQLite impl of persistence.RunTreeTable — mirror of the postgres
// impl. Under RunScope-first (per spec
// .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md),
// the tree shape lives on `rimsky_run_scopes`; ListChildren joins
// across run_scope_id.

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/foundation/spec"
)

type runTreeImpl tablesImpl

var _ persistence.RunTreeTable = (*runTreeImpl)(nil)

func (s *tablesImpl) RunTree() persistence.RunTreeTable { return (*runTreeImpl)(s) }

func (b *runTreeImpl) q(tx persistence.Tx) querier { return (*tablesImpl)(b).q(tx) }

const sqliteRunTreeCols = `
  id, node_id, frame_id, run_scope_id, phase,
  state, settling_signal_type, aggregation_policy`

func (b *runTreeImpl) CreateRootRun(ctx context.Context, tx persistence.Tx, in persistence.CreateRootRunInput) error {
	if in.RunScopeID == (shared.UUID{}) {
		return errors.New("sqlite.run_tree.CreateRootRun: run_scope_id required")
	}
	policy, err := persistence.MarshalAggregationPolicy(in.AggregationPolicy)
	if err != nil {
		return fmt.Errorf("sqlite.run_tree.CreateRootRun: marshal policy: %w", err)
	}
	stores := in.RequiredStores
	if stores == nil {
		stores = []string{}
	}
	var executor any
	if in.ExecutorName != "" {
		executor = in.ExecutorName
	}
	var policyArg any
	if len(policy) > 0 {
		policyArg = string(policy)
	}
	_, err = b.q(tx).ExecContext(ctx,
		`INSERT INTO rimsky_node_runs (
		   id, node_id, executor_name, required_stores, enqueued_at, phase, frame_id,
		   run_scope_id, state, aggregation_policy
		 ) VALUES (?, ?, ?, ?, ?, 'pending', ?, ?, 'stale', ?)
		 ON CONFLICT(id) DO NOTHING`,
		in.RunID.String(), in.NodeID.String(), executor, marshalStringArray(stores),
		formatTime(time.Now().UTC()), in.FrameID.String(), in.RunScopeID.String(), policyArg,
	)
	if err != nil {
		return fmt.Errorf("sqlite.run_tree.CreateRootRun: %w", err)
	}
	return nil
}

func (b *runTreeImpl) CreateChildRun(ctx context.Context, tx persistence.Tx, in persistence.CreateChildRunInput) error {
	if in.RunScopeID == (shared.UUID{}) {
		return errors.New("sqlite.run_tree.CreateChildRun: run_scope_id required")
	}
	policy, err := persistence.MarshalAggregationPolicy(in.AggregationPolicy)
	if err != nil {
		return fmt.Errorf("sqlite.run_tree.CreateChildRun: marshal policy: %w", err)
	}
	stores := in.RequiredStores
	if stores == nil {
		stores = []string{}
	}
	var executor any
	if in.ExecutorName != "" {
		executor = in.ExecutorName
	}
	var policyArg any
	if len(policy) > 0 {
		policyArg = string(policy)
	}
	// Idempotency: NOT EXISTS keyed on (node_id, run_scope_id).
	_, err = b.q(tx).ExecContext(ctx,
		`INSERT INTO rimsky_node_runs (
		   id, node_id, executor_name, required_stores, enqueued_at, phase, frame_id,
		   run_scope_id, state, aggregation_policy
		 )
		 SELECT ?, ?, ?, ?, ?, 'pending', ?, ?, 'stale', ?
		  WHERE NOT EXISTS (
		    SELECT 1 FROM rimsky_node_runs
		     WHERE node_id = ? AND run_scope_id = ?
		       AND phase IN ('pending','active','held','parked')
		  )`,
		in.RunID.String(), in.NodeID.String(), executor, marshalStringArray(stores),
		formatTime(time.Now().UTC()), in.FrameID.String(), in.RunScopeID.String(), policyArg,
		in.NodeID.String(), in.RunScopeID.String(),
	)
	if err != nil {
		return fmt.Errorf("sqlite.run_tree.CreateChildRun: %w", err)
	}
	return nil
}

func (b *runTreeImpl) GetByID(ctx context.Context, tx persistence.Tx, runID shared.UUID) (*persistence.RunTreeRow, error) {
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

// LockTreeForUpdate is a best-effort emulation under SQLite. SQLite does
// not support row-level `SELECT ... FOR UPDATE`; the per-connection
// write-lock that the open transaction holds is what guarantees
// atomic update. Concurrent readers see snapshot data per WAL mode.
// Multi-host deployments must use postgres.
func (b *runTreeImpl) LockTreeForUpdate(ctx context.Context, tx persistence.Tx, runID shared.UUID) (*persistence.RunTreeRow, error) {
	if tx == nil {
		return nil, errors.New("sqlite.run_tree.LockTreeForUpdate: tx required")
	}
	return b.GetByID(ctx, tx, runID)
}

// ListChildren returns all in-flight runs in RunScopes whose
// parent_run_id equals parentRunID. Walks via rimsky_run_scopes JOIN.
func (b *runTreeImpl) ListChildren(ctx context.Context, tx persistence.Tx, parentRunID shared.UUID) ([]persistence.RunTreeRow, error) {
	rows, err := b.q(tx).QueryContext(ctx,
		`SELECT nr.id, nr.node_id, nr.frame_id, nr.run_scope_id, nr.phase,
		        nr.state, nr.settling_signal_type, nr.aggregation_policy
		   FROM rimsky_node_runs nr
		   JOIN rimsky_run_scopes rs ON rs.id = nr.run_scope_id
		  WHERE rs.parent_run_id = ?
		  ORDER BY rs.partition_key, nr.enqueued_at`,
		parentRunID.String())
	if err != nil {
		return nil, fmt.Errorf("sqlite.run_tree.ListChildren: %w", err)
	}
	defer rows.Close()
	var out []persistence.RunTreeRow
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

func (b *runTreeImpl) UpdateStateAndOutcome(
	ctx context.Context, tx persistence.Tx, runID shared.UUID,
	state cascade.NodeState, settlingSignalType *string,
) error {
	if settlingSignalType == nil {
		_, err := b.q(tx).ExecContext(ctx,
			`UPDATE rimsky_node_runs SET state = ? WHERE id = ?`,
			string(state), runID.String())
		if err != nil {
			return fmt.Errorf("sqlite.run_tree.UpdateStateAndOutcome: %w", err)
		}
		return nil
	}
	_, err := b.q(tx).ExecContext(ctx,
		`UPDATE rimsky_node_runs SET state = ?, settling_signal_type = ? WHERE id = ?`,
		string(state), *settlingSignalType, runID.String())
	if err != nil {
		return fmt.Errorf("sqlite.run_tree.UpdateStateAndOutcome: %w", err)
	}
	return nil
}

func (b *runTreeImpl) UpdateAggregationPolicy(
	ctx context.Context, tx persistence.Tx, runID shared.UUID, policy spec.AggregationPolicy,
) error {
	bytes, err := persistence.MarshalAggregationPolicy(policy)
	if err != nil {
		return fmt.Errorf("sqlite.run_tree.UpdateAggregationPolicy: marshal: %w", err)
	}
	var arg any
	if len(bytes) > 0 {
		arg = string(bytes)
	}
	_, err = b.q(tx).ExecContext(ctx,
		`UPDATE rimsky_node_runs SET aggregation_policy = ? WHERE id = ?`, arg, runID.String())
	if err != nil {
		return fmt.Errorf("sqlite.run_tree.UpdateAggregationPolicy: %w", err)
	}
	return nil
}

// scanSqliteRunTreeRow handles both *sql.Row and *sql.Rows via the
// shared `scannable` interface (defined in backend.go).
func scanSqliteRunTreeRow(s scannable) (*persistence.RunTreeRow, error) {
	var (
		idStr, nodeIDStr, frameIDStr, runScopeIDStr string
		phase                                       string
		state                                       string
		settlingSignal                              sql.NullString
		policyText                                  sql.NullString
	)
	if err := s.Scan(&idStr, &nodeIDStr, &frameIDStr, &runScopeIDStr, &phase, &state, &settlingSignal, &policyText); err != nil {
		return nil, err
	}
	out := &persistence.RunTreeRow{
		Phase: phase,
		State: cascade.NodeState(state),
	}
	if settlingSignal.Valid {
		v := settlingSignal.String
		out.SettlingSignalType = &v
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		return nil, fmt.Errorf("parse id: %w", err)
	}
	out.RunID = shared.UUID(id)
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
