// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// node_attributes.go — SQLite-backed persistence.NodeAttributeTable.
//
// Under per-run keying (2026-05-20), the table is keyed by node_run_id
// with a denormalized node_id for forensic queries.
//
// `data` is a TEXT (JSON) column. Upsert replaces it outright; MergeDelta
// performs a SHALLOW merge by reading the existing row, merging in Go,
// and writing back — SQLite has no JSONB `||` operator.
//
// Blob spill (plan §D6/D7): mirrors the postgres impl. When a configured
// BlobBackend is non-nil and the marshalled `data` exceeds the spill
// threshold, the bytes are written through the backend, the returned
// handle is stored in value_handle + value_handle_backend, and the
// inline `data` column is reset to '{}'. Reads transparently dereference
// non-NULL value_handle entries via the backend. Overwriting a row that
// previously had a value_handle queues the old handle in
// rimsky_blob_orphans for the SweepOrphanedBlobs sweep.
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

// GetByRun returns the attribute row for runID or (nil, nil) when absent.
func (s *nodeAttributesImpl) GetByRun(ctx context.Context, runID shared.UUID, tx persistence.Tx) (*persistence.NodeAttributesRow, error) {
	row := s.q(tx).QueryRowContext(ctx,
		`SELECT node_run_id, node_id, data, updated_at, value_handle, value_handle_backend
		   FROM rimsky_node_attributes
		  WHERE node_run_id = ?`, runID.String(),
	)
	return scanAttributeRow(ctx, (*tablesImpl)(s).blob, row, "GetByRun")
}

// GetLatestByNode returns the most-recent attribute row for the
// (node, run scope) pair. Returns (nil, nil) when no row exists.
//
// Under RunScope-first (per spec
// .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md),
// the lookup is scoped: callers pick the RunScope first.
func (s *nodeAttributesImpl) GetLatestByNode(ctx context.Context, nodeID shared.UUID, runScopeID shared.UUID, tx persistence.Tx) (*persistence.NodeAttributesRow, error) {
	row := s.q(tx).QueryRowContext(ctx,
		`SELECT a.node_run_id, a.node_id, a.data, a.updated_at, a.value_handle, a.value_handle_backend
		   FROM rimsky_node_attributes a
		   JOIN rimsky_node_runs r ON r.id = a.node_run_id
		  WHERE a.node_id = ?
		    AND r.run_scope_id = ?
		  ORDER BY a.updated_at DESC
		  LIMIT 1`, nodeID.String(), runScopeID.String(),
	)
	return scanAttributeRow(ctx, (*tablesImpl)(s).blob, row, "GetLatestByNode")
}

// scanAttributeRow scans a single attribute row (six columns:
// node_run_id, node_id, data, updated_at, value_handle, value_handle_backend)
// and dereferences any blob-spilled value through the active backend.
// Returns (nil, nil) when the underlying scan returns sql.ErrNoRows.
// `op` is the calling method name, used in wrapped error messages.
func scanAttributeRow(ctx context.Context, bb persistence.BlobBackend, row rowScanner, op string) (*persistence.NodeAttributesRow, error) {
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

	if handle.Valid && handle.String != "" && bb != nil && handleBkend.Valid && handleBkend.String == bb.Name() {
		bytes, err := bb.Read(ctx, persistence.Handle(handle.String))
		if err != nil {
			if errors.Is(err, persistence.ErrBlobNotFound) {
				out.Data = map[string]any{}
				return &out, nil
			}
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
			NodeID:        runID.String(), // per-run keying
			AttributeName: "data",
		}, raw)
		if werr != nil {
			return fmt.Errorf("node_attributes.Upsert: blob.Write: %w", werr)
		}
		newHandle = string(h)
		newBackend = si.blob.Name()
		dataToSave = "{}"
	}

	// SQLite Upsert always writes value_handle / value_handle_backend
	// (NULL when not spilled, so a downgrade from spilled-to-inline
	// correctly clears the prior pointer).
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

// MergeDelta runs a SHALLOW merge. SQLite has no JSONB `||`; we read,
// merge in Go, and write back. Per spec §5.7.2.
//
// Spill-aware: when the existing row is spilled, materialize via GetByRun,
// merge, and re-Upsert (which re-applies the spill decision and queues
// orphans). When inline today, run the legacy read-then-write merge.
//
// nil-delta is a no-op merge: bumps updated_at if the row exists, silent
// no-op if absent. Mirrors postgres impl semantics.
//
// Atomicity: the read-then-write runs inside the caller's tx — a tx is
// mandatory on every Table method (tablesImpl.q panics on nil), and the
// DSN's _txlock=immediate makes the tx a BEGIN IMMEDIATE whose
// writer-slot hold keeps the merge atomic across OS processes sharing
// the database file, not just across goroutines.
func (s *nodeAttributesImpl) MergeDelta(ctx context.Context, runID shared.UUID, delta map[string]any, tx persistence.Tx) error {
	if delta == nil {
		_, err := s.q(tx).ExecContext(ctx,
			`UPDATE rimsky_node_attributes
			    SET updated_at = ?
			  WHERE node_run_id = ?`,
			nowUTC(), runID.String(),
		)
		if err != nil {
			return fmt.Errorf("node_attributes.MergeDelta: touch: %w", err)
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

// readPriorBlobHandle returns the value_handle / value_handle_backend
// for runID (or empty strings when the row does not exist or has no
// handle). Errors only on actual query failure — absence is signaled
// by both return strings being empty.
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
