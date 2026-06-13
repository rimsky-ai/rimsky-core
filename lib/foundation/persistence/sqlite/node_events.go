// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// node_events.go is the sqlite accessor for the rimsky_node_events
// ledger (migration C6, sqlite variant). Mirrors postgres but uses
// last_insert_rowid() and TEXT (UUID-as-text) columns.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

// NodeEvents returns the sqlite NodeEventTable impl.
func (s *tablesImpl) NodeEvents() persistence.NodeEventTable {
	return (*nodeEventsImpl)(s)
}

type nodeEventsImpl tablesImpl

func (b *nodeEventsImpl) q(tx persistence.Tx) querier { return (*tablesImpl)(b).q(tx) }

var _ persistence.NodeEventTable = (*nodeEventsImpl)(nil)

// Insert appends a row and returns its auto-generated id.
func (b *nodeEventsImpl) Insert(ctx context.Context, evt persistence.NodeEvent, tx persistence.Tx) (int64, error) {
	// emitted_at is stored as fixed-width UTC TEXT (timeLayoutFixedNanos,
	// whose lexicographic order matches chronological order) — the same
	// convention as the audit log's occurred_at and what DeleteOlderThan
	// compares against — so the insert and the time-window reaper share one
	// time format (a raw time.Time bind would store modernc's t.String()
	// layout instead, which orders differently from the fixed-width cutoff
	// the reaper binds).
	now := formatTime(time.Now().UTC())
	res, err := b.q(tx).ExecContext(ctx,
		`INSERT INTO rimsky_node_events
		   (instance_id, emitter_node_id, event_name,
		    payload_inline, payload_handle, payload_handle_backend,
		    emitted_at, frame_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		evt.InstanceID, evt.EmitterNodeID, evt.EventName,
		nilIfEmpty(evt.PayloadInline),
		nilIfEmptyStr(evt.PayloadHandle),
		nilIfEmptyStr(evt.PayloadHandleBackend),
		now,
		nilIfEmptyStr(evt.FrameID),
	)
	if err != nil {
		return 0, fmt.Errorf("node_events.Insert: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("node_events.Insert: last id: %w", err)
	}
	return id, nil
}

// LatestByName returns the most recent emission for (instance, emitter,
// event_name). (nil, nil) when no row exists.
func (b *nodeEventsImpl) LatestByName(ctx context.Context, instanceID, emitterNodeID, eventName string, tx persistence.Tx) (*persistence.NodeEvent, error) {
	row := b.q(tx).QueryRowContext(ctx,
		`SELECT id, instance_id, emitter_node_id, event_name,
		        payload_inline, payload_handle, payload_handle_backend,
		        emitted_at, frame_id
		   FROM rimsky_node_events
		  WHERE instance_id = ?
		    AND emitter_node_id = ?
		    AND event_name = ?
		  ORDER BY emitted_at DESC
		  LIMIT 1`,
		instanceID, emitterNodeID, eventName,
	)
	var (
		out     persistence.NodeEvent
		inline  []byte
		hRaw    sql.NullString
		hbRaw   sql.NullString
		whenStr string
		fid     sql.NullString
	)
	err := row.Scan(
		&out.ID, &out.InstanceID, &out.EmitterNodeID, &out.EventName,
		&inline, &hRaw, &hbRaw, &whenStr, &fid,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("node_events.LatestByName: %w", err)
	}
	out.PayloadInline = inline
	if hRaw.Valid {
		out.PayloadHandle = hRaw.String
	}
	if hbRaw.Valid {
		out.PayloadHandleBackend = hbRaw.String
	}
	if fid.Valid {
		out.FrameID = fid.String
	}
	// emitted_at is fixed-width UTC TEXT (timeLayoutFixedNanos; same
	// convention as the audit log's occurred_at); parse it back the way
	// events.List does rather than scanning straight into a time.Time.
	emittedAt, err := parseTime(whenStr)
	if err != nil {
		return nil, fmt.Errorf("node_events.LatestByName: parse emitted_at: %w", err)
	}
	out.EmittedAt = emittedAt
	return &out, nil
}

// DeleteByInstance bulk-deletes; collects orphan handles in a separate
// SELECT before the DELETE because sqlite does not support
// DELETE … RETURNING on every modernc.org/sqlite version cleanly.
func (b *nodeEventsImpl) DeleteByInstance(ctx context.Context, instanceID string, tx persistence.Tx) (int64, []persistence.NodeEventOrphan, error) {
	rows, err := b.q(tx).QueryContext(ctx,
		`SELECT payload_handle, payload_handle_backend
		   FROM rimsky_node_events
		  WHERE instance_id = ?
		    AND payload_handle IS NOT NULL`,
		instanceID,
	)
	if err != nil {
		return 0, nil, fmt.Errorf("node_events.DeleteByInstance: select orphans: %w", err)
	}
	var orphans []persistence.NodeEventOrphan
	for rows.Next() {
		var h, hb sql.NullString
		if err := rows.Scan(&h, &hb); err != nil {
			_ = rows.Close()
			return 0, nil, fmt.Errorf("node_events.DeleteByInstance: scan: %w", err)
		}
		if h.Valid && h.String != "" && hb.Valid && hb.String != "" {
			orphans = append(orphans, persistence.NodeEventOrphan{
				Handle: h.String, Backend: hb.String,
			})
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, nil, fmt.Errorf("node_events.DeleteByInstance: rows.Err: %w", err)
	}
	_ = rows.Close()
	res, err := b.q(tx).ExecContext(ctx,
		`DELETE FROM rimsky_node_events WHERE instance_id = ?`,
		instanceID,
	)
	if err != nil {
		return 0, nil, fmt.Errorf("node_events.DeleteByInstance: %w", err)
	}
	deleted, err := res.RowsAffected()
	if err != nil {
		return 0, nil, fmt.Errorf("node_events.DeleteByInstance: rows-affected: %w", err)
	}
	return deleted, orphans, nil
}

// DeleteOlderThan deletes rimsky_node_events rows whose emitted_at is
// before cutoff. The named-event ledger is time-keyed (its frame_id is a
// non-FK column), so the trailing trace-retention window alone bounds it.
// For a durable instance this is the ONLY reclamation path for those bytes:
// the instance never terminates, so the instance-delete cascade never runs.
//
// Spilled-payload handles are queued into rimsky_blob_orphans in the SAME
// transaction as the DELETE so the queue-and-delete is atomic: a crash
// after the queue but before the delete leaves the rows for the next tick to
// re-find (Insert is idempotent on the handle PK), and the rows are never
// deleted without their blob handle durably queued. Queueing BEFORE the
// delete (not after) is what makes that hold — a post-delete queue that
// failed would orphan the bytes with no row left to re-discover. The
// returned orphan slice is for the caller's observability only; the handles
// are already persisted when this returns.
//
// Collects orphan handles in a SELECT before the DELETE (mirroring
// DeleteByInstance — modernc.org/sqlite does not support DELETE … RETURNING
// cleanly on every version). Standalone sweep — no caller-supplied tx; the
// method opens its own transaction so the scheduler tick can call it without
// a surrounding Tables.Transaction.
func (b *nodeEventsImpl) DeleteOlderThan(ctx context.Context, cutoff time.Time) (int, []persistence.NodeEventOrphan, error) {
	stx, err := (*tablesImpl)(b).db.BeginTx(ctx, nil)
	if err != nil {
		return 0, nil, fmt.Errorf("sqlite.NodeEvents.DeleteOlderThan: begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = stx.Rollback()
		}
	}()

	rows, err := stx.QueryContext(ctx,
		`SELECT payload_handle, payload_handle_backend
		   FROM rimsky_node_events
		  WHERE emitted_at < ?
		    AND payload_handle IS NOT NULL`, formatTime(cutoff))
	if err != nil {
		return 0, nil, fmt.Errorf("sqlite.NodeEvents.DeleteOlderThan: select orphans: %w", err)
	}
	var orphans []persistence.NodeEventOrphan
	for rows.Next() {
		var h, hb sql.NullString
		if err := rows.Scan(&h, &hb); err != nil {
			_ = rows.Close()
			return 0, nil, fmt.Errorf("sqlite.NodeEvents.DeleteOlderThan: scan: %w", err)
		}
		if h.Valid && h.String != "" && hb.Valid && hb.String != "" {
			orphans = append(orphans, persistence.NodeEventOrphan{Handle: h.String, Backend: hb.String})
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, nil, fmt.Errorf("sqlite.NodeEvents.DeleteOlderThan: rows.Err: %w", err)
	}
	_ = rows.Close()

	// Queue spilled handles before the DELETE, inside the same tx.
	pTx := &sqliteTx{tx: stx}
	for _, o := range orphans {
		if qerr := persistence.QueueBlobOrphan(
			ctx, (*tablesImpl)(b).BlobOrphans(), pTx, o.Handle, o.Backend,
			time.Now().UTC(), (*tablesImpl)(b).BlobRetention(),
		); qerr != nil {
			return 0, nil, fmt.Errorf("sqlite.NodeEvents.DeleteOlderThan: queue orphan: %w", qerr)
		}
	}

	res, err := stx.ExecContext(ctx,
		`DELETE FROM rimsky_node_events WHERE emitted_at < ?`, formatTime(cutoff))
	if err != nil {
		return 0, nil, fmt.Errorf("sqlite.NodeEvents.DeleteOlderThan: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, nil, fmt.Errorf("sqlite.NodeEvents.DeleteOlderThan: rows-affected: %w", err)
	}
	if err := stx.Commit(); err != nil {
		return 0, nil, fmt.Errorf("sqlite.NodeEvents.DeleteOlderThan: commit: %w", err)
	}
	committed = true
	return int(n), orphans, nil
}

// nilIfEmpty / nilIfEmptyStr — local helpers so callers can pass empty
// strings/slices without thinking about NULL semantics.
func nilIfEmpty(b []byte) []byte {
	if len(b) == 0 {
		return nil
	}
	return b
}

func nilIfEmptyStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
