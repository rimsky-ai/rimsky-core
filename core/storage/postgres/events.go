// EventStore — port of rimsky/src/storage/postgres/event-store.ts.
//
// The append-only event log. Tail/list use a (occurred_at, id) cursor
// encoded as base64 JSON so clients can't naively trust the value but do
// get stable pagination under concurrent writes.
package postgres

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/storage"
)

type EventStore struct {
	pool *pgxpool.Pool
}

var _ storage.EventStore = (*EventStore)(nil)

// Append inserts an event row. If Payload is nil we insert {} so the column's
// NOT NULL constraint stays satisfied. If OccurredAt is nil the DB default
// (NOW()) is used.
func (s *EventStore) Append(ctx context.Context, in storage.EventAppendInput, tx storage.Tx) error {
	ex := q(tx, s.pool)
	payload := in.Payload
	if payload == nil {
		payload = map[string]any{}
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("events.append: marshal payload: %w", err)
	}
	var occurredAt any
	if in.OccurredAt != nil {
		occurredAt = *in.OccurredAt
	}
	_, err = ex.Exec(ctx,
		`INSERT INTO rimsky_events (instance_id, node_id, kind, payload, occurred_at)
		 VALUES ($1, $2, $3, $4, COALESCE($5, NOW()))`,
		instanceIDArg(in.InstanceID), nodeIDArg(in.NodeID),
		in.Kind, payloadBytes, occurredAt,
	)
	return err
}

// List returns events matching filter, ordered by (occurred_at ASC, id ASC).
// Cursor is an opaque base64-JSON encoding of (occurred_at, id).
func (s *EventStore) List(ctx context.Context, filter storage.EventListFilter, pag storage.ListPagination, tx storage.Tx) (storage.EventListResult, error) {
	ex := q(tx, s.pool)
	limit := pag.Limit
	if limit <= 0 {
		limit = 100
	}
	var cursorOccurred *time.Time
	var cursorID *int64
	if pag.Cursor != "" {
		oc, id, err := decodeEventCursor(pag.Cursor)
		if err != nil {
			return storage.EventListResult{}, fmt.Errorf("events.list: bad cursor: %w", err)
		}
		cursorOccurred = &oc
		cursorID = &id
	}

	rows, err := ex.Query(ctx,
		`SELECT id, instance_id, node_id, kind, payload, occurred_at
		 FROM rimsky_events
		 WHERE ($1::uuid IS NULL OR node_id = $1)
		   AND ($2::uuid IS NULL OR instance_id = $2)
		   AND ($3::text IS NULL OR kind = $3)
		   AND ($4::timestamptz IS NULL OR occurred_at >= $4)
		   AND ($5::timestamptz IS NULL OR occurred_at <= $5)
		   AND ($6::timestamptz IS NULL OR (occurred_at, id) > ($6, $7))
		 ORDER BY occurred_at ASC, id ASC
		 LIMIT $8`,
		nodeIDArg(filter.NodeID), instanceIDArg(filter.InstanceID),
		nullableString(filter.Kind),
		nullableTime(filter.Since), nullableTime(filter.Until),
		nullableTime(cursorOccurred), nullableInt64(cursorID),
		limit,
	)
	if err != nil {
		return storage.EventListResult{}, err
	}
	defer rows.Close()

	var out []storage.EventRow
	for rows.Next() {
		var (
			r           storage.EventRow
			instanceID  *shared.UUID
			nodeID      *shared.UUID
			payload     []byte
			occurredAt  time.Time
			eventID     int64
		)
		if err := rows.Scan(&eventID, &instanceID, &nodeID, &r.Kind, &payload, &occurredAt); err != nil {
			return storage.EventListResult{}, err
		}
		r.ID = eventID
		r.InstanceID = instanceID
		r.NodeID = nodeID
		r.OccurredAt = occurredAt
		if len(payload) > 0 {
			m := map[string]any{}
			if err := json.Unmarshal(payload, &m); err != nil {
				return storage.EventListResult{}, fmt.Errorf("events.list: unmarshal payload: %w", err)
			}
			r.Payload = m
		} else {
			r.Payload = map[string]any{}
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return storage.EventListResult{}, err
	}
	var nextCursor string
	if len(out) == limit && len(out) > 0 {
		last := out[len(out)-1]
		nextCursor = encodeEventCursor(last.OccurredAt, last.ID)
	}
	return storage.EventListResult{Events: out, NextCursor: nextCursor}, nil
}

// Tail is equivalent to List with no filters, just a cursor + limit.
func (s *EventStore) Tail(ctx context.Context, cursor string, limit int, tx storage.Tx) (storage.EventListResult, error) {
	return s.List(ctx, storage.EventListFilter{}, storage.ListPagination{Limit: limit, Cursor: cursor}, tx)
}

// ---- cursor encoding ----

type eventCursor struct {
	O time.Time `json:"o"`
	I int64     `json:"i"`
}

func encodeEventCursor(occurred time.Time, id int64) string {
	c := eventCursor{O: occurred, I: id}
	b, _ := json.Marshal(c)
	return base64.StdEncoding.EncodeToString(b)
}

func decodeEventCursor(s string) (time.Time, int64, error) {
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return time.Time{}, 0, err
	}
	var c eventCursor
	if err := json.Unmarshal(raw, &c); err != nil {
		return time.Time{}, 0, err
	}
	return c.O, c.I, nil
}

// ---- argument helpers ----

func instanceIDArg(p *shared.UUID) any {
	if p == nil {
		return nil
	}
	return *p
}
func nodeIDArg(p *shared.UUID) any {
	if p == nil {
		return nil
	}
	return *p
}
func nullableTime(p *time.Time) any {
	if p == nil {
		return nil
	}
	return *p
}
func nullableInt64(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}
