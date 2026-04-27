// Package claimstorepg implements the postgres-backed claim store described
// in spec §7. The store hands out items from an operator-owned items table
// (schema in §9.10) via the "claim" lock kind: a node asks for a region, the
// store picks one, atomically flips its `state` to `'in_progress'`, and
// reports the picked row's payload + ID. Lock state itself lives in
// `rimsky_lock_holders` (spec §5.3 invariant), not in this store.
//
// Items table is operator-owned. The factory verifies the table exists with
// the §9.10 columns at registry build time; missing or malformed → fail-fast.
//
// Imports: `core/store/`, `pgx/v5`, `pgxpool`, stdlib. (Per spec §8.1.)
package claimstorepg

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/fallguy/rimsky/core/store"
)

// Factory builds postgres-backed claim stores from per-store YAML config.
// Registered with a *store.Registry under kind "claim_store".
//
// The factory holds a pool reference because Build needs to verify the
// items table's schema at startup; the same pool is threaded into the
// constructed *Store so it can fall back to non-tx reads (e.g. eligibility
// hints) when no outer tx is supplied.
type Factory struct {
	Pool *pgxpool.Pool
}

// Kind returns the canonical store kind, "claim_store".
func (Factory) Kind() string { return "claim_store" }

// Build constructs a *Store from operator-supplied config. Required keys:
//
//   - backend: must be "postgres" (only backend supported in v1).
//   - items_table: name of the operator-owned items table (§9.10).
//   - on_commit_default: action when a commit fires; one of
//     'delete' | 'release_to_back' | 'release_to_head'.
//   - on_give_up_default: same vocabulary; action when a give-up fires.
//   - visibility_timeout_seconds: int seconds (≥0) for the §7.7 backstop.
//
// The factory issues a SELECT against information_schema.columns to verify
// the items table exists and has the §9.10 column shape. A missing table
// or column-shape mismatch → startup failure with a clear error.
func (f Factory) Build(name string, cfg map[string]any) (store.Store, error) {
	if f.Pool == nil {
		return nil, fmt.Errorf("claim_store %q: factory missing pgxpool.Pool — wire one in at registration time", name)
	}

	backend, err := requireString(cfg, "backend", name)
	if err != nil {
		return nil, err
	}
	if backend != "postgres" {
		return nil, fmt.Errorf("claim_store %q: only backend=postgres is supported in v1, got %q", name, backend)
	}

	itemsTable, err := requireString(cfg, "items_table", name)
	if err != nil {
		return nil, err
	}
	if !validIdent(itemsTable) {
		return nil, fmt.Errorf("claim_store %q: items_table %q is not a valid identifier (lowercase letters/digits/underscore; not starting with a digit)", name, itemsTable)
	}

	onCommit, err := requireString(cfg, "on_commit_default", name)
	if err != nil {
		return nil, err
	}
	if !validReleaseAction(onCommit) {
		return nil, fmt.Errorf("claim_store %q: on_commit_default %q must be one of 'delete' | 'release_to_back' | 'release_to_head'", name, onCommit)
	}

	onGiveUp, err := requireString(cfg, "on_give_up_default", name)
	if err != nil {
		return nil, err
	}
	if !validReleaseAction(onGiveUp) {
		return nil, fmt.Errorf("claim_store %q: on_give_up_default %q must be one of 'delete' | 'release_to_back' | 'release_to_head'", name, onGiveUp)
	}

	visTimeoutSecs, err := requireInt(cfg, "visibility_timeout_seconds", name)
	if err != nil {
		return nil, err
	}
	if visTimeoutSecs < 0 {
		return nil, fmt.Errorf("claim_store %q: visibility_timeout_seconds must be ≥ 0, got %d", name, visTimeoutSecs)
	}

	// Verify the items table exists with the §9.10 columns. Fail-fast on
	// mismatch so misconfigured deployments surface at startup, not at
	// dispatch time.
	if err := verifyItemsTable(context.Background(), f.Pool, itemsTable); err != nil {
		return nil, fmt.Errorf("claim_store %q: items table %q: %w", name, itemsTable, err)
	}

	return &Store{
		name:              name,
		pool:              f.Pool,
		itemsTable:        itemsTable,
		onCommitDefault:   onCommit,
		onGiveUpDefault:   onGiveUp,
		visibilityTimeout: time.Duration(visTimeoutSecs) * time.Second,
	}, nil
}

