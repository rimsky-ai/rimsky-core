// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

func (s *nodeAttributesImpl) GetByRun(ctx context.Context, runID shared.UUID, tx persistence.Tx) (*persistence.NodeAttributesRow, error) {
	row := s.q(tx).QueryRow(ctx,
		`SELECT node_run_id, node_id, data, updated_at
		   FROM rimsky_node_attributes
		  WHERE node_run_id = $1`, runID,
	)
	return scanAttributeRow(row, "GetByRun")
}

func (s *nodeAttributesImpl) GetLatestByNode(ctx context.Context, nodeID shared.UUID, runScopeID shared.UUID, tx persistence.Tx) (*persistence.NodeAttributesRow, error) {
	row := s.q(tx).QueryRow(ctx,
		`SELECT a.node_run_id, a.node_id, a.data, a.updated_at
		   FROM rimsky_node_attributes a
		   JOIN rimsky_node_runs r ON r.id = a.node_run_id
		  WHERE a.node_id = $1
		    AND r.run_scope_id = $2
		  ORDER BY a.updated_at DESC, r.sequence DESC
		  LIMIT 1`, nodeID, runScopeID,
	)
	return scanAttributeRow(row, "GetLatestByNode")
}

// @decision: attribute-bytes-in-the-row
func scanAttributeRow(row pgx.Row, op string) (*persistence.NodeAttributesRow, error) {
	var (
		out  persistence.NodeAttributesRow
		raw  []byte
		when time.Time
	)
	if err := row.Scan(&out.NodeRunID, &out.NodeID, &raw, &when); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("node_attributes.%s: %w", op, err)
	}
	out.UpdatedAt = when
	m := map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &m); err != nil {
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

	_, err = s.q(tx).Exec(ctx,
		`INSERT INTO rimsky_node_attributes (node_run_id, node_id, data, updated_at)
		 VALUES ($1, $2, $3, now())
		 ON CONFLICT (node_run_id) DO UPDATE
		   SET data       = EXCLUDED.data,
		       updated_at = now()`,
		runID, nodeID, raw,
	)
	if err != nil {
		return fmt.Errorf("node_attributes.Upsert: %w", err)
	}
	return nil
}

// @decision: attribute-bytes-in-the-row
func (s *nodeAttributesImpl) MergeDelta(ctx context.Context, runID shared.UUID, delta map[string]any, tx persistence.Tx) error {
	if delta == nil {
		tag, err := s.q(tx).Exec(ctx,
			`UPDATE rimsky_node_attributes
			    SET updated_at = now()
			  WHERE node_run_id = $1`,
			runID,
		)
		if err != nil {
			return fmt.Errorf("node_attributes.MergeDelta: touch: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("node_attributes.MergeDelta: touch: %w", persistence.ErrNotFound)
		}
		return nil
	}

	var current []byte
	if err := s.q(tx).QueryRow(ctx,
		`SELECT data FROM rimsky_node_attributes WHERE node_run_id = $1 FOR UPDATE`, runID,
	).Scan(&current); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
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
	tag, err := s.q(tx).Exec(ctx,
		`UPDATE rimsky_node_attributes
		    SET data = $2,
		        updated_at = now()
		  WHERE node_run_id = $1`,
		runID, merged,
	)
	if err != nil {
		return fmt.Errorf("node_attributes.MergeDelta: %w", err)
	}
	if tag.RowsAffected() == 0 {
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
	_, err = s.q(tx).Exec(ctx,
		`INSERT INTO rimsky_node_attributes (node_run_id, node_id, data, dispatch_input_bag, updated_at)
		 VALUES ($1, $2, '{}', $3, now())
		 ON CONFLICT (node_run_id) DO UPDATE
		   SET dispatch_input_bag = EXCLUDED.dispatch_input_bag,
		       updated_at         = now()`,
		runID, nodeID, raw,
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
	err := s.q(tx).QueryRow(ctx,
		`SELECT dispatch_input_bag FROM rimsky_node_attributes WHERE node_run_id = $1`, runID,
	).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
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
	err := s.q(tx).QueryRow(ctx,
		`SELECT a.data
		   FROM rimsky_node_attributes a
		   JOIN rimsky_node_runs r ON r.id = a.node_run_id
		  WHERE a.node_id = $1
		    AND r.run_scope_id = $2
		    AND r.id <> $3
		  ORDER BY r.sequence DESC
		  LIMIT 1`,
		nodeID, runScopeID, newRunID,
	).Scan(&priorData)
	if errors.Is(err, pgx.ErrNoRows) {
		if _, err := s.q(tx).Exec(ctx,
			`INSERT INTO rimsky_node_attributes
			   (node_run_id, node_id, data, dispatch_input_bag, updated_at)
			 VALUES ($1, $2, '{}', '{}', NOW())
			 ON CONFLICT (node_run_id) DO NOTHING`,
			newRunID, nodeID,
		); err != nil {
			return fmt.Errorf("node_attributes.SnapshotBagForNewRun: insert empty: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("node_attributes.SnapshotBagForNewRun: load prior: %w", err)
	}
	if _, err := s.q(tx).Exec(ctx,
		`INSERT INTO rimsky_node_attributes
		   (node_run_id, node_id, data, dispatch_input_bag, updated_at)
		 VALUES ($1, $2, $3, $4, NOW())
		 ON CONFLICT (node_run_id) DO NOTHING`,
		newRunID, nodeID, priorData, priorData,
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
	row := s.q(tx).QueryRow(ctx,
		`SELECT a.node_run_id, a.node_id, a.data, a.updated_at
		   FROM rimsky_node_attributes a
		   JOIN rimsky_node_runs r ON r.id = a.node_run_id
		   JOIN rimsky_node_runs cur ON cur.id = $1
		  WHERE r.node_id = cur.node_id
		    AND r.run_scope_id = cur.run_scope_id
		    AND r.sequence < cur.sequence
		  ORDER BY r.sequence DESC
		  LIMIT 1`,
		runID,
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
