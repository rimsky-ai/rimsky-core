// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

func blobBackendName(bb persistence.BlobBackend) string {
	if bb == nil {
		return "<none>"
	}
	return bb.Name()
}

func (s *nodeAttributesImpl) GetByRun(ctx context.Context, runID shared.UUID, tx persistence.Tx) (*persistence.NodeAttributesRow, error) {
	row := s.q(tx).QueryRowContext(ctx,
		`SELECT node_run_id, node_id, data, updated_at, value_handle, value_handle_backend
		   FROM rimsky_node_attributes
		  WHERE node_run_id = ?`, runID.String(),
	)
	return scanAttributeRow(ctx, (*tablesImpl)(s).blob, row, "GetByRun")
}

func (s *nodeAttributesImpl) GetLatestByNode(ctx context.Context, nodeID shared.UUID, runScopeID shared.UUID, tx persistence.Tx) (*persistence.NodeAttributesRow, error) {
	row := s.q(tx).QueryRowContext(ctx,
		`SELECT a.node_run_id, a.node_id, a.data, a.updated_at, a.value_handle, a.value_handle_backend
		   FROM rimsky_node_attributes a
		   JOIN rimsky_node_runs r ON r.id = a.node_run_id
		  WHERE a.node_id = ?
		    AND r.run_scope_id = ?
		  ORDER BY a.updated_at DESC, r.sequence DESC
		  LIMIT 1`, nodeID.String(), runScopeID.String(),
	)
	return scanAttributeRow(ctx, (*tablesImpl)(s).blob, row, "GetLatestByNode")
}

