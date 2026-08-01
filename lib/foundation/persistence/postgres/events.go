// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/events"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

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
	if err != nil {
		return fmt.Errorf("events.append: %w", err)
	}
	return nil
}

func (s *eventsImpl) List(ctx context.Context, filter persistence.EventListFilter, pag persistence.ListPagination, tx persistence.Tx) (persistence.EventListResult, error) {
	ex := s.q(tx)
	limit := pag.Limit
	if limit <= 0 {
		limit = 100
	}
	var cursorOccurred *time.Time
	var cursorID *int64
	if pag.Cursor != "" {
		oc, id, err := persistence.DecodeEventCursor(pag.Cursor)
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
	var respStatusArg any
	if filter.AuditPayload.ResponseStatus != nil {
		respStatusArg = strconv.Itoa(*filter.AuditPayload.ResponseStatus)
	}
	rows, err := ex.Query(ctx,
		`SELECT id, instance_id, node_id, kind, payload, occurred_at
		 FROM rimsky_events
		 WHERE ($1::uuid IS NULL OR node_id = $1)
		   AND ($2::uuid IS NULL OR instance_id = $2)
		   AND ($3::text[] IS NULL OR kind = ANY($3::text[]))
		   AND ($4::timestamptz IS NULL OR occurred_at >= $4)
		   AND ($5::timestamptz IS NULL OR occurred_at <= $5)
		   AND ($6::timestamptz IS NULL OR (occurred_at, id) < ($6, $7))
		   AND ($9::text IS NULL OR payload->>'key_id' = $9)
		   AND ($10::text IS NULL OR payload->>'key_name' = $10)
		   AND ($11::text IS NULL OR payload->>'action' = $11)
		   AND ($12::text IS NULL OR payload->>'action' LIKE $12 || '%')
		   AND ($13::text IS NULL OR payload->>'response_status' = $13)
		   AND ($14::text IS NULL OR payload->>'mode' = $14)
		   AND ($15::text IS NULL OR payload->>'request_path' = $15)
		 ORDER BY occurred_at DESC, id DESC
		 LIMIT $8`,
		nodeIDArg(filter.NodeID), instanceIDArg(filter.InstanceID),
		kindInArg,
		nullableTime(filter.Since), nullableTime(filter.Until),
		nullableTime(cursorOccurred), nullableInt64(cursorID),
		limit,
		nullableStringPtr(filter.AuditPayload.KeyID), nullableStringPtr(filter.AuditPayload.KeyName),
		nullableStringPtr(filter.AuditPayload.ActionExact), nullableStringPtr(filter.AuditPayload.ActionPrefix),
		respStatusArg, nullableStringPtr(filter.AuditPayload.Mode),
		nullableStringPtr(filter.AuditPayload.RequestPath),
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
		// @decision: event-log-kind-enum
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
		nextCursor = persistence.EncodeEventCursor(last.OccurredAt, last.ID)
	}
	return persistence.EventListResult{Events: out, NextCursor: nextCursor}, nil
}

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
		    AND kind = ANY($2::text[])
		  ORDER BY node_id, occurred_at DESC, id DESC`,
		nodeIDs, []string{events.KindWorkCompleted().String(), events.KindError().String()},
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
			if err := json.Unmarshal(payload, &m); err != nil {
				return nil, fmt.Errorf("events.lastTerminalByNodes: unmarshal payload: %w", err)
			}
			r.Payload = m
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

func (s *eventsImpl) DeleteOlderThan(ctx context.Context, cutoff time.Time) (int, error) {
	tag, err := (*tablesImpl)(s).pool.Exec(ctx,
		`DELETE FROM rimsky_events WHERE occurred_at < $1`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("postgres.Events.DeleteOlderThan: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

func (s *eventsImpl) CountAttributeOverrideMatchesByIndex(
	ctx context.Context, instanceID shared.UUID, tx persistence.Tx,
) (map[int64]int64, error) {
	rows, err := s.q(tx).Query(ctx,
		`SELECT (payload->>'override_index')::bigint AS idx, count(*)
		   FROM rimsky_events
		  WHERE instance_id = $1
		    AND kind = $2
		  GROUP BY idx`,
		instanceID, events.KindAttributeOverrideMatched().String())
	if err != nil {
		return nil, fmt.Errorf("postgres.Events.CountAttributeOverrideMatchesByIndex: %w", err)
	}
	defer rows.Close()
	out := map[int64]int64{}
	for rows.Next() {
		var idx, cnt int64
		if err := rows.Scan(&idx, &cnt); err != nil {
			return nil, fmt.Errorf("postgres.Events.CountAttributeOverrideMatchesByIndex: scan: %w", err)
		}
		out[idx] = cnt
	}
	return out, rows.Err()
}

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

func nullableStringPtr(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}
