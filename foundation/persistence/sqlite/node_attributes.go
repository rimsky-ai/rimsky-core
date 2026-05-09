// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// node_attributes.go — SQLite-backed persistence.NodeAttributesStore.
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

	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/modeling/shared"
)

func (s *nodeAttributesImpl) Get(ctx context.Context, nodeID shared.UUID, tx persistence.Tx) (*persistence.NodeAttributesRow, error) {
	row := s.q(tx).QueryRowContext(ctx,
		`SELECT node_id, run_attempt, data, updated_at, value_handle, value_handle_backend
		   FROM rimsky_node_attributes
		  WHERE node_id = ?`, nodeID.String(),
	)
	var (
		idStr        string
		runAttempt   int
		dataStr      string
		updatedAtStr string
		handle       sql.NullString
		handleBkend  sql.NullString
	)
	if err := row.Scan(&idStr, &runAttempt, &dataStr, &updatedAtStr, &handle, &handleBkend); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("node_attributes.Get: %w", err)
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		return nil, fmt.Errorf("node_attributes.Get: bad id: %w", err)
	}
	updatedAt, err := parseTime(updatedAtStr)
	if err != nil {
		return nil, err
	}
	out := persistence.NodeAttributesRow{
		NodeID:     id,
		RunAttempt: runAttempt,
		UpdatedAt:  updatedAt,
	}

	bb := (*storeImpl)(s).blob
	if handle.Valid && handle.String != "" && bb != nil && handleBkend.Valid && handleBkend.String == bb.Name() {
		bytes, err := bb.Read(ctx, persistence.Handle(handle.String))
		if err != nil {
			if errors.Is(err, persistence.ErrBlobNotFound) {
				out.Data = map[string]any{}
				return &out, nil
			}
			return nil, fmt.Errorf("node_attributes.Get: blob.Read(%s): %w", handle.String, err)
		}
		m := map[string]any{}
		if len(bytes) > 0 {
			if err := json.Unmarshal(bytes, &m); err != nil {
				return nil, fmt.Errorf("node_attributes.Get: unmarshal blob bytes: %w", err)
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
			return nil, fmt.Errorf("node_attributes.Get: unmarshal data: %w", err)
		}
		out.Data = m
	}
	return &out, nil
}

func (s *nodeAttributesImpl) Upsert(ctx context.Context, nodeID shared.UUID, runAttempt int, data map[string]any, tx persistence.Tx) error {
	if data == nil {
		data = map[string]any{}
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("node_attributes.Upsert: marshal: %w", err)
	}

	si := (*storeImpl)(s)
	priorHandle, priorBkend, err := readPriorBlobHandle(ctx, si.q(tx), nodeID)
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
			NodeID:        nodeID.String(),
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
		`INSERT INTO rimsky_node_attributes (node_id, run_attempt, data, updated_at, value_handle, value_handle_backend)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(node_id) DO UPDATE
		   SET run_attempt          = excluded.run_attempt,
		       data                 = excluded.data,
		       updated_at           = excluded.updated_at,
		       value_handle         = excluded.value_handle,
		       value_handle_backend = excluded.value_handle_backend`,
		nodeID.String(), runAttempt, dataToSave, nowUTC(), nullHandle, nullBackend,
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
// Spill-aware: when the existing row is spilled, materialize via Get,
// merge, and re-Upsert (which re-applies the spill decision and queues
// orphans). When inline today, run the legacy read-then-write merge.
//
// nil-delta is a no-op merge: bumps updated_at if the row exists, silent
// no-op if absent. Mirrors postgres impl semantics.
//
// Atomicity: when tx != nil the read-then-write runs inside the caller's
// BEGIN IMMEDIATE tx (writer-slot held for the duration). When tx == nil
// the SQLite driver's MaxOpenConns=1 (see sqliteMaxOpenConns in
// driver.go) serializes any concurrent caller at the connection level.
func (s *nodeAttributesImpl) MergeDelta(ctx context.Context, nodeID shared.UUID, delta map[string]any, tx persistence.Tx) error {
	if delta == nil {
		_, err := s.q(tx).ExecContext(ctx,
			`UPDATE rimsky_node_attributes
			    SET updated_at = ?
			  WHERE node_id = ?`,
			nowUTC(), nodeID.String(),
		)
		if err != nil {
			return fmt.Errorf("node_attributes.MergeDelta: touch: %w", err)
		}
		return nil
	}

	si := (*storeImpl)(s)

	priorHandle, _, err := readPriorBlobHandle(ctx, si.q(tx), nodeID)
	if err != nil {
		return fmt.Errorf("node_attributes.MergeDelta: read prior handle: %w", err)
	}
	if priorHandle != "" {
		prior, err := s.Get(ctx, nodeID, tx)
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
		return s.Upsert(ctx, nodeID, prior.RunAttempt, merged, tx)
	}

	row := s.q(tx).QueryRowContext(ctx,
		`SELECT data FROM rimsky_node_attributes WHERE node_id = ?`,
		nodeID.String(),
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
		  WHERE node_id = ?`,
		string(merged), nowUTC(), nodeID.String(),
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
// for nodeID (or empty strings when the row does not exist or has no
// handle). Errors only on actual query failure — absence is signaled
// by both return strings being empty.
func readPriorBlobHandle(ctx context.Context, q querier, nodeID shared.UUID) (string, string, error) {
	row := q.QueryRowContext(ctx,
		`SELECT value_handle, value_handle_backend
		   FROM rimsky_node_attributes
		  WHERE node_id = ?`, nodeID.String(),
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
