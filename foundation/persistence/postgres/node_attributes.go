// node_attributes.go is the postgres accessor for `rimsky_node_attributes`
// (spec §9.9.1). The table is created lazily on first dispatch of a node;
// callers that read before any write see (nil, nil) from Get.
//
// `data` is a JSONB column. Upsert replaces it outright; MergeDelta runs a
// SHALLOW JSONB merge (`data || $1::jsonb`) and requires the row to exist
// (spec §5.7.2). Every method takes a `tx persistence.Tx` so callers
// participate in the supervisor's tx when needed.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/modeling/shared"
)

// Get returns the row for nodeID or (nil, nil) when no row exists.
func (s *nodeAttributesImpl) Get(ctx context.Context, nodeID shared.UUID, tx persistence.Tx) (*persistence.NodeAttributesRow, error) {
	row := s.q(tx).QueryRow(ctx,
		`SELECT node_id, run_attempt, data, updated_at
		   FROM rimsky_node_attributes
		  WHERE node_id = $1`, nodeID,
	)
	var (
		out  persistence.NodeAttributesRow
		raw  []byte
		when time.Time
	)
	if err := row.Scan(&out.NodeID, &out.RunAttempt, &raw, &when); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("node_attributes.Get: %w", err)
	}
	out.UpdatedAt = when
	if len(raw) == 0 {
		out.Data = map[string]any{}
	} else {
		m := map[string]any{}
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, fmt.Errorf("node_attributes.Get: unmarshal data: %w", err)
		}
		out.Data = m
	}
	return &out, nil
}

// Upsert writes (or replaces) the row for nodeID. `data` overwrites any
// prior value.
func (s *nodeAttributesImpl) Upsert(ctx context.Context, nodeID shared.UUID, runAttempt int, data map[string]any, tx persistence.Tx) error {
	if data == nil {
		data = map[string]any{}
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("node_attributes.Upsert: marshal: %w", err)
	}
	_, err = s.q(tx).Exec(ctx,
		`INSERT INTO rimsky_node_attributes (node_id, run_attempt, data, updated_at)
		 VALUES ($1, $2, $3::jsonb, now())
		 ON CONFLICT (node_id) DO UPDATE
		   SET run_attempt = EXCLUDED.run_attempt,
		       data        = EXCLUDED.data,
		       updated_at  = now()`,
		nodeID, runAttempt, raw,
	)
	if err != nil {
		return fmt.Errorf("node_attributes.Upsert: %w", err)
	}
	return nil
}

// MergeDelta performs a shallow JSONB merge (`data || $delta::jsonb`).
// Requires the row to exist on a non-nil-delta call; returns an error
// wrapping ErrNoRows when absent.
//
// nil-delta is a no-op merge: bumps updated_at if the row exists,
// silently no-ops if it doesn't. This matches the active runtime impl
// in core/attributes/store.go::MergeDelta, which uses pool.Exec without
// checking rows-affected on the touch path.
func (s *nodeAttributesImpl) MergeDelta(ctx context.Context, nodeID shared.UUID, delta map[string]any, tx persistence.Tx) error {
	if delta == nil {
		_, err := s.q(tx).Exec(ctx,
			`UPDATE rimsky_node_attributes
			    SET updated_at = now()
			  WHERE node_id = $1`,
			nodeID,
		)
		if err != nil {
			return fmt.Errorf("node_attributes.MergeDelta: touch: %w", err)
		}
		return nil
	}
	raw, err := json.Marshal(delta)
	if err != nil {
		return fmt.Errorf("node_attributes.MergeDelta: marshal: %w", err)
	}
	tag, err := s.q(tx).Exec(ctx,
		`UPDATE rimsky_node_attributes
		    SET data = data || $2::jsonb,
		        updated_at = now()
		  WHERE node_id = $1`,
		nodeID, raw,
	)
	if err != nil {
		return fmt.Errorf("node_attributes.MergeDelta: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("node_attributes.MergeDelta: %w", persistence.ErrNotFound)
	}
	return nil
}
