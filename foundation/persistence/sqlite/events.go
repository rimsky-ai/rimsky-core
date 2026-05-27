// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// events.go — SQLite-backed persistence.EventTable.
package sqlite

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/foundation/shared"
)

func (s *eventsImpl) Append(ctx context.Context, in persistence.EventAppendInput, tx persistence.Tx) error {
	payload := in.Payload
	if payload == nil {
		payload = map[string]any{}
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("events.append: marshal payload: %w", err)
	}
	occurredAt := nowUTC()
	if in.OccurredAt != nil {
		occurredAt = formatTime(*in.OccurredAt)
	}
	_, err = s.q(tx).ExecContext(ctx,
		`INSERT INTO rimsky_events (instance_id, node_id, kind, payload, occurred_at)
		 VALUES (?, ?, ?, ?, ?)`,
		instanceIDArg(in.InstanceID), nodeIDArg(in.NodeID),
		in.Kind, string(payloadBytes), occurredAt,
	)
	return err
}

func (s *eventsImpl) List(ctx context.Context, filter persistence.EventListFilter, pag persistence.ListPagination, tx persistence.Tx) (persistence.EventListResult, error) {
	limit := pag.Limit
	if limit <= 0 {
		limit = 100
	}
	var cursorOccurred any
	var cursorID any
	if pag.Cursor != "" {
		oc, id, err := decodeEventCursor(pag.Cursor)
		if err != nil {
			return persistence.EventListResult{}, fmt.Errorf("events.list: bad cursor: %w", err)
		}
		cursorOccurred = formatTime(oc)
		cursorID = id
	}

	var nodeArg, instArg, kindArg, sinceArg, untilArg any
	if filter.NodeID != nil {
		nodeArg = filter.NodeID.String()
	}
	if filter.InstanceID != nil {
		instArg = filter.InstanceID.String()
	}
	if filter.Kind != "" {
		kindArg = filter.Kind
	}
	if filter.Since != nil {
		sinceArg = formatTime(*filter.Since)
	}
	if filter.Until != nil {
		untilArg = formatTime(*filter.Until)
	}

	// Build a kind_in IN (...) clause dynamically because sqlite has no
	// native array bind. Skipped when filter.KindIn is empty.
	kindInClause := ""
	args := []any{
		nodeArg, nodeArg, instArg, instArg, kindArg, kindArg,
		sinceArg, sinceArg, untilArg, untilArg,
		cursorOccurred, cursorOccurred, cursorID,
	}
	if len(filter.KindIn) > 0 {
		placeholders := ""
		for i, k := range filter.KindIn {
			if i > 0 {
				placeholders += ","
			}
			placeholders += "?"
			args = append(args, k)
		}
		kindInClause = " AND kind IN (" + placeholders + ")"
	}
	args = append(args, limit)

	rows, err := s.q(tx).QueryContext(ctx,
		`SELECT id, instance_id, node_id, kind, payload, occurred_at
		 FROM rimsky_events
		 WHERE (? IS NULL OR node_id = ?)
		   AND (? IS NULL OR instance_id = ?)
		   AND (? IS NULL OR kind = ?)
		   AND (? IS NULL OR occurred_at >= ?)
		   AND (? IS NULL OR occurred_at <= ?)
		   AND (? IS NULL OR (occurred_at, id) < (?, ?))`+kindInClause+
			` ORDER BY occurred_at DESC, id DESC
		 LIMIT ?`,
		args...,
	)
	if err != nil {
		return persistence.EventListResult{}, err
	}
	defer rows.Close()

	var out []persistence.EventRow
	for rows.Next() {
		var (
			r             persistence.EventRow
			instanceIDStr sql.NullString
			nodeIDStr     sql.NullString
			payloadStr    string
			occurredAtStr string
			eventID       int64
		)
		if err := rows.Scan(&eventID, &instanceIDStr, &nodeIDStr, &r.Kind, &payloadStr, &occurredAtStr); err != nil {
			return persistence.EventListResult{}, err
		}
		r.ID = eventID
		if instanceIDStr.Valid && instanceIDStr.String != "" {
			u, err := uuid.Parse(instanceIDStr.String)
			if err != nil {
				return persistence.EventListResult{}, err
			}
			r.InstanceID = &u
		}
		if nodeIDStr.Valid && nodeIDStr.String != "" {
			u, err := uuid.Parse(nodeIDStr.String)
			if err != nil {
				return persistence.EventListResult{}, err
			}
			r.NodeID = &u
		}
		occurredAt, err := parseTime(occurredAtStr)
		if err != nil {
			return persistence.EventListResult{}, err
		}
		r.OccurredAt = occurredAt
		if payloadStr != "" {
			m := map[string]any{}
			if err := json.Unmarshal([]byte(payloadStr), &m); err != nil {
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
// per node id. SQLite has no DISTINCT ON, so the implementation is a
// correlated-subquery filter (sufficient for the cascade-graph use
// case where node lists are bounded by template size).
func (s *eventsImpl) LastTerminalByNodes(ctx context.Context, nodeIDs []shared.UUID, tx persistence.Tx) (map[shared.UUID]persistence.EventRow, error) {
	out := make(map[shared.UUID]persistence.EventRow, len(nodeIDs))
	if len(nodeIDs) == 0 {
		return out, nil
	}
	placeholders := ""
	args := make([]any, 0, len(nodeIDs))
	for i, id := range nodeIDs {
		if i > 0 {
			placeholders += ","
		}
		placeholders += "?"
		args = append(args, id.String())
	}
	q := `SELECT e.id, e.instance_id, e.node_id, e.kind, e.payload, e.occurred_at
		FROM rimsky_events e
		WHERE e.node_id IN (` + placeholders + `)
		  AND e.kind IN ('work_completed', 'error')
		  AND e.id = (
		    SELECT e2.id FROM rimsky_events e2
		    WHERE e2.node_id = e.node_id
		      AND e2.kind IN ('work_completed', 'error')
		    ORDER BY e2.occurred_at DESC, e2.id DESC
		    LIMIT 1
		  )`
	rows, err := s.q(tx).QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("events.lastTerminalByNodes: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			r             persistence.EventRow
			instanceIDStr sql.NullString
			nodeIDStr     sql.NullString
			payloadStr    string
			occurredAtStr string
			eventID       int64
		)
		if err := rows.Scan(&eventID, &instanceIDStr, &nodeIDStr, &r.Kind, &payloadStr, &occurredAtStr); err != nil {
			return nil, err
		}
		r.ID = eventID
		if instanceIDStr.Valid && instanceIDStr.String != "" {
			u, err := uuid.Parse(instanceIDStr.String)
			if err != nil {
				return nil, err
			}
			r.InstanceID = &u
		}
		var nid uuid.UUID
		if nodeIDStr.Valid && nodeIDStr.String != "" {
			u, err := uuid.Parse(nodeIDStr.String)
			if err != nil {
				return nil, err
			}
			r.NodeID = &u
			nid = u
		}
		occurredAt, err := parseTime(occurredAtStr)
		if err != nil {
			return nil, err
		}
		r.OccurredAt = occurredAt
		if payloadStr != "" {
			m := map[string]any{}
			if err := json.Unmarshal([]byte(payloadStr), &m); err == nil {
				r.Payload = m
			} else {
				r.Payload = map[string]any{}
			}
		} else {
			r.Payload = map[string]any{}
		}
		if nid != (uuid.UUID{}) {
			out[nid] = r
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
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
