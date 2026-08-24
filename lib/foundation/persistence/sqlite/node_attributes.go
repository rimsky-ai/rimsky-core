// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

func (s *nodeAttributesImpl) GetByRun(ctx context.Context, runID shared.UUID, tx persistence.Tx) (*persistence.NodeAttributesRow, error) {
	row := s.q(tx).QueryRowContext(ctx,
		`SELECT node_run_id, node_id, data, updated_at
		   FROM rimsky_node_attributes
		  WHERE node_run_id = ?`, runID.String(),
	)
	return scanAttributeRow(row, "GetByRun")
}

func (s *nodeAttributesImpl) GetLatestByNode(ctx context.Context, nodeID shared.UUID, runScopeID shared.UUID, tx persistence.Tx) (*persistence.NodeAttributesRow, error) {
	row := s.q(tx).QueryRowContext(ctx,
		`SELECT a.node_run_id, a.node_id, a.data, a.updated_at
		   FROM rimsky_node_attributes a
		   JOIN rimsky_node_runs r ON r.id = a.node_run_id
		  WHERE a.node_id = ?
		    AND r.run_scope_id = ?
		  ORDER BY a.updated_at DESC, r.sequence DESC
		  LIMIT 1`, nodeID.String(), runScopeID.String(),
	)
	return scanAttributeRow(row, "GetLatestByNode")
}

// @decision: attribute-bytes-in-the-row
func scanAttributeRow(row scannable, op string) (*persistence.NodeAttributesRow, error) {
	var (
		runIDStr     string
		nodeIDStr    string
		data         []byte
		updatedAtStr string
	)
	if err := row.Scan(&runIDStr, &nodeIDStr, &data, &updatedAtStr); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("node_attributes.%s: %w", op, err)
	}
	runID, err := uuid.Parse(runIDStr)
	if err != nil {
		return nil, fmt.Errorf("node_attributes.%s: bad run id: %w", op, err)
	}
	nodeID, err := uuid.Parse(nodeIDStr)
	if err != nil {
		return nil, fmt.Errorf("node_attributes.%s: bad node id: %w", op, err)
	}
	updatedAt, err := parseTime(updatedAtStr)
	if err != nil {
		return nil, err
	}
	out := persistence.NodeAttributesRow{
		NodeRunID: runID,
		NodeID:    nodeID,
		UpdatedAt: updatedAt,
	}
	m := map[string]any{}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &m); err != nil {
			return nil, fmt.Errorf("node_attributes.%s: unmarshal data: %w", op, err)
		}
	}
	out.Data = m
	return &out, nil
}

// @decision: attribute-bytes-in-the-row
func (s *nodeAttributesImpl) Upsert(ctx context.Context, runID, nodeID shared.UUID, data map[string]any, tx persistence.Tx) error {
	if data == nil {
		data = map[string]any{}
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("node_attributes.Upsert: marshal: %w", err)
	}
	if err := persistence.CheckValueSize("node_attributes.Upsert", runID, "attribute bag", len(raw)); err != nil {
		return err
	}

	_, err = s.q(tx).ExecContext(ctx,
		`INSERT INTO rimsky_node_attributes (node_run_id, node_id, data, updated_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(node_run_id) DO UPDATE
		   SET data       = excluded.data,
		       updated_at = excluded.updated_at`,
		runID.String(), nodeID.String(), raw, nowUTC(),
	)
	if err != nil {
		return fmt.Errorf("node_attributes.Upsert: %w", err)
	}
	return nil
}

func (s *nodeAttributesImpl) MergeDelta(ctx context.Context, runID shared.UUID, delta map[string]any, tx persistence.Tx) error {
	if delta == nil {
		res, err := s.q(tx).ExecContext(ctx,
			`UPDATE rimsky_node_attributes
			    SET updated_at = ?
			  WHERE node_run_id = ?`,
			nowUTC(), runID.String(),
		)
		if err != nil {
			return fmt.Errorf("node_attributes.MergeDelta: touch: %w", err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("node_attributes.MergeDelta: touch: rows-affected: %w", err)
		}
		if n == 0 {
			return fmt.Errorf("node_attributes.MergeDelta: touch: %w", persistence.ErrNotFound)
		}
		return nil
	}

	row := s.q(tx).QueryRowContext(ctx,
		`SELECT data FROM rimsky_node_attributes WHERE node_run_id = ?`,
		runID.String(),
	)
	var current []byte
	if err := row.Scan(&current); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("node_attributes.MergeDelta: %w", persistence.ErrNotFound)
		}
		return fmt.Errorf("node_attributes.MergeDelta: read: %w", err)
	}
	merged, err := persistence.MergeAttributeBag(current, delta)
	if err != nil {
		return fmt.Errorf("node_attributes.MergeDelta: %w", err)
	}
	if err := persistence.CheckValueSize("node_attributes.MergeDelta", runID, "attribute bag", len(merged)); err != nil {
		return err
	}
	res, err := s.q(tx).ExecContext(ctx,
		`UPDATE rimsky_node_attributes
		    SET data = ?,
		        updated_at = ?
		  WHERE node_run_id = ?`,
		merged, nowUTC(), runID.String(),
	)
	if err != nil {
		return fmt.Errorf("node_attributes.MergeDelta: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("node_attributes.MergeDelta: rows-affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("node_attributes.MergeDelta: %w", persistence.ErrNotFound)
	}
	return nil
}

