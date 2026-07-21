// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

func (s *nodeAttributesImpl) GetByRun(ctx context.Context, runID shared.UUID, tx persistence.Tx) (*persistence.NodeAttributesRow, error) {
	row := s.q(tx).QueryRow(ctx,
		`SELECT node_run_id, node_id, data, updated_at, value_handle, value_handle_backend
		   FROM rimsky_node_attributes
		  WHERE node_run_id = $1`, runID,
	)
	return scanAttributeRow(ctx, (*tablesImpl)(s).blob, row, "GetByRun", tx)
}

func (s *nodeAttributesImpl) GetLatestByNode(ctx context.Context, nodeID shared.UUID, runScopeID shared.UUID, tx persistence.Tx) (*persistence.NodeAttributesRow, error) {
	row := s.q(tx).QueryRow(ctx,
		`SELECT a.node_run_id, a.node_id, a.data, a.updated_at, a.value_handle, a.value_handle_backend
		   FROM rimsky_node_attributes a
		   JOIN rimsky_node_runs r ON r.id = a.node_run_id
		  WHERE a.node_id = $1
		    AND r.run_scope_id = $2
		  ORDER BY a.updated_at DESC, r.sequence DESC
		  LIMIT 1`, nodeID, runScopeID,
	)
	return scanAttributeRow(ctx, (*tablesImpl)(s).blob, row, "GetLatestByNode", tx)
}

func blobWriteIsTransactional(bb persistence.BlobBackend) bool {
	_, ok := bb.(persistence.TxBlobBackend)
	return ok
}

func blobBackendName(bb persistence.BlobBackend) string {
	if bb == nil {
		return "<none>"
	}
	return bb.Name()
}