func scanAttributeRow(ctx context.Context, bb persistence.BlobBackend, row scannable, op string) (*persistence.NodeAttributesRow, error) {
	var (
		runIDStr     string
		nodeIDStr    string
		dataStr      string
		updatedAtStr string
		handle       sql.NullString
		handleBkend  sql.NullString
	)
	if err := row.Scan(&runIDStr, &nodeIDStr, &dataStr, &updatedAtStr, &handle, &handleBkend); err != nil {
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

	if handle.Valid && handle.String != "" {
		if bb == nil || !handleBkend.Valid || handleBkend.String != bb.Name() {
			rowBackend := "<none>"
			if handleBkend.Valid {
				rowBackend = handleBkend.String
			}
			return nil, fmt.Errorf("node_attributes.%s: row has value_handle %q on backend %q, but active blob backend is %q",
				op, handle.String, rowBackend, blobBackendName(bb))
		}
		bytes, err := bb.Read(ctx, persistence.Handle(handle.String))
		if err != nil {
			return nil, fmt.Errorf("node_attributes.%s: blob.Read(%s): %w", op, handle.String, err)
		}
		m := map[string]any{}
		if len(bytes) > 0 {
			if err := json.Unmarshal(bytes, &m); err != nil {
				return nil, fmt.Errorf("node_attributes.%s: unmarshal blob bytes: %w", op, err)
			}
		}
		out.Data = m
		return &out, nil
	}
	if dataStr == "" {
		out.Data = map[string]any{}
	} else {
		m := map[string]any{}
		if err := json.Unmarshal([]byte(dataStr), &m); err != nil {
			return nil, fmt.Errorf("node_attributes.%s: unmarshal data: %w", op, err)
		}
		out.Data = m
	}
	return &out, nil
}

func (s *nodeAttributesImpl) Upsert(ctx context.Context, runID, nodeID shared.UUID, data map[string]any, tx persistence.Tx) error {
	if data == nil {
		data = map[string]any{}
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("node_attributes.Upsert: marshal: %w", err)
	}

	si := (*tablesImpl)(s)
	priorHandle, priorBkend, err := readPriorBlobHandle(ctx, si.q(tx), runID)
	if err != nil {
		return fmt.Errorf("node_attributes.Upsert: read prior handle: %w", err)
	}

	var (
		newHandle  string
		newBackend string
		dataToSave = string(raw)
	)
	if persistence.ShouldSpillBlob(si.blob, si.blobThreshold, len(raw)) {
		h, werr := si.blob.Write(ctx, persistence.BlobKey{
			NodeID:        runID.String(),
			AttributeName: "data",
		}, raw)
		if werr != nil {
			return fmt.Errorf("node_attributes.Upsert: blob.Write: %w", werr)
		}
		newHandle = string(h)
		newBackend = si.blob.Name()
		dataToSave = "{}"
	}

	var (
		nullHandle  any
		nullBackend any
	)
	if newHandle != "" {
		nullHandle = newHandle
		nullBackend = newBackend
	} else {
		nullHandle = nil
		nullBackend = nil
	}
	_, err = si.q(tx).ExecContext(ctx,
		`INSERT INTO rimsky_node_attributes (node_run_id, node_id, data, updated_at, value_handle, value_handle_backend)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(node_run_id) DO UPDATE
		   SET data                 = excluded.data,
		       updated_at           = excluded.updated_at,
		       value_handle         = excluded.value_handle,
		       value_handle_backend = excluded.value_handle_backend`,
		runID.String(), nodeID.String(), dataToSave, nowUTC(), nullHandle, nullBackend,
	)
	if err != nil {
		if newHandle != "" {
			if delErr := si.blob.Delete(ctx, persistence.Handle(newHandle)); delErr != nil {
				return fmt.Errorf("node_attributes.Upsert: %w (cleanup of orphaned blob %s also failed: %v)", err, newHandle, delErr)
			}
		}
		return fmt.Errorf("node_attributes.Upsert: %w", err)
	}

	if priorHandle != "" && priorHandle != newHandle {
		now := time.Now().UTC()
		if err := persistence.QueueBlobOrphan(ctx, si.BlobOrphans(), tx,
			priorHandle, priorBkend, now, si.blobRetention); err != nil {
			return fmt.Errorf("node_attributes.Upsert: queue prior orphan: %w", err)
		}
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

	si := (*tablesImpl)(s)

	priorHandle, _, err := readPriorBlobHandle(ctx, si.q(tx), runID)
	if err != nil {
		return fmt.Errorf("node_attributes.MergeDelta: read prior handle: %w", err)
	}
	if priorHandle != "" {
		prior, err := s.GetByRun(ctx, runID, tx)
		if err != nil {
			return fmt.Errorf("node_attributes.MergeDelta: get prior: %w", err)
		}
		if prior == nil {
			return fmt.Errorf("node_attributes.MergeDelta: %w", persistence.ErrNotFound)
		}
		merged := prior.Data
		if merged == nil {
			merged = map[string]any{}
		}
		for k, v := range delta {
			merged[k] = v
		}
		return s.Upsert(ctx, runID, prior.NodeID, merged, tx)
	}

	row := s.q(tx).QueryRowContext(ctx,
		`SELECT data FROM rimsky_node_attributes WHERE node_run_id = ?`,
		runID.String(),
	)
	var dataStr string
	if err := row.Scan(&dataStr); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("node_attributes.MergeDelta: %w", persistence.ErrNotFound)
		}
		return fmt.Errorf("node_attributes.MergeDelta: read: %w", err)
	}
	current := map[string]any{}
	if dataStr != "" {
		if err := json.Unmarshal([]byte(dataStr), &current); err != nil {
			return fmt.Errorf("node_attributes.MergeDelta: unmarshal current: %w", err)
		}
	}
	for k, v := range delta {
		current[k] = v
	}
	merged, err := json.Marshal(current)
	if err != nil {
		return fmt.Errorf("node_attributes.MergeDelta: marshal: %w", err)
	}
	res, err := s.q(tx).ExecContext(ctx,
		`UPDATE rimsky_node_attributes
		    SET data = ?,
		        updated_at = ?
		  WHERE node_run_id = ?`,
		string(merged), nowUTC(), runID.String(),
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
	ctx context.Context, tx persistence.Tx, runID, nodeID shared.UUID, bag map[string]any,
) error {
	if bag == nil {
		bag = map[string]any{}
	}
	raw, err := json.Marshal(bag)
	if err != nil {
		return fmt.Errorf("node_attributes.SetDispatchInputBag: marshal: %w", err)
	}
	_, err = s.q(tx).ExecContext(ctx,
		`INSERT INTO rimsky_node_attributes (node_run_id, node_id, data, dispatch_input_bag, updated_at)
		 VALUES (?, ?, '{}', ?, ?)
		 ON CONFLICT(node_run_id) DO UPDATE
		   SET dispatch_input_bag = excluded.dispatch_input_bag,
		       updated_at         = excluded.updated_at`,
		runID.String(), nodeID.String(), string(raw), nowUTC(),
	)
	if err != nil {
		return fmt.Errorf("node_attributes.SetDispatchInputBag: %w", err)
	}
	return nil
}

// @concept: cascade
func (s *nodeAttributesImpl) GetDispatchInputBag(
	ctx context.Context, tx persistence.Tx, runID shared.UUID,
) (map[string]any, error) {
	var raw sql.NullString
	err := s.q(tx).QueryRowContext(ctx,
		`SELECT dispatch_input_bag FROM rimsky_node_attributes WHERE node_run_id = ?`, runID.String(),
	).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("node_attributes.GetDispatchInputBag: %w", err)
	}
	if !raw.Valid || raw.String == "" {
		return nil, nil
	}
	out := map[string]any{}
	if err := json.Unmarshal([]byte(raw.String), &out); err != nil {
		return nil, fmt.Errorf("node_attributes.GetDispatchInputBag: unmarshal: %w", err)
	}
	return out, nil
}

