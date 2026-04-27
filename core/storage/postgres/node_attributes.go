// NodeAttributesStore is the postgres accessor for `rimsky_node_attributes`
// (spec §9.9.1). The table is created lazily on first dispatch of a node;
// callers that read before any write see (nil, nil) from Get.
//
// `data` is a JSONB column. Upsert replaces it outright; MergeDelta runs a
// SHALLOW JSONB merge (`data || $1::jsonb`) and requires the row to exist
// (spec §5.7.2).
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/storage"
)

// NodeAttributesStore is the postgres NodeAttributesStore implementation.
// Methods bypass the storage.Tx abstraction because the supervisor's
// retry / dispatch paths talk to it directly.
type NodeAttributesStore struct {
	pool *pgxpool.Pool
}

var _ storage.NodeAttributesStore = (*NodeAttributesStore)(nil)

// Get returns the row for nodeID or (nil, nil) when no row exists.
func (s *NodeAttributesStore) Get(ctx context.Context, nodeID shared.UUID) (*storage.NodeAttributesRow, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT node_id, run_attempt, data, updated_at
		   FROM rimsky_node_attributes
		  WHERE node_id = $1`, nodeID,
	)
	var (
		out  storage.NodeAttributesRow
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
func (s *NodeAttributesStore) Upsert(ctx context.Context, nodeID shared.UUID, runAttempt int, data map[string]any) error {
	if data == nil {
		data = map[string]any{}
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("node_attributes.Upsert: marshal: %w", err)
	}
	_, err = s.pool.Exec(ctx,
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
// Requires the row to exist; returns an error wrapping ErrNoRows when
// absent (per spec §5.7.2 — incremental writeback presupposes the
// dispatch row has been created).
func (s *NodeAttributesStore) MergeDelta(ctx context.Context, nodeID shared.UUID, delta map[string]any) error {
	if delta == nil {
		delta = map[string]any{}
	}
	raw, err := json.Marshal(delta)
	if err != nil {
		return fmt.Errorf("node_attributes.MergeDelta: marshal: %w", err)
	}
	tag, err := s.pool.Exec(ctx,
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
		return fmt.Errorf("node_attributes.MergeDelta: %w", pgx.ErrNoRows)
	}
	return nil
}

// IncrementRunAttempt bumps `run_attempt` by 1 atomically and returns the
// new value. Used at retry-prep time before ClearExecutorPopulated runs.
// Returns ErrNoRows when no row exists for nodeID.
func (s *NodeAttributesStore) IncrementRunAttempt(ctx context.Context, nodeID shared.UUID) (int, error) {
	row := s.pool.QueryRow(ctx,
		`UPDATE rimsky_node_attributes
		    SET run_attempt = run_attempt + 1,
		        updated_at = now()
		  WHERE node_id = $1
		  RETURNING run_attempt`,
		nodeID,
	)
	var n int
	if err := row.Scan(&n); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, pgx.ErrNoRows
		}
		return 0, fmt.Errorf("node_attributes.IncrementRunAttempt: %w", err)
	}
	return n, nil
}

// ClearExecutorPopulated implements the source-aware retry-clear of
// spec §5.7.3. Walks the schema's top-level `properties`; any property
// whose declaration carries a `source:` directive is preserved (it will
// be repopulated from upstream / claim / params at the next dispatch).
// Any property without `source:` is removed from `data`.
//
// The schema argument is the raw JSON Schema (draft-07) for the node's
// attributes — typically loaded from the in-memory template registry's
// `attributes.schema` field. Passing it explicitly keeps this package
// independent of core/node and core/attributes (avoids the cycle).
//
// Behaviour notes:
//   - Properties present in `data` but absent from the schema's
//     `properties` map are kept verbatim. The schema-validation pass at
//     commit (§5.7.1) is the place to reject those; clearing here would
//     mask bugs.
//   - Schemas without a top-level `properties` map are tolerated
//     (treated as "all fields executor-populated") to keep early-stage
//     templates working.
func (s *NodeAttributesStore) ClearExecutorPopulated(
	ctx context.Context, nodeID shared.UUID, schema map[string]any,
) error {
	current, err := s.Get(ctx, nodeID)
	if err != nil {
		return err
	}
	if current == nil {
		// No row yet; nothing to clear. The next dispatch will populate.
		return nil
	}
	executorKeys := executorPopulatedKeys(schema, current.Data)
	if len(executorKeys) == 0 {
		// Nothing to clear (every populated key is source-driven, or the
		// row's data map is empty).
		return nil
	}
	pruned := make(map[string]any, len(current.Data))
	for k, v := range current.Data {
		if _, isExecutor := executorKeys[k]; isExecutor {
			continue
		}
		pruned[k] = v
	}
	raw, err := json.Marshal(pruned)
	if err != nil {
		return fmt.Errorf("node_attributes.ClearExecutorPopulated: marshal: %w", err)
	}
	_, err = s.pool.Exec(ctx,
		`UPDATE rimsky_node_attributes
		    SET data = $2::jsonb,
		        updated_at = now()
		  WHERE node_id = $1`,
		nodeID, raw,
	)
	if err != nil {
		return fmt.Errorf("node_attributes.ClearExecutorPopulated: %w", err)
	}
	return nil
}

// executorPopulatedKeys returns the set of keys in `data` that, per the
// schema, are executor-populated (i.e. the schema declares the property
// without a `source:` directive). Keys not present in the schema's
// properties map are NOT returned (they are kept verbatim — see the
// behaviour note in ClearExecutorPopulated).
func executorPopulatedKeys(schema map[string]any, data map[string]any) map[string]struct{} {
	if len(data) == 0 {
		return nil
	}
	props := schemaProperties(schema)
	if props == nil {
		return nil
	}
	out := map[string]struct{}{}
	for k := range data {
		propRaw, ok := props[k]
		if !ok {
			continue
		}
		propMap, ok := propRaw.(map[string]any)
		if !ok {
			continue
		}
		if _, hasSource := propMap["source"]; hasSource {
			continue
		}
		out[k] = struct{}{}
	}
	return out
}

func schemaProperties(schema map[string]any) map[string]any {
	if schema == nil {
		return nil
	}
	raw, ok := schema["properties"]
	if !ok {
		return nil
	}
	props, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	return props
}