func scanAttributeRow(ctx context.Context, bb persistence.BlobBackend, row pgx.Row, op string, tx persistence.Tx) (*persistence.NodeAttributesRow, error) {
	var (
		out         persistence.NodeAttributesRow
		raw         []byte
		when        time.Time
		handle      *string
		handleBkend *string
	)
	if err := row.Scan(&out.NodeRunID, &out.NodeID, &raw, &when, &handle, &handleBkend); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("node_attributes.%s: %w", op, err)
	}
	out.UpdatedAt = when
	if handle != nil && *handle != "" {
		if bb == nil || handleBkend == nil || *handleBkend != bb.Name() {
			rowBackend := "<none>"
			if handleBkend != nil {
				rowBackend = *handleBkend
			}
			return nil, fmt.Errorf("node_attributes.%s: row has value_handle %q on backend %q, but active blob backend is %q",
				op, *handle, rowBackend, blobBackendName(bb))
		}
		bytes, err := persistence.ReadBlobInTx(ctx, bb, persistence.Handle(*handle), tx)
		if err != nil {
			if errors.Is(err, persistence.ErrBlobNotFound) {
				slog.Error("node_attributes.spilled_value_missing",
					"op", op, "node_run_id", out.NodeRunID.String(), "handle", *handle, "backend", *handleBkend)
				out.Data = map[string]any{}
				return &out, nil
			}
			return nil, fmt.Errorf("node_attributes.%s: blob.Read(%s): %w", op, *handle, err)
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
	if len(raw) == 0 {
		out.Data = map[string]any{}
	} else {
		m := map[string]any{}
		if err := json.Unmarshal(raw, &m); err != nil {
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
		dataToSave = raw
	)
	if persistence.ShouldSpillBlob(si.blob, si.blobThreshold, len(raw)) {
		h, werr := persistence.WriteBlobInTx(ctx, si.blob, persistence.BlobKey{
			NodeID:        nodeID.String(),
			AttributeName: "data",
		}, raw, tx)
		if werr != nil {
			return fmt.Errorf("node_attributes.Upsert: blob.Write: %w", werr)
		}
		newHandle = string(h)
		newBackend = si.blob.Name()
		dataToSave = []byte(`{}`)
	}

	if newHandle != "" {
		_, err = si.q(tx).Exec(ctx,
			`INSERT INTO rimsky_node_attributes (node_run_id, node_id, data, updated_at, value_handle, value_handle_backend)
			 VALUES ($1, $2, $3::jsonb, now(), $4, $5)
			 ON CONFLICT (node_run_id) DO UPDATE
			   SET data                 = EXCLUDED.data,
			       value_handle         = EXCLUDED.value_handle,
			       value_handle_backend = EXCLUDED.value_handle_backend,
			       updated_at           = now()`,
			runID, nodeID, dataToSave, newHandle, newBackend,
		)
	} else {
		_, err = si.q(tx).Exec(ctx,
			`INSERT INTO rimsky_node_attributes (node_run_id, node_id, data, updated_at, value_handle, value_handle_backend)
			 VALUES ($1, $2, $3::jsonb, now(), NULL, NULL)
			 ON CONFLICT (node_run_id) DO UPDATE
			   SET data                 = EXCLUDED.data,
			       value_handle         = NULL,
			       value_handle_backend = NULL,
			       updated_at           = now()`,
			runID, nodeID, dataToSave,
		)
	}
	if err != nil {
		if newHandle != "" && !blobWriteIsTransactional(si.blob) {
			if delErr := si.blob.Delete(ctx, persistence.Handle(newHandle)); delErr != nil {
				return fmt.Errorf("node_attributes.Upsert: %w (cleanup of orphaned blob %s also failed: %v)", err, newHandle, delErr)
			}
		}
		return fmt.Errorf("node_attributes.Upsert: %w", err)
	}

	if priorHandle != "" && priorHandle != newHandle {
		now := time.Now().UTC()
		if err := persistence.QueueBlobOrphan(ctx, si.BlobOrphans(), priorHandle, priorBkend, now, si.blobRetention, tx); err != nil {
			return fmt.Errorf("node_attributes.Upsert: queue prior orphan: %w", err)
		}
	}
	return nil
}

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

	raw, err := json.Marshal(delta)
	if err != nil {
		return fmt.Errorf("node_attributes.MergeDelta: marshal: %w", err)
	}
	tag, err := s.q(tx).Exec(ctx,
		`UPDATE rimsky_node_attributes
		    SET data = data || $2::jsonb,
		        updated_at = now()
		  WHERE node_run_id = $1`,
		runID, raw,
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
	_, err = s.q(tx).Exec(ctx,
		`INSERT INTO rimsky_node_attributes (node_run_id, node_id, data, dispatch_input_bag, updated_at)
		 VALUES ($1, $2, '{}'::jsonb, $3::jsonb, now())
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
	var (
		priorData          []byte
		priorHandle        *string
		priorHandleBackend *string
	)
	err := s.q(tx).QueryRow(ctx,
		`SELECT a.data, a.value_handle, a.value_handle_backend
		   FROM rimsky_node_attributes a
		   JOIN rimsky_node_runs r ON r.id = a.node_run_id
		  WHERE a.node_id = $1
		    AND r.run_scope_id = $2
		    AND r.id <> $3
		  ORDER BY r.sequence DESC
		  LIMIT 1`,
		nodeID, runScopeID, newRunID,
	).Scan(&priorData, &priorHandle, &priorHandleBackend)
	if errors.Is(err, pgx.ErrNoRows) {
		if _, err := s.q(tx).Exec(ctx,
			`INSERT INTO rimsky_node_attributes
			   (node_run_id, node_id, data, dispatch_input_bag, updated_at)
			 VALUES ($1, $2, '{}'::jsonb, '{}'::jsonb, NOW())
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
	priorHandleStr := ""
	if priorHandle != nil {
		priorHandleStr = *priorHandle
	}
	priorBackendStr := ""
	if priorHandleBackend != nil {
		priorBackendStr = *priorHandleBackend
	}
	carried, err := persistence.CarryForwardBag(ctx, (*tablesImpl)(s).blob, persistence.BlobKey{NodeID: nodeID.String(), AttributeName: "data"}, priorData, priorHandleStr, priorBackendStr, tx)
	if err != nil {
		return fmt.Errorf("node_attributes.SnapshotBagForNewRun: carry forward blob: %w", err)
	}
	var handleArg, backendArg any
	if carried.Handle != "" {
		handleArg = carried.Handle
		backendArg = carried.Backend
	}
	if _, err := s.q(tx).Exec(ctx,
		`INSERT INTO rimsky_node_attributes
		   (node_run_id, node_id, data, dispatch_input_bag, value_handle, value_handle_backend, updated_at)
		 VALUES ($1, $2, $3::jsonb, $4::jsonb, $5, $6, NOW())
		 ON CONFLICT (node_run_id) DO NOTHING`,
		newRunID, nodeID, carried.Data, carried.DispatchBag, handleArg, backendArg,
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
		`SELECT a.node_run_id, a.node_id, a.data, a.updated_at, a.value_handle, a.value_handle_backend
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
	prior, err := scanAttributeRow(ctx, (*tablesImpl)(s).blob, row, "GetPriorRunData", tx)
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
	row := q.QueryRow(ctx,
		`SELECT value_handle, value_handle_backend
		   FROM rimsky_node_attributes
		  WHERE node_run_id = $1`, runID,
	)
	var h, b *string
	if err := row.Scan(&h, &b); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", "", nil
		}
		return "", "", err
	}
	if h == nil || *h == "" {
		return "", "", nil
	}
	bk := ""
	if b != nil {
		bk = *b
	}
	return *h, bk, nil
}