// @concept: cascade
// @decision: mode-default-most-recent
func (s *nodeAttributesImpl) SetDispatchInputBag(
	ctx context.Context, runID, nodeID shared.UUID, bag map[string]any, tx persistence.Tx,
) error {
	if bag == nil {
		bag = map[string]any{}
	}
	raw, err := json.Marshal(bag)
	if err != nil {
		return fmt.Errorf("node_attributes.SetDispatchInputBag: marshal: %w", err)
	}
	if err := persistence.CheckValueSize("node_attributes.SetDispatchInputBag", runID, "dispatch input bag", len(raw)); err != nil {
		return err
	}
	_, err = s.q(tx).ExecContext(ctx,
		`INSERT INTO rimsky_node_attributes (node_run_id, node_id, data, dispatch_input_bag, updated_at)
		 VALUES (?, ?, '{}', ?, ?)
		 ON CONFLICT(node_run_id) DO UPDATE
		   SET dispatch_input_bag = excluded.dispatch_input_bag,
		       updated_at         = excluded.updated_at`,
		runID.String(), nodeID.String(), raw, nowUTC(),
	)
	if err != nil {
		return fmt.Errorf("node_attributes.SetDispatchInputBag: %w", err)
	}
	return nil
}

// @concept: cascade
func (s *nodeAttributesImpl) GetDispatchInputBag(
	ctx context.Context, runID shared.UUID, tx persistence.Tx,
) (map[string]any, error) {
	var raw []byte
	err := s.q(tx).QueryRowContext(ctx,
		`SELECT dispatch_input_bag FROM rimsky_node_attributes WHERE node_run_id = ?`, runID.String(),
	).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("node_attributes.GetDispatchInputBag: %w", err)
	}
	if len(raw) == 0 {
		return nil, nil
	}
	out := map[string]any{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("node_attributes.GetDispatchInputBag: unmarshal: %w", err)
	}
	return out, nil
}

// @concept: cascade
// @decision: non-cascade-direct-to-stale
// @story: resume-preserves-snapshot
func (s *nodeAttributesImpl) SnapshotBagForNewRun(
	ctx context.Context, newRunID, nodeID, runScopeID shared.UUID, tx persistence.Tx,
) error {
	var priorData []byte
	err := s.q(tx).QueryRowContext(ctx,
		`SELECT a.data
		   FROM rimsky_node_attributes a
		   JOIN rimsky_node_runs r ON r.id = a.node_run_id
		  WHERE a.node_id = ?
		    AND r.run_scope_id = ?
		    AND r.id <> ?
		  ORDER BY r.sequence DESC
		  LIMIT 1`,
		nodeID.String(), runScopeID.String(), newRunID.String(),
	).Scan(&priorData)
	if errors.Is(err, sql.ErrNoRows) {
		if _, err := s.q(tx).ExecContext(ctx,
			`INSERT INTO rimsky_node_attributes
			   (node_run_id, node_id, data, dispatch_input_bag, updated_at)
			 VALUES (?, ?, '{}', '{}', ?)
			 ON CONFLICT(node_run_id) DO NOTHING`,
			newRunID.String(), nodeID.String(), nowUTC(),
		); err != nil {
			return fmt.Errorf("node_attributes.SnapshotBagForNewRun: insert empty: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("node_attributes.SnapshotBagForNewRun: load prior: %w", err)
	}
	if _, err := s.q(tx).ExecContext(ctx,
		`INSERT INTO rimsky_node_attributes
		   (node_run_id, node_id, data, dispatch_input_bag, updated_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(node_run_id) DO NOTHING`,
		newRunID.String(), nodeID.String(), priorData, priorData, nowUTC(),
	); err != nil {
		return fmt.Errorf("node_attributes.SnapshotBagForNewRun: insert carry-forward: %w", err)
	}
	return nil
}

// @concept: cascade
// @decision: frame-isolation-is-structural
func (s *nodeAttributesImpl) GetPriorRunData(
	ctx context.Context, runID shared.UUID, tx persistence.Tx,
) (map[string]any, error) {
	row := s.q(tx).QueryRowContext(ctx,
		`SELECT a.node_run_id, a.node_id, a.data, a.updated_at
		   FROM rimsky_node_attributes a
		   JOIN rimsky_node_runs r ON r.id = a.node_run_id
		   JOIN rimsky_node_runs cur ON cur.id = ?
		  WHERE r.node_id = cur.node_id
		    AND r.run_scope_id = cur.run_scope_id
		    AND r.sequence < cur.sequence
		  ORDER BY r.sequence DESC
		  LIMIT 1`,
		runID.String(),
	)
	prior, err := scanAttributeRow(row, "GetPriorRunData")
	if err != nil {
		return nil, err
	}
	if prior == nil {
		return nil, nil
	}
	if prior.Data == nil {
		return map[string]any{}, nil
	}
	return prior.Data, nil
}
