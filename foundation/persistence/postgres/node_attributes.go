// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// node_attributes.go is the postgres accessor for `rimsky_node_attributes`
// (spec §9.9.1). Under per-run keying (2026-05-20), the table is keyed by
// node_run_id with a denormalized node_id column for forensic queries. A
// row is created lazily by the runner's pre-dispatch Upsert and lives until
// its parent node_run row is deleted (FK ON DELETE CASCADE).
//
// `data` is a JSONB column. Upsert replaces it outright; MergeDelta runs a
// SHALLOW JSONB merge (`data || $1::jsonb`) and requires the row to exist
// (spec §5.7.2). Every method takes a `tx persistence.Tx` so callers
// participate in the supervisor's tx when needed.
//
// Blob spill (plan §D6/D7): when a configured BlobBackend is non-nil and
// the marshalled `data` exceeds the spill threshold, the bytes are
// written through the backend, the returned handle is stored in
// value_handle + value_handle_backend, and the inline `data` column is
// reset to '{}'::jsonb. Reads transparently dereference: when
// value_handle is non-NULL the bytes come from the backend; otherwise
// the inline `data` is returned. Overwriting a row that previously had
// a value_handle inserts the old handle into rimsky_blob_orphans for
// the SweepOrphanedBlobs sweep to delete after the retention window.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/fallguyconsulting/rimsky/foundation/persistence"
	"github.com/fallguyconsulting/rimsky/foundation/shared"
)

// GetByRun returns the row for runID or (nil, nil) when no row exists.
//
// When the row's value_handle is non-NULL, the actual bytes live in the
// configured BlobBackend; this method dereferences the handle and
// returns the materialized data map. When value_handle is NULL the
// inline `data` JSONB is returned as today.
//
// @blessed-invariant 21: blob bytes flow through walkPath substitution
// only; this method materializes them without logging or transforming.
func (s *nodeAttributesImpl) GetByRun(ctx context.Context, runID shared.UUID, tx persistence.Tx) (*persistence.NodeAttributesRow, error) {
	row := s.q(tx).QueryRow(ctx,
		`SELECT node_run_id, node_id, data, updated_at, value_handle, value_handle_backend
		   FROM rimsky_node_attributes
		  WHERE node_run_id = $1`, runID,
	)
	return scanAttributeRow(ctx, (*tablesImpl)(s).blob, row, "GetByRun")
}

// GetLatestByNode returns the most-recent attribute row for the
// (node, run scope) pair. Returns (nil, nil) when no row exists.
//
// Under RunScope-first (per spec
// .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md),
// the lookup is scoped: under fan-out, multiple concurrent runs of
// the same node exist in different RunScopes; callers pick the
// scope before querying.
func (s *nodeAttributesImpl) GetLatestByNode(ctx context.Context, nodeID shared.UUID, runScopeID shared.UUID, tx persistence.Tx) (*persistence.NodeAttributesRow, error) {
	row := s.q(tx).QueryRow(ctx,
		`SELECT a.node_run_id, a.node_id, a.data, a.updated_at, a.value_handle, a.value_handle_backend
		   FROM rimsky_node_attributes a
		   JOIN rimsky_node_runs r ON r.id = a.node_run_id
		  WHERE a.node_id = $1
		    AND r.run_scope_id = $2
		  ORDER BY a.updated_at DESC
		  LIMIT 1`, nodeID, runScopeID,
	)
	return scanAttributeRow(ctx, (*tablesImpl)(s).blob, row, "GetLatestByNode")
}

