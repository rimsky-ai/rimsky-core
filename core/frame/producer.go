package frame

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// EnqueueOrCoalesce inserts (serial_queue) or upserts (coalesce) a queued
// frame for the instance. The caller passes a tx so the enqueue can join
// the producer's existing transaction (e.g., the schedule_ticker's tick tx,
// or the controlapi handler's request tx).
//
// Returns the frame_id of the row that received the source — either a
// freshly-created row or an existing pending-coalesce row.
//
// @blessed-invariant 15 (mode mandatory): the helper reads mode from the
// template join and rejects if missing.
func EnqueueOrCoalesce(ctx context.Context, tx pgx.Tx,
	instanceID, sourceNodeID uuid.UUID) (uuid.UUID, error) {

	var (
		mode           string
		frameTimeoutMs int64
	)
	// Template config is stored as JSONB in rimsky_templates.spec.
	// Validation (Task 4) guarantees non-empty frame_resolution; default
	// frame_timeout_ms when missing/zero. COALESCE the mode lookup to
	// empty string so a missing key surfaces as "unsupported mode" rather
	// than a NULL-scan error.
	err := tx.QueryRow(ctx, `
        SELECT COALESCE(t.spec->>'frame_resolution', '') AS mode,
               COALESCE(NULLIF((t.spec->>'frame_timeout_ms'),'')::bigint, 600000) AS frame_timeout_ms
        FROM rimsky_instances i
        JOIN rimsky_templates  t ON t.id = i.template_hash
        WHERE i.id = $1
    `, instanceID).Scan(&mode, &frameTimeoutMs)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, fmt.Errorf("frame.EnqueueOrCoalesce: instance %s not found", instanceID)
		}
		return uuid.Nil, fmt.Errorf("frame.EnqueueOrCoalesce: lookup template: %w", err)
	}
	if frameTimeoutMs <= 0 {
		frameTimeoutMs = 600000
	}

	switch Mode(mode) {
	case ModeSerialQueue:
		return enqueueSerial(ctx, tx, instanceID, sourceNodeID, frameTimeoutMs)
	case ModeCoalesce:
		return enqueueCoalesce(ctx, tx, instanceID, sourceNodeID, frameTimeoutMs)
	default:
		return uuid.Nil, fmt.Errorf("frame.EnqueueOrCoalesce: unsupported mode %q for instance %s",
			mode, instanceID)
	}
}

func enqueueSerial(ctx context.Context, tx pgx.Tx,
	instanceID, sourceNodeID uuid.UUID, timeoutMs int64) (uuid.UUID, error) {

	var frameID uuid.UUID
	err := tx.QueryRow(ctx, `
        INSERT INTO rimsky_frames
            (instance_id, mode, state, source_node_ids, queued_at, frame_timeout_ms)
        VALUES ($1, 'serial_queue', 'queued', ARRAY[$2]::UUID[], now(), $3)
        RETURNING frame_id
    `, instanceID, sourceNodeID, timeoutMs).Scan(&frameID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("frame.EnqueueOrCoalesce(serial_queue): insert: %w", err)
	}
	return frameID, nil
}

func enqueueCoalesce(ctx context.Context, tx pgx.Tx,
	instanceID, sourceNodeID uuid.UUID, timeoutMs int64) (uuid.UUID, error) {

	// Spec §7.3 step 1: one atomic statement keyed on the partial unique index
	// uq_rimsky_frames_coalesce_queued (instance_id) WHERE state='queued'
	// AND mode='coalesce'. Two concurrent producers (each in their own tx)
	// must not deadlock or 5xx — exactly one wins the INSERT, all others
	// fall through DO UPDATE and append source_node_ids. The ON CONFLICT
	// predicate must match the partial index predicate verbatim for
	// PostgreSQL to infer the right index.
	var frameID uuid.UUID
	err := tx.QueryRow(ctx, `
        INSERT INTO rimsky_frames
            (instance_id, mode, state, source_node_ids, queued_at, frame_timeout_ms)
        VALUES ($1, 'coalesce', 'queued', ARRAY[$2]::UUID[], now(), $3)
        ON CONFLICT (instance_id) WHERE state = 'queued' AND mode = 'coalesce'
        DO UPDATE SET source_node_ids = (
            CASE WHEN $2 = ANY(rimsky_frames.source_node_ids) THEN rimsky_frames.source_node_ids
                 ELSE array_append(rimsky_frames.source_node_ids, $2)
            END
        )
        RETURNING frame_id
    `, instanceID, sourceNodeID, timeoutMs).Scan(&frameID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("frame.EnqueueOrCoalesce(coalesce): upsert: %w", err)
	}
	return frameID, nil
}
