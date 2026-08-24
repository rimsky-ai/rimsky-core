// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/events"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/eventpayload"
)

func (s *eventsImpl) Append(ctx context.Context, in persistence.EventAppendInput, tx persistence.Tx) error {
	ex := s.q(tx)
	kindWire := in.Kind.String()
	if kindWire == "" {
		return fmt.Errorf("events.append: empty kind (zero events.Kind value)")
	}
	payloadBytes, err := json.Marshal(in.Payload.Map())
	if err != nil {
		return fmt.Errorf("events.append: marshal payload: %w", err)
	}
	occurredAt := nowUTC()
	if in.OccurredAt != nil {
		occurredAt = formatTime(*in.OccurredAt)
	}
	_, err = ex.ExecContext(ctx,
		`INSERT INTO rimsky_events (instance_id, node_id, kind, payload, occurred_at)
		 VALUES (?, ?, ?, ?, ?)`,
		nullableUUID(in.InstanceID), nullableUUID(in.NodeID),
		kindWire, string(payloadBytes), occurredAt,
	)
	if err != nil {
		return fmt.Errorf("events.append: %w", err)
	}
	return nil
}

func (s *eventsImpl) List(ctx context.Context, filter persistence.EventListFilter, pag persistence.ListPagination, tx persistence.Tx) (persistence.EventListResult, error) {
	limit := pag.Limit
	if limit <= 0 {
		limit = 100
	}
	var cursorOccurred any
	var cursorID any
	if pag.Cursor != "" {
		oc, id, err := persistence.DecodeEventCursor(pag.Cursor)
		if err != nil {
			return persistence.EventListResult{}, persistence.ErrInvalidCursor
		}
		cursorOccurred = formatTime(oc)
		cursorID = id
	}

	var nodeArg, instArg, sinceArg, untilArg any
	if filter.NodeID != nil {
		nodeArg = filter.NodeID.String()
	}
	if filter.InstanceID != nil {
		instArg = filter.InstanceID.String()
	}
	if filter.Since != nil {
		sinceArg = formatTime(*filter.Since)
	}
	if filter.Until != nil {
		untilArg = formatTime(*filter.Until)
	}

	var keyIDArg, keyNameArg, actionExactArg, actionPrefixArg, respStatusArg, modeArg, requestPathArg any
	if filter.AuditPayload.KeyID != nil {
		keyIDArg = *filter.AuditPayload.KeyID
	}
	if filter.AuditPayload.KeyName != nil {
		keyNameArg = *filter.AuditPayload.KeyName
	}
	if filter.AuditPayload.ActionExact != nil {
		actionExactArg = *filter.AuditPayload.ActionExact
	}
	if filter.AuditPayload.ActionPrefix != nil {
		actionPrefixArg = *filter.AuditPayload.ActionPrefix + "%"
	}
	if filter.AuditPayload.ResponseStatus != nil {
		respStatusArg = *filter.AuditPayload.ResponseStatus
	}
	if filter.AuditPayload.Mode != nil {
		modeArg = *filter.AuditPayload.Mode
	}
	if filter.AuditPayload.RequestPath != nil {
		requestPathArg = *filter.AuditPayload.RequestPath
	}

	kindInClause := ""
	args := []any{
		nodeArg, nodeArg, instArg, instArg,
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
	args = append(args,
		keyIDArg, keyIDArg,
		keyNameArg, keyNameArg,
		actionExactArg, actionExactArg,
		actionPrefixArg, actionPrefixArg,
		respStatusArg, respStatusArg,
		modeArg, modeArg,
		requestPathArg, requestPathArg,
	)
	args = append(args, limit)

	rows, err := s.q(tx).QueryContext(ctx,
		`SELECT id, instance_id, node_id, kind, payload, occurred_at
		 FROM rimsky_events
		 WHERE (? IS NULL OR node_id = ?)
		   AND (? IS NULL OR instance_id = ?)
		   AND (? IS NULL OR occurred_at >= ?)
		   AND (? IS NULL OR occurred_at <= ?)
		   AND (? IS NULL OR (occurred_at, id) < (?, ?))`+kindInClause+
			` AND (? IS NULL OR json_extract(payload, '$.key_id') = ?)
		   AND (? IS NULL OR json_extract(payload, '$.key_name') = ?)
		   AND (? IS NULL OR json_extract(payload, '$.action') = ?)
		   AND (? IS NULL OR json_extract(payload, '$.action') LIKE ?)
		   AND (? IS NULL OR json_extract(payload, '$.response_status') = ?)
		   AND (? IS NULL OR json_extract(payload, '$.mode') = ?)
		   AND (? IS NULL OR json_extract(payload, '$.request_path') = ?)
		 ORDER BY occurred_at DESC, id DESC
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
			kindRaw       string
			payloadStr    string
			occurredAtStr string
			eventID       int64
		)
		if err := rows.Scan(&eventID, &instanceIDStr, &nodeIDStr, &kindRaw, &payloadStr, &occurredAtStr); err != nil {
			return persistence.EventListResult{}, err
		}
		// @decision: event-log-kind-enum
		k, err := events.ParseKindString(kindRaw)
		if err != nil {
			slog.Error("PERSISTENCE.EVENTKIND.UNKNOWN", slog.String("raw", kindRaw))
			return persistence.EventListResult{}, fmt.Errorf("events.list: %w", err)
		}
		r.ID = eventID
		r.Kind = k
		r.KindRaw = kindRaw
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
			r.Payload = eventpayload.Decoded(m)
		} else {
			r.Payload = eventpayload.Decoded(map[string]any{})
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
	placeholders := ""
	args := make([]any, 0, len(nodeIDs)+4)
	for i, id := range nodeIDs {
		if i > 0 {
			placeholders += ","
		}
		placeholders += "?"
		args = append(args, id.String())
	}
	terminalKind1, terminalKind2 := events.KindWorkCompleted().String(), events.KindError().String()
	args = append(args, terminalKind1, terminalKind2, terminalKind1, terminalKind2)
	q := `SELECT e.id, e.instance_id, e.node_id, e.kind, e.payload, e.occurred_at
		FROM rimsky_events e
		WHERE e.node_id IN (` + placeholders + `)
		  AND e.kind IN (?, ?)
		  AND e.id = (
		    SELECT e2.id FROM rimsky_events e2
		    WHERE e2.node_id = e.node_id
		      AND e2.kind IN (?, ?)
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
			kindRaw       string
			payloadStr    string
			occurredAtStr string
			eventID       int64
		)
		if err := rows.Scan(&eventID, &instanceIDStr, &nodeIDStr, &kindRaw, &payloadStr, &occurredAtStr); err != nil {
			return nil, err
		}
		k, err := events.ParseKindString(kindRaw)
		if err != nil {
			slog.Error("PERSISTENCE.EVENTKIND.UNKNOWN", slog.String("raw", kindRaw))
			return nil, fmt.Errorf("events.lastTerminalByNodes: %w", err)
		}
		r.ID = eventID
		r.Kind = k
		r.KindRaw = kindRaw
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
			if err := json.Unmarshal([]byte(payloadStr), &m); err != nil {
				return nil, fmt.Errorf("events.lastTerminalByNodes: unmarshal payload: %w", err)
			}
			r.Payload = eventpayload.Decoded(m)
		} else {
			r.Payload = eventpayload.Decoded(map[string]any{})
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

func (s *eventsImpl) DeleteOlderThan(ctx context.Context, cutoff time.Time) (int, error) {
	res, err := (*tablesImpl)(s).db.ExecContext(ctx,
		`DELETE FROM rimsky_events WHERE occurred_at < ?`, formatTime(cutoff))
	if err != nil {
		return 0, fmt.Errorf("sqlite.Events.DeleteOlderThan: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return int(n), nil
}

func (s *eventsImpl) CountAttributeOverrideMatchesByIndex(
	ctx context.Context, instanceID shared.UUID, tx persistence.Tx,
) (map[int64]int64, error) {
	rows, err := s.q(tx).QueryContext(ctx,
		`SELECT CAST(json_extract(payload, '$.override_index') AS INTEGER) AS idx, count(*)
		   FROM rimsky_events
		  WHERE instance_id = ?
		    AND kind = ?
		  GROUP BY idx`,
		instanceID.String(), events.KindAttributeOverrideMatched().String())
	if err != nil {
		return nil, fmt.Errorf("sqlite.Events.CountAttributeOverrideMatchesByIndex: %w", err)
	}
	defer rows.Close()
	out := map[int64]int64{}
	for rows.Next() {
		var idx, cnt int64
		if err := rows.Scan(&idx, &cnt); err != nil {
			return nil, fmt.Errorf("sqlite.Events.CountAttributeOverrideMatchesByIndex: scan: %w", err)
		}
		out[idx] = cnt
	}
	return out, rows.Err()
}
