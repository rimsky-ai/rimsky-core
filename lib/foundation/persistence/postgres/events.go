// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// EventTable — port of rimsky/src/storage/postgres/event-store.ts.
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
	"log/slog"
	"strconv"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/events"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

// Append inserts an event row. If Payload is nil we insert {} so the column's
// NOT NULL constraint stays satisfied. If OccurredAt is nil the DB default
// (NOW()) is used.
//
// The typed Kind is marshaled to its canonical wire form via
// Kind.String() at the persistence boundary (per
// decision:event-log-kind-enum). A zero / unrecognized typed value
// stringifies to "" and we refuse the write — silently inserting an
// empty kind would create observability blind spots indistinguishable
// from a missing row to consumers filtering by kind. Nil-tx
// enforcement happens first via s.q(tx) (preserved by the assignment
// order).
func (s *eventsImpl) Append(ctx context.Context, in persistence.EventAppendInput, tx persistence.Tx) error {
	ex := s.q(tx)
	kindWire := in.Kind.String()
	if kindWire == "" {
		return fmt.Errorf("events.append: empty kind (zero events.Kind value)")
	}
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
		kindWire, payloadBytes, occurredAt,
	)
	return err
}

// List returns events matching filter, ordered by (occurred_at DESC, id DESC)
// per spec §1.2.5. Cursor is an opaque base64-JSON encoding of (occurred_at, id).
func (s *eventsImpl) List(ctx context.Context, filter persistence.EventListFilter, pag persistence.ListPagination, tx persistence.Tx) (persistence.EventListResult, error) {
	ex := s.q(tx)
	limit := pag.Limit
	if limit <= 0 {
		limit = 100
	}
	var cursorOccurred *time.Time
	var cursorID *int64
	if pag.Cursor != "" {
		oc, id, err := decodeEventCursor(pag.Cursor)
		if err != nil {
			return persistence.EventListResult{}, fmt.Errorf("events.list: bad cursor: %w", err)
		}
		cursorOccurred = &oc
		cursorID = &id
	}

	var kindInArg any
	if len(filter.KindIn) > 0 {
		kindInArg = filter.KindIn
	}
	// Auth-payload filters. Each is a NULL-tolerant predicate ($N IS NULL
	// → no-op) so a nil pointer never excludes a row. response_status is
	// stored as a JSON number; payload->>'response_status' renders it as
	// text, so we compare against the int cast to text.
	var respStatusArg any
	if filter.ResponseStatus != nil {
		respStatusArg = strconv.Itoa(*filter.ResponseStatus)
	}
	rows, err := ex.Query(ctx,
		`SELECT id, instance_id, node_id, kind, payload, occurred_at
		 FROM rimsky_events
		 WHERE ($1::uuid IS NULL OR node_id = $1)
		   AND ($2::uuid IS NULL OR instance_id = $2)
		   AND ($3::text IS NULL OR kind = $3)
		   AND ($4::text[] IS NULL OR kind = ANY($4::text[]))
		   AND ($5::timestamptz IS NULL OR occurred_at >= $5)
		   AND ($6::timestamptz IS NULL OR occurred_at <= $6)
		   AND ($7::timestamptz IS NULL OR (occurred_at, id) < ($7, $8))
		   AND ($10::text IS NULL OR payload->>'key_id' = $10)
		   AND ($11::text IS NULL OR payload->>'key_name' = $11)
		   AND ($12::text IS NULL OR payload->>'action' = $12)
		   AND ($13::text IS NULL OR payload->>'action' LIKE $13 || '%')
		   AND ($14::text IS NULL OR payload->>'response_status' = $14)
		   AND ($15::text IS NULL OR payload->>'mode' = $15)
		   AND ($16::text IS NULL OR payload->>'request_path' = $16)
		 ORDER BY occurred_at DESC, id DESC
		 LIMIT $9`,
		nodeIDArg(filter.NodeID), instanceIDArg(filter.InstanceID),
		nullableString(filter.Kind), kindInArg,
		nullableTime(filter.Since), nullableTime(filter.Until),
		nullableTime(cursorOccurred), nullableInt64(cursorID),
		limit,
		nullableStringPtr(filter.KeyID), nullableStringPtr(filter.KeyName),
		nullableStringPtr(filter.ActionExact), nullableStringPtr(filter.ActionPrefix),
		respStatusArg, nullableStringPtr(filter.Mode),
		nullableStringPtr(filter.RequestPath),
	)
	if err != nil {
		return persistence.EventListResult{}, err
	}
	defer rows.Close()

	var out []persistence.EventRow
	for rows.Next() {
		var (
			r          persistence.EventRow
			instanceID *shared.UUID
			nodeID     *shared.UUID
			kindRaw    string
			payload    []byte
			occurredAt time.Time
			eventID    int64
		)
		if err := rows.Scan(&eventID, &instanceID, &nodeID, &kindRaw, &payload, &occurredAt); err != nil {
			return persistence.EventListResult{}, err
		}
		// Defensive parse at the unmarshal boundary (per
		// decision:event-log-kind-enum). An unknown string is a
		// real error — surface it, don't synthesize a Kind. The
		// raw value lands in the logger so an operator can find
		// the offending row.
		k, err := events.ParseKindString(kindRaw)
		if err != nil {
			slog.Error("events.unknown_kind_at_unmarshal", slog.String("raw", kindRaw))
			return persistence.EventListResult{}, fmt.Errorf("events.list: %w", err)
		}
		r.ID = eventID
		r.InstanceID = instanceID
		r.NodeID = nodeID
		r.Kind = k
		r.KindRaw = kindRaw
		r.OccurredAt = occurredAt
		if len(payload) > 0 {
			m := map[string]any{}
			if err := json.Unmarshal(payload, &m); err != nil {
				return persistence.EventListResult{}, fmt.Errorf("events.list: unmarshal payload: %w", err)
			}
			r.Payload = m
		} else {
			r.Payload = map[string]any{}
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return persistence.EventListResult{}, err
	}
	var nextCursor string
	if len(out) == limit && len(out) > 0 {
		last := out[len(out)-1]
		nextCursor = encodeEventCursor(last.OccurredAt, last.ID)
	}
	return persistence.EventListResult{Events: out, NextCursor: nextCursor}, nil
}

// LastTerminalByNodes returns the most-recent dispatch-terminal event
// (kind in {work_completed, error}) per node id. The DISTINCT ON
// projection picks the latest row per node in a single SELECT,
// avoiding the per-node N+1 the cascade-graph builder previously did.
func (s *eventsImpl) LastTerminalByNodes(ctx context.Context, nodeIDs []shared.UUID, tx persistence.Tx) (map[shared.UUID]persistence.EventRow, error) {
	out := make(map[shared.UUID]persistence.EventRow, len(nodeIDs))
	if len(nodeIDs) == 0 {
		return out, nil
	}
	ex := s.q(tx)
	rows, err := ex.Query(ctx,
		`SELECT DISTINCT ON (node_id)
		        id, instance_id, node_id, kind, payload, occurred_at
		   FROM rimsky_events
		  WHERE node_id = ANY($1)
		    AND kind IN ('work_completed', 'error')
		  ORDER BY node_id, occurred_at DESC, id DESC`,
		nodeIDs,
	)
	if err != nil {
		return nil, fmt.Errorf("events.lastTerminalByNodes: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			r          persistence.EventRow
			instanceID *shared.UUID
			nodeID     *shared.UUID
			kindRaw    string
			payload    []byte
			occurredAt time.Time
			eventID    int64
		)
		if err := rows.Scan(&eventID, &instanceID, &nodeID, &kindRaw, &payload, &occurredAt); err != nil {
			return nil, err
		}
		k, err := events.ParseKindString(kindRaw)
		if err != nil {
			slog.Error("events.unknown_kind_at_unmarshal", slog.String("raw", kindRaw))
			return nil, fmt.Errorf("events.lastTerminalByNodes: %w", err)
		}
		r.ID = eventID
		r.InstanceID = instanceID
		r.NodeID = nodeID
		r.Kind = k
		r.KindRaw = kindRaw
		r.OccurredAt = occurredAt
		if len(payload) > 0 {
			m := map[string]any{}
			if err := json.Unmarshal(payload, &m); err == nil {
				r.Payload = m
			} else {
				r.Payload = map[string]any{}
			}
		} else {
			r.Payload = map[string]any{}
		}
		if nodeID != nil {
			out[*nodeID] = r
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// DeleteOlderThan deletes rimsky_events rows whose occurred_at is before
// cutoff. The audit log is time-keyed (no frame FK), so the trailing
// trace-retention window alone bounds it. Standalone sweep — no
// caller-supplied tx; run directly against the pool (mirroring
// Lineage.DeleteOlderThan) so the scheduler tick can call it without a
// surrounding Tables.Transaction.
func (s *eventsImpl) DeleteOlderThan(ctx context.Context, cutoff time.Time) (int, error) {
	tag, err := (*tablesImpl)(s).pool.Exec(ctx,
		`DELETE FROM rimsky_events WHERE occurred_at < $1`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("postgres.Events.DeleteOlderThan: %w", err)
	}
	return int(tag.RowsAffected()), nil
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

// nullableStringPtr maps a *string filter field to a query arg: nil →
// SQL NULL (the predicate short-circuits to a no-op), non-nil → the
// dereferenced value. Distinct from nullableString (which maps an empty
// string to NULL); here an empty-but-non-nil filter is a real "= ”"
// match, so we never collapse it.
func nullableStringPtr(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}