// @concept: cascade
// @decision: non-cascade-direct-to-stale
// @story: resume-preserves-snapshot
func (s *nodeAttributesImpl) SnapshotBagForNewRun(
	ctx context.Context, tx persistence.Tx,
	newRunID, nodeID, runScopeID shared.UUID,
) error {
	var (
		priorData          string
		priorHandle        sql.NullString
		priorHandleBackend sql.NullString
	)
	err := s.q(tx).QueryRowContext(ctx,
		`SELECT a.data, a.value_handle, a.value_handle_backend
		   FROM rimsky_node_attributes a
		   JOIN rimsky_node_runs r ON r.id = a.node_run_id
		  WHERE a.node_id = ?
		    AND r.run_scope_id = ?
		    AND r.id <> ?
		  ORDER BY r.sequence DESC
		  LIMIT 1`,
		nodeID.String(), runScopeID.String(), newRunID.String(),
	).Scan(&priorData, &priorHandle, &priorHandleBackend)
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
	priorHandleStr := ""
	if priorHandle.Valid {
		priorHandleStr = priorHandle.String
	}
	priorBackendStr := ""
	if priorHandleBackend.Valid {
		priorBackendStr = priorHandleBackend.String
	}
	carried, err := persistence.CarryForwardBag(ctx, (*tablesImpl)(s).blob, tx,
		persistence.BlobKey{NodeID: newRunID.String(), AttributeName: "data"},
		[]byte(priorData), priorHandleStr, priorBackendStr)
	if err != nil {
		return fmt.Errorf("node_attributes.SnapshotBagForNewRun: carry forward blob: %w", err)
	}
	var handleArg, backendArg any
	if carried.Handle != "" {
		handleArg = carried.Handle
		backendArg = carried.Backend
	}
	if _, err := s.q(tx).ExecContext(ctx,
		`INSERT INTO rimsky_node_attributes
		   (node_run_id, node_id, data, dispatch_input_bag, value_handle, value_handle_backend, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(node_run_id) DO NOTHING`,
		newRunID.String(), nodeID.String(), string(carried.Data), string(carried.DispatchBag),
		handleArg, backendArg, nowUTC(),
	); err != nil {
		return fmt.Errorf("node_attributes.SnapshotBagForNewRun: insert carry-forward: %w", err)
	}
	return nil
}

// @concept: cascade
// @decision: frame-isolation-is-structural
func (s *nodeAttributesImpl) GetPriorRunData(
	ctx context.Context, tx persistence.Tx, runID shared.UUID,
) (map[string]any, error) {
	row := s.q(tx).QueryRowContext(ctx,
		`SELECT a.node_run_id, a.node_id, a.data, a.updated_at, a.value_handle, a.value_handle_backend
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
	prior, err := scanAttributeRow(ctx, (*tablesImpl)(s).blob, row, "GetPriorRunData")
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

func readPriorBlobHandle(ctx context.Context, q querier, runID shared.UUID) (string, string, error) {
	row := q.QueryRowContext(ctx,
		`SELECT value_handle, value_handle_backend
		   FROM rimsky_node_attributes
		  WHERE node_run_id = ?`, runID.String(),
	)
	var h, b sql.NullString
	if err := row.Scan(&h, &b); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", "", nil
		}
		return "", "", err
	}
	if !h.Valid || h.String == "" {
		return "", "", nil
	}
	bk := ""
	if b.Valid {
		bk = b.String
	}
	return h.String, bk, nil
}