// requireString pulls a string field out of cfg or returns a typed error.
func requireString(cfg map[string]any, key, storeName string) (string, error) {
	raw, ok := cfg[key]
	if !ok {
		return "", fmt.Errorf("claim_store %q: missing %q field", storeName, key)
	}
	s, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("claim_store %q: %q must be string, got %T", storeName, key, raw)
	}
	if s == "" {
		return "", fmt.Errorf("claim_store %q: %q must not be empty", storeName, key)
	}
	return s, nil
}

// requireInt pulls an int field out of cfg, accepting common YAML decode
// shapes (int, int64, float64). Returns a typed error on missing or wrong-
// shape input.
func requireInt(cfg map[string]any, key, storeName string) (int, error) {
	raw, ok := cfg[key]
	if !ok {
		return 0, fmt.Errorf("claim_store %q: missing %q field", storeName, key)
	}
	switch v := raw.(type) {
	case int:
		return v, nil
	case int64:
		return int(v), nil
	case float64:
		// YAML decoders often hand back float64; round-trip and verify
		// it's actually an integer value.
		if v != float64(int(v)) {
			return 0, fmt.Errorf("claim_store %q: %q must be an integer, got fractional %v", storeName, key, v)
		}
		return int(v), nil
	default:
		return 0, fmt.Errorf("claim_store %q: %q must be int, got %T", storeName, key, raw)
	}
}

// validReleaseAction reports whether s is one of the three allowed claim-
// store actions per spec §5.6.4.
func validReleaseAction(s string) bool {
	return s == "delete" || s == "release_to_back" || s == "release_to_head"
}

// validIdent reports whether s is a safe SQL identifier we can interpolate
// into a query. We do not allow quoted identifiers or schema-qualified
// names in v1; operators set up the items table in the search_path schema.
func validIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
			if i == 0 {
				return false
			}
		case r == '_':
		default:
			return false
		}
	}
	return true
}

// expectedColumns lists the §9.10 column requirements: name → expected
// data_type substring. We use substring match because pg reports e.g.
// "timestamp with time zone" for TIMESTAMPTZ; "USER-DEFINED" or "ARRAY"
// shapes are not expected here.
var expectedColumns = []struct {
	name     string
	dataType string
}{
	{"item_id", "uuid"},
	{"payload", "jsonb"},
	{"enqueued_at", "timestamp with time zone"},
	{"state", "text"},
	{"claim_token", "uuid"},
	{"claimed_at", "timestamp with time zone"},
}

// verifyItemsTable runs a SELECT against information_schema.columns
// confirming the items table has the §9.10 column shape. Fails on missing
// table, missing column, or wrong type for any required column.
func verifyItemsTable(ctx context.Context, pool *pgxpool.Pool, table string) error {
	rows, err := pool.Query(ctx,
		`SELECT column_name, data_type
		   FROM information_schema.columns
		  WHERE table_schema = current_schema()
		    AND table_name = $1`,
		table,
	)
	if err != nil {
		return fmt.Errorf("query information_schema.columns: %w", err)
	}
	defer rows.Close()

	got := make(map[string]string)
	for rows.Next() {
		var col, typ string
		if err := rows.Scan(&col, &typ); err != nil {
			return fmt.Errorf("scan column row: %w", err)
		}
		got[col] = typ
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate column rows: %w", err)
	}
	if len(got) == 0 {
		return fmt.Errorf("table not found in current schema (or has zero columns)")
	}
	for _, want := range expectedColumns {
		gotType, ok := got[want.name]
		if !ok {
			return fmt.Errorf("missing column %q (expected type %q)", want.name, want.dataType)
		}
		if gotType != want.dataType {
			return fmt.Errorf("column %q has type %q, expected %q", want.name, gotType, want.dataType)
		}
	}
	return nil
}
