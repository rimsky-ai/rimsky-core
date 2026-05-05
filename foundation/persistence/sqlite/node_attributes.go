// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// node_attributes.go — SQLite-backed persistence.NodeAttributesStore.
//
// `data` is a TEXT (JSON) column. Upsert replaces it outright; MergeDelta
// performs a SHALLOW merge by reading the existing row, merging in Go,
// and writing back — SQLite has no JSONB `||` operator.
package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/modeling/shared"
)

func (s *nodeAttributesImpl) Get(ctx context.Context, nodeID shared.UUID, tx persistence.Tx) (*persistence.NodeAttributesRow, error) {
	row := s.q(tx).QueryRowContext(ctx,
		`SELECT node_id, run_attempt, data, updated_at
		   FROM rimsky_node_attributes
		  WHERE node_id = ?`, nodeID.String(),
	)
	var (
		idStr        string
		runAttempt   int
		dataStr      string
		updatedAtStr string
	)
	if err := row.Scan(&idStr, &runAttempt, &dataStr, &updatedAtStr); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("node_attributes.Get: %w", err)
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		return nil, fmt.Errorf("node_attributes.Get: bad id: %w", err)
	}
	updatedAt, err := parseTime(updatedAtStr)
	if err != nil {
		return nil, err
	}
	out := persistence.NodeAttributesRow{
		NodeID:     id,
		RunAttempt: runAttempt,
		UpdatedAt:  updatedAt,
	}
	if dataStr == "" {
		out.Data = map[string]any{}
	} else {
		m := map[string]any{}
		if err := json.Unmarshal([]byte(dataStr), &m); err != nil {
			return nil, fmt.Errorf("node_attributes.Get: unmarshal data: %w", err)
		}
		out.Data = m
	}
	return &out, nil
}

func (s *nodeAttributesImpl) Upsert(ctx context.Context, nodeID shared.UUID, runAttempt int, data map[string]any, tx persistence.Tx) error {
	if data == nil {
		data = map[string]any{}
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("node_attributes.Upsert: marshal: %w", err)
	}
	_, err = s.q(tx).ExecContext(ctx,
		`INSERT INTO rimsky_node_attributes (node_id, run_attempt, data, updated_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(node_id) DO UPDATE
		   SET run_attempt = excluded.run_attempt,
		       data        = excluded.data,
		       updated_at  = excluded.updated_at`,
		nodeID.String(), runAttempt, string(raw), nowUTC(),
	)
	if err != nil {
		return fmt.Errorf("node_attributes.Upsert: %w", err)
	}
	return nil
}

// MergeDelta runs a SHALLOW merge. SQLite has no JSONB `||`; we read,
// merge in Go, and write back. Per spec §5.7.2.
//
// nil-delta is a no-op merge: bumps updated_at if the row exists, silent
// no-op if absent. Mirrors postgres impl semantics.
//
// Atomicity: when tx != nil the read-then-write runs inside the caller's
// BEGIN IMMEDIATE tx (writer-slot held for the duration). When tx == nil
// the SQLite driver's MaxOpenConns=1 (see sqliteMaxOpenConns in
// driver.go) serializes any concurrent caller at the connection level.
// Both paths prevent the read-then-write race; raising MaxOpenConns
// would break the tx==nil path.
func (s *nodeAttributesImpl) MergeDelta(ctx context.Context, nodeID shared.UUID, delta map[string]any, tx persistence.Tx) error {
	if delta == nil {
		_, err := s.q(tx).ExecContext(ctx,
			`UPDATE rimsky_node_attributes
			    SET updated_at = ?
			  WHERE node_id = ?`,
			nowUTC(), nodeID.String(),
		)
		if err != nil {
			return fmt.Errorf("node_attributes.MergeDelta: touch: %w", err)
		}
		return nil
	}
	row := s.q(tx).QueryRowContext(ctx,
		`SELECT data FROM rimsky_node_attributes WHERE node_id = ?`,
		nodeID.String(),
	)
	var dataStr string
	if err := row.Scan(&dataStr); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("node_attributes.MergeDelta: %w", persistence.ErrNotFound)
		}
		return fmt.Errorf("node_attributes.MergeDelta: read: %w", err)
	}
	current := map[string]any{}
	if dataStr != "" {
		if err := json.Unmarshal([]byte(dataStr), &current); err != nil {
			return fmt.Errorf("node_attributes.MergeDelta: unmarshal current: %w", err)
		}
	}
	for k, v := range delta {
		current[k] = v
	}
	merged, err := json.Marshal(current)
	if err != nil {
		return fmt.Errorf("node_attributes.MergeDelta: marshal: %w", err)
	}
	res, err := s.q(tx).ExecContext(ctx,
		`UPDATE rimsky_node_attributes
		    SET data = ?,
		        updated_at = ?
		  WHERE node_id = ?`,
		string(merged), nowUTC(), nodeID.String(),
	)
	if err != nil {
		return fmt.Errorf("node_attributes.MergeDelta: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("node_attributes.MergeDelta: rows-affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("node_attributes.MergeDelta: %w", persistence.ErrNotFound)
	}
	return nil
}
