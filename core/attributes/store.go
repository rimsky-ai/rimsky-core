// Postgres helpers for the rimsky_node_attributes table.
//
// The schema (spec §9.9.1):
//
//	CREATE TABLE IF NOT EXISTS rimsky_node_attributes (
//	    node_id     UUID PRIMARY KEY REFERENCES rimsky_nodes(id) ON DELETE CASCADE,
//	    run_attempt INT NOT NULL DEFAULT 0,
//	    data        JSONB NOT NULL DEFAULT '{}'::jsonb,
//	    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
//	);
//
// This file owns the SQL surface for the table — three helpers (Get,
// Upsert, MergeDelta) on a small *Store struct. Plan Task 10 lifts the
// interface (Store/NodeAttributesStore) into core/storage; this struct
// will implement that interface verbatim. Until then, the supervisor and
// the callback handler reference the local interface NodeAttributesStore
// (defined below) so they compile in isolation.

package attributes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/fallguy/rimsky/core/shared"
)

// Row mirrors a row of rimsky_node_attributes.
type Row struct {
	NodeID     shared.UUID
	RunAttempt int
	Data       map[string]any
	UpdatedAt  time.Time
}

// NodeAttributesStore is the local interface the callback handler depends
// on. Plan Task 10 promotes this exact shape into core/storage; once that
// lands, callers will import it from there and this declaration is
// removed. The shape is fixed by spec §5.7.2 (incremental writeback) +
// §9.9.1 (schema).
type NodeAttributesStore interface {
	Get(ctx context.Context, nodeID shared.UUID) (*Row, error)
	Upsert(ctx context.Context, nodeID shared.UUID, runAttempt int, data map[string]any) error
	MergeDelta(ctx context.Context, nodeID shared.UUID, delta map[string]any) error
}

// Store is the postgres-backed implementation of NodeAttributesStore.
//
// NOTE: this implementation does not participate in a caller-supplied
// storage.Tx today — every method opens a single SQL statement against the
// pool. The merge-delta path is intrinsically atomic (single UPDATE with
// `data || $1::jsonb`); Upsert is a single ON CONFLICT statement. If a
// future call site needs MergeDelta inside the supervisor's outer
// dispatch transaction (see spec §13.3), we will widen the signature to
// take a storage.Tx parameter — the JSONB merge SQL is already
// transaction-safe.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore returns a postgres-backed *Store bound to pool. The pool must
// already point at a database where rimsky migrations have been applied.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

var _ NodeAttributesStore = (*Store)(nil)

// Get returns the rimsky_node_attributes row for nodeID, or (nil, nil)
// when the row is absent. Absence is a normal lifecycle state — the row
// is created lazily on first dispatch (spec §9.9.1).
func (s *Store) Get(ctx context.Context, nodeID shared.UUID) (*Row, error) {
	var (
		runAttempt int
		dataBytes  []byte
		updatedAt  time.Time
	)
	err := s.pool.QueryRow(ctx,
		`SELECT run_attempt, data, updated_at
		   FROM rimsky_node_attributes
		  WHERE node_id = $1`,
		nodeID,
	).Scan(&runAttempt, &dataBytes, &updatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("attributes.get: %w", err)
	}
	data := map[string]any{}
	if len(dataBytes) > 0 {
		if err := json.Unmarshal(dataBytes, &data); err != nil {
			return nil, fmt.Errorf("attributes.get: unmarshal data: %w", err)
		}
	}
	return &Row{NodeID: nodeID, RunAttempt: runAttempt, Data: data, UpdatedAt: updatedAt}, nil
}

// Upsert writes the row in one statement. Used by the supervisor at
// dispatch (after substitution, before handing the executor the request)
// and by retry/resume logic. Replaces `data` outright — callers that want
// to merge use MergeDelta.
func (s *Store) Upsert(ctx context.Context, nodeID shared.UUID, runAttempt int, data map[string]any) error {
	if data == nil {
		data = map[string]any{}
	}
	dataBytes, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("attributes.upsert: marshal data: %w", err)
	}
	_, err = s.pool.Exec(ctx,
		`INSERT INTO rimsky_node_attributes (node_id, run_attempt, data, updated_at)
		 VALUES ($1, $2, $3::jsonb, NOW())
		 ON CONFLICT (node_id) DO UPDATE
		   SET run_attempt = EXCLUDED.run_attempt,
		       data        = EXCLUDED.data,
		       updated_at  = NOW()`,
		nodeID, runAttempt, dataBytes,
	)
	if err != nil {
		return fmt.Errorf("attributes.upsert: %w", err)
	}
	return nil
}

// MergeDelta performs an in-place JSONB shallow merge. SQL: `data ||
// $1::jsonb`. Used by the §12.5 incremental writeback callback (the
// executor POSTs `{"delta": {...}}` and the supervisor merges per-call).
//
// Important: PG's `||` operator is a SHALLOW merge. Top-level keys in
// `delta` overwrite top-level keys in `data`; nested objects are
// REPLACED, not deep-merged. That matches the executor protocol — the
// callback contract delivers field-keyed deltas and an executor wanting
// nested-key precision sends nested-key deltas.
//
// The row is required to exist — MergeDelta returns an error when no row
// matches (the supervisor calls Upsert first, at dispatch). This keeps
// the merge-vs-create policy explicit and prevents the callback from
// silently creating a row for a node the supervisor hasn't dispatched
// yet.
func (s *Store) MergeDelta(ctx context.Context, nodeID shared.UUID, delta map[string]any) error {
	if delta == nil {
		// No-op merge. Still bump updated_at so observers can tell the
		// callback fired even when it carried no fields.
		_, err := s.pool.Exec(ctx,
			`UPDATE rimsky_node_attributes
			    SET updated_at = NOW()
			  WHERE node_id = $1`,
			nodeID,
		)
		if err != nil {
			return fmt.Errorf("attributes.mergeDelta: touch: %w", err)
		}
		return nil
	}
	deltaBytes, err := json.Marshal(delta)
	if err != nil {
		return fmt.Errorf("attributes.mergeDelta: marshal delta: %w", err)
	}
	tag, err := s.pool.Exec(ctx,
		`UPDATE rimsky_node_attributes
		    SET data       = data || $1::jsonb,
		        updated_at = NOW()
		  WHERE node_id = $2`,
		deltaBytes, nodeID,
	)
	if err != nil {
		return fmt.Errorf("attributes.mergeDelta: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("attributes.mergeDelta: no row for node %s", nodeID)
	}
	return nil
}
