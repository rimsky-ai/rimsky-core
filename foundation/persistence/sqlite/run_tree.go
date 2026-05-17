// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// SQLite impl of persistence.RunTreeTable — mirror of the postgres
// impl. SQLite is dev-only; multi-host deployments must use postgres.

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/fallguy/rimsky/foundation/cascade"
	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/foundation/shared"
	"github.com/fallguy/rimsky/foundation/spec"
)

type runTreeImpl tablesImpl

var _ persistence.RunTreeTable = (*runTreeImpl)(nil)

func (s *tablesImpl) RunTree() persistence.RunTreeTable { return (*runTreeImpl)(s) }

func (b *runTreeImpl) q(tx persistence.Tx) querier { return (*tablesImpl)(b).q(tx) }

const sqliteRunTreeCols = `
  id, node_id, frame_id, parent_run_id, child_key,
  state, last_outcome, aggregation_policy`

func (b *runTreeImpl) CreateRootRun(ctx context.Context, tx persistence.Tx, in persistence.CreateRootRunInput) error {
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
		   parent_run_id, child_key, state, last_outcome, aggregation_policy
		 ) VALUES (?, ?, ?, ?, ?, 'pending', ?, NULL, NULL, 'stale', 'fresh_unchanged', ?)
		 ON CONFLICT(id) DO NOTHING`,
		in.RunID.String(), in.NodeID.String(), executor, marshalStringArray(stores), formatTime(time.Now().UTC()), in.FrameID.String(), policyArg,
	)
	if err != nil {
		return fmt.Errorf("sqlite.run_tree.CreateRootRun: %w", err)
	}
	return nil
}

func (b *runTreeImpl) CreateChildRun(ctx context.Context, tx persistence.Tx, in persistence.CreateChildRunInput) error {
	if in.ChildKey == "" {
		return errors.New("sqlite.run_tree.CreateChildRun: child_key required")
	}
	if in.ParentRunID == (shared.UUID{}) {
		return errors.New("sqlite.run_tree.CreateChildRun: parent_run_id required")
	}
	policy, err := persistence.MarshalAggregationPolicy(in.AggregationPolicy)
	if err != nil {
		return fmt.Errorf("sqlite.run_tree.CreateChildRun: marshal policy: %w", err)
	}
	existing, err := b.GetByParentChildKey(ctx, tx, in.ParentRunID, in.ChildKey)
	if err != nil {
		return fmt.Errorf("sqlite.run_tree.CreateChildRun: idempotency lookup: %w", err)
	}
	if existing != nil {
		return nil
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
		   parent_run_id, child_key, state, last_outcome, aggregation_policy
		 ) VALUES (?, ?, ?, ?, ?, 'pending', ?, ?, ?, 'stale', 'fresh_unchanged', ?)`,
		in.RunID.String(), in.NodeID.String(), executor, marshalStringArray(stores), formatTime(time.Now().UTC()), in.FrameID.String(),
		in.ParentRunID.String(), in.ChildKey, policyArg,
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

func (b *runTreeImpl) GetByParentChildKey(ctx context.Context, tx persistence.Tx, parentRunID shared.UUID, childKey string) (*persistence.RunTreeRow, error) {
	row, err := scanSqliteRunTreeRow(b.q(tx).QueryRowContext(ctx,
		`SELECT `+sqliteRunTreeCols+` FROM rimsky_node_runs WHERE parent_run_id = ? AND child_key = ?`,
		parentRunID.String(), childKey))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("sqlite.run_tree.GetByParentChildKey: %w", err)
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

func (b *runTreeImpl) ListChildren(ctx context.Context, tx persistence.Tx, parentRunID shared.UUID) ([]persistence.RunTreeRow, error) {
	rows, err := b.q(tx).QueryContext(ctx,
		`SELECT `+sqliteRunTreeCols+` FROM rimsky_node_runs WHERE parent_run_id = ? ORDER BY child_key`,
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
	state cascade.NodeState, lastOutcome cascade.LastOutcome,
) error {
	if lastOutcome == "" {
		_, err := b.q(tx).ExecContext(ctx,
			`UPDATE rimsky_node_runs SET state = ? WHERE id = ?`,
			string(state), runID.String())
		if err != nil {
			return fmt.Errorf("sqlite.run_tree.UpdateStateAndOutcome: %w", err)
		}
		return nil
	}
	_, err := b.q(tx).ExecContext(ctx,
		`UPDATE rimsky_node_runs SET state = ?, last_outcome = ? WHERE id = ?`,
		string(state), string(lastOutcome), runID.String())
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
		idStr, nodeIDStr, frameIDStr string
		parentRunIDStr               sql.NullString
		childKey                     sql.NullString
		state                        string
		outcome                      sql.NullString
		policyText                   sql.NullString
	)
	if err := s.Scan(&idStr, &nodeIDStr, &frameIDStr, &parentRunIDStr, &childKey, &state, &outcome, &policyText); err != nil {
		return nil, err
	}
	out := &persistence.RunTreeRow{
		State: cascade.NodeState(state),
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
	if parentRunIDStr.Valid {
		pid, err := uuid.Parse(parentRunIDStr.String)
		if err != nil {
			return nil, fmt.Errorf("parse parent_run_id: %w", err)
		}
		parent := shared.UUID(pid)
		out.ParentRunID = &parent
	}
	if childKey.Valid {
		out.ChildKey = childKey.String
	}
	if outcome.Valid {
		out.LastOutcome = cascade.LastOutcome(outcome.String)
	}
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
