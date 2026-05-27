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

	"github.com/rimsky-ai/rimsky-core/foundation/persistence"
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
	now := time.Now().UTC()
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
		out    persistence.NodeEvent
		inline []byte
		hRaw   sql.NullString
		hbRaw  sql.NullString
		when   time.Time
		fid    sql.NullString
	)
	err := row.Scan(
		&out.ID, &out.InstanceID, &out.EmitterNodeID, &out.EventName,
		&inline, &hRaw, &hbRaw, &when, &fid,
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
	out.EmittedAt = when
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
