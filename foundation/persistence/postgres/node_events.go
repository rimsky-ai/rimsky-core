// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// node_events.go is the postgres accessor for the rimsky_node_events
// ledger (migration C6). Append-only; LatestByName lookup and
// per-instance bulk delete (returning orphan handles for blob cleanup).
package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/fallguy/rimsky/foundation/persistence"
)

// NodeEvents returns the postgres NodeEventsStore impl.
func (s *storeImpl) NodeEvents() persistence.NodeEventsStore {
	return (*nodeEventsImpl)(s)
}

type nodeEventsImpl storeImpl

func (b *nodeEventsImpl) q(tx persistence.Tx) querier { return (*storeImpl)(b).q(tx) }

var _ persistence.NodeEventsStore = (*nodeEventsImpl)(nil)

// Insert appends a row and returns its auto-generated id.
//
// FrameID may be empty; when empty the column is left NULL (the cast
// `NULLIF($_, '')::uuid` keeps the SQL simple and avoids two code paths).
func (b *nodeEventsImpl) Insert(ctx context.Context, evt persistence.NodeEvent, tx persistence.Tx) (int64, error) {
	var id int64
	err := b.q(tx).QueryRow(ctx,
		`INSERT INTO rimsky_node_events
		   (instance_id, emitter_node_id, event_name,
		    payload_inline, payload_handle, payload_handle_backend,
		    emitted_at, frame_id)
		 VALUES ($1::uuid, $2, $3, $4, $5, $6, NOW(), NULLIF($7, '')::uuid)
		 RETURNING id`,
		evt.InstanceID, evt.EmitterNodeID, evt.EventName,
		nilIfEmpty(evt.PayloadInline),
		nilIfEmptyStr(evt.PayloadHandle),
		nilIfEmptyStr(evt.PayloadHandleBackend),
		evt.FrameID,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("node_events.Insert: %w", err)
	}
	return id, nil
}

// LatestByName returns the most recent emission for (instance, emitter,
// event_name). Returns (nil, nil) when no row exists.
func (b *nodeEventsImpl) LatestByName(ctx context.Context, instanceID, emitterNodeID, eventName string, tx persistence.Tx) (*persistence.NodeEvent, error) {
	var (
		row    persistence.NodeEvent
		inline []byte
		hRaw   sql.NullString
		hbRaw  sql.NullString
		fid    sql.NullString
	)
	err := b.q(tx).QueryRow(ctx,
		`SELECT id, instance_id, emitter_node_id, event_name,
		        payload_inline, payload_handle, payload_handle_backend,
		        emitted_at, frame_id
		   FROM rimsky_node_events
		  WHERE instance_id = $1::uuid
		    AND emitter_node_id = $2
		    AND event_name = $3
		  ORDER BY emitted_at DESC
		  LIMIT 1`,
		instanceID, emitterNodeID, eventName,
	).Scan(
		&row.ID, &row.InstanceID, &row.EmitterNodeID, &row.EventName,
		&inline, &hRaw, &hbRaw, &row.EmittedAt, &fid,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("node_events.LatestByName: %w", err)
	}
	row.PayloadInline = inline
	if hRaw.Valid {
		row.PayloadHandle = hRaw.String
	}
	if hbRaw.Valid {
		row.PayloadHandleBackend = hbRaw.String
	}
	if fid.Valid {
		row.FrameID = fid.String
	}
	return &row, nil
}

// DeleteByInstance bulk-deletes every row for instanceID and returns
// the (handle, backend) pairs whose payload spilled to a backend so the
// caller can queue them for blob orphan reaping.
func (b *nodeEventsImpl) DeleteByInstance(ctx context.Context, instanceID string, tx persistence.Tx) (int64, []persistence.NodeEventOrphan, error) {
	rows, err := b.q(tx).Query(ctx,
		`DELETE FROM rimsky_node_events
		  WHERE instance_id = $1::uuid
		 RETURNING payload_handle, payload_handle_backend`,
		instanceID,
	)
	if err != nil {
		return 0, nil, fmt.Errorf("node_events.DeleteByInstance: %w", err)
	}
	defer rows.Close()
	var deleted int64
	var orphans []persistence.NodeEventOrphan
	for rows.Next() {
		var h, hb sql.NullString
		if err := rows.Scan(&h, &hb); err != nil {
			return 0, nil, fmt.Errorf("node_events.DeleteByInstance: scan: %w", err)
		}
		deleted++
		if h.Valid && h.String != "" && hb.Valid && hb.String != "" {
			orphans = append(orphans, persistence.NodeEventOrphan{
				Handle: h.String, Backend: hb.String,
			})
		}
	}
	if err := rows.Err(); err != nil {
		return 0, nil, fmt.Errorf("node_events.DeleteByInstance: rows.Err: %w", err)
	}
	return deleted, orphans, nil
}

// nilIfEmpty turns a zero-length byte slice into nil so the postgres
// driver writes NULL instead of an empty bytea.
func nilIfEmpty(b []byte) []byte {
	if len(b) == 0 {
		return nil
	}
	return b
}

// nilIfEmptyStr turns an empty string into nil so the postgres driver
// writes NULL.
func nilIfEmptyStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