// scanAttributeRow scans a single attribute row (six columns:
// node_run_id, node_id, data, updated_at, value_handle, value_handle_backend)
// and dereferences any blob-spilled value through the active backend.
// Returns (nil, nil) when the underlying scan returns pgx.ErrNoRows.
// `op` is the calling method name, used in wrapped error messages.
func scanAttributeRow(ctx context.Context, bb persistence.BlobBackend, row pgx.Row, op string) (*persistence.NodeAttributesRow, error) {
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
	// Spill-read: when value_handle is set and the row's recorded
	// backend matches the active backend, fetch the bytes from the
	// backend and unmarshal as the data map. When the recorded backend
	// differs from the active backend (cross-backend topology mismatch
	// during migration), fall back to the inline data column — this
	// preserves continuity for migrated deployments at the cost of
	// silently downgrading the row's storage; the operator's migration
	// tooling is responsible for re-spilling such rows.
	if handle != nil && *handle != "" && bb != nil && handleBkend != nil && *handleBkend == bb.Name() {
		bytes, err := bb.Read(ctx, persistence.Handle(*handle))
		if err != nil {
			if errors.Is(err, persistence.ErrBlobNotFound) {
				// The blob is missing — likely a SweepOrphanedBlobs race
				// or a deployment that lost its filesystem mount. Surface
				// as an empty data map so the substitution machinery sees
				// a well-defined absence rather than a partial JSON parse.
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

// Upsert writes (or replaces) the row for runID. `data` overwrites any
// prior value. nodeID is denormalized into the row for forensic lookups.
//
// When the marshalled data exceeds the configured spill threshold and a
// BlobBackend is installed, the bytes are written through the backend,
// the returned handle is stored in value_handle, and `data` is reset
// to '{}'::jsonb. Otherwise the legacy inline path runs.
//
// When the prior row had a non-NULL value_handle, the old handle is
// queued in rimsky_blob_orphans for the SweepOrphanedBlobs sweep.
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
		h, werr := si.blob.Write(ctx, persistence.BlobKey{
			NodeID:        runID.String(), // per-run keying
			AttributeName: "data",
		}, raw)
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
		// Inline path: explicitly clear any prior value_handle so a row
		// previously stored as a spill correctly downgrades to inline
		// when the new value fits.
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
		return fmt.Errorf("node_attributes.Upsert: %w", err)
	}

	// Queue the prior handle for orphan reaping when it would otherwise
	// be lost (i.e. the row previously had a value_handle and we are
	// either replacing it with a different handle or downgrading to
	// inline). Same-handle upserts are a no-op (PK conflict on the
	// orphans row swallows it).
	if priorHandle != "" && priorHandle != newHandle {
		now := time.Now().UTC()
		if err := persistence.QueueBlobOrphan(ctx, si.BlobOrphans(), tx,
			priorHandle, priorBkend, now, si.blobRetention); err != nil {
			return fmt.Errorf("node_attributes.Upsert: queue prior orphan: %w", err)
		}
	}
	return nil
}

// MergeDelta performs a shallow JSONB merge (`data || $delta::jsonb`).
// Requires the row to exist on a non-nil-delta call; returns an error
// wrapping ErrNoRows when absent.
//
// nil-delta is a no-op merge: bumps updated_at if the row exists,
// silently no-ops if it doesn't.
//
// Spill semantics: when the existing row is spilled (value_handle is
// non-NULL) and an active BlobBackend matches the recorded backend, we
// materialize the prior bytes, merge in Go, and re-write through Upsert
// (which re-applies the spill decision based on the merged size). When
// the row is inline today, the SQL `||` JSONB merge runs as before; if
// the post-merge bytes exceed the threshold, an upcoming Get + Upsert
// cycle elsewhere in the pipeline will spill them. (We do not
// re-marshal here on the inline path to keep MergeDelta cheap; the
// merged-then-spill path is correct because Upsert is the canonical
// spill-decision site.)
func (s *nodeAttributesImpl) MergeDelta(ctx context.Context, runID shared.UUID, delta map[string]any, tx persistence.Tx) error {
	if delta == nil {
		_, err := s.q(tx).Exec(ctx,
			`UPDATE rimsky_node_attributes
			    SET updated_at = now()
			  WHERE node_run_id = $1`,
			runID,
		)
		if err != nil {
			return fmt.Errorf("node_attributes.MergeDelta: touch: %w", err)
		}
		return nil
	}

	si := (*tablesImpl)(s)

	// Spilled-row path: materialize, merge in Go, re-Upsert (which
	// re-applies spill, queues orphans).
	priorHandle, _, err := readPriorBlobHandle(ctx, si.q(tx), runID)
	if err != nil {
		return fmt.Errorf("node_attributes.MergeDelta: read prior handle: %w", err)
	}
	if priorHandle != "" {
		// Read existing data (either from backend or inline).
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

	// Inline path: SQL-level shallow merge.
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

// readPriorBlobHandle returns the value_handle / value_handle_backend
// for runID (or empty strings when the row does not exist or has no
// handle). Errors only on actual query failure — absence is signaled
// by both return strings being empty.
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
