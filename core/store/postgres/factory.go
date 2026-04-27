// Factory for the postgres-backed store. Builds *Store instances from
// per-store YAML config; verifies the items_table for each configured
// pick policy at startup so misconfigured deployments fail fast.

package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/fallguy/rimsky/core/store"
)

// Factory builds postgres-backed stores from per-store YAML config.
// Registered with a *store.Registry under kind "postgres".
//
// Each `kind: postgres` store carries its own `connection:` field — a
// Postgres DSN — and the factory opens a dedicated *pgxpool.Pool against
// that DSN for the constructed *Store and the items-table verify. The
// factory itself holds no pool: every store is independent. Operators
// who want a workload store collocated with rimsky's control-plane
// database should declare a store with the same DSN as RIMSKY_DB_URL;
// implicit reuse is intentionally not supported (one config surface,
// no name-by-omission magic).
type Factory struct{}

// Kind returns the canonical store kind, "postgres".
func (Factory) Kind() string { return "postgres" }

// MaxWriteSemantics returns WriteSemanticsDirect — v1 postgres does
// not implement staging. The protocol supports staged_blocking /
// staged_async; the supervisor's atomic-acquisition + auto-terminal
// machinery handles those modes uniformly when a future substrate
// implements them. (Per spec §M1.)
func (Factory) MaxWriteSemantics() store.WriteSemantics { return store.WriteSemanticsDirect }

// Build constructs a *Store from operator-supplied config. Recognised
// keys (top level):
//
//   - connection (string, required): postgres connection URL for this
//     store. The factory opens a dedicated *pgxpool.Pool against this
//     URL — used by the constructed *Store and by the items-table
//     verify. To collocate a workload store with rimsky's control-plane
//     database, declare the store with the same DSN as RIMSKY_DB_URL;
//     there is no implicit fallback to a "platform pool".
//   - write_semantics (string): defaults to "direct"; capped at the
//     factory's MaxWriteSemantics by the registry's ceiling check.
//   - pick_policies (map[string]any): keyed by recognized selector form
//     (e.g. "@review-queue"). Each value is a map with substrate-
//     specific config (see below).
//
// Per-pick-policy config keys (substrate-defined; this factory parses
// the queue/ring shape):
//
//   - type (string): "queue" | "ring" | etc. Informational; the
//     factory does not gate behaviour on this in v1.
//   - items_table (string): the operator-owned items-table name. Must
//     match [a-z0-9_] and start with a letter. Schema verified against
//     spec §12.12 at startup.
//   - on_commit_default (string): default action on Commit; one of
//     "delete" | "release_to_back" | "release_to_head".
//   - on_give_up_default (string): default action on Abandon; same
//     vocabulary.
//   - visibility_timeout_seconds (int): the §12.12 backstop window for
//     the scheduler's items-table sweep. ≥0; 0 disables.
func (Factory) Build(name string, cfg map[string]any) (store.Store, error) {
	pool, err := openPoolForStore(name, cfg)
	if err != nil {
		return nil, err
	}
	// closeOnError releases the pool we just opened if Build returns
	// before constructing the *Store (so Registry.Close has nothing to
	// close on our behalf).
	closeOnError := func() { pool.Close() }

	ws := store.WriteSemanticsDirect
	if wsRaw, ok := cfg["write_semantics"]; ok {
		wsStr, ok := wsRaw.(string)
		if !ok {
			closeOnError()
			return nil, fmt.Errorf("postgres store %q: write_semantics must be string, got %T", name, wsRaw)
		}
		ws = store.WriteSemantics(wsStr)
	}

	policies := make(map[string]*pickPolicy)
	if rawPolicies, ok := cfg["pick_policies"]; ok {
		policyMap, ok := rawPolicies.(map[string]any)
		if !ok {
			closeOnError()
			return nil, fmt.Errorf("postgres store %q: pick_policies must be map, got %T", name, rawPolicies)
		}
		for selector, raw := range policyMap {
			pol, err := parsePickPolicy(name, selector, raw)
			if err != nil {
				closeOnError()
				return nil, err
			}
			if err := verifyItemsTable(context.Background(), pool, pol.itemsTable); err != nil {
				closeOnError()
				return nil, fmt.Errorf("postgres store %q: pick_policies[%q]: items table %q: %w",
					name, selector, pol.itemsTable, err)
			}
			policies[selector] = pol
		}
	}

	return &Store{
		name:           name,
		pool:           pool,
		writeSemantics: ws,
		pickPolicies:   policies,
	}, nil
}

// openPoolForStore opens a fresh *pgxpool.Pool for one store entry.
// `connection:` is required; rimsky no longer maintains a fallback
// "platform pool" for stores that omit it. Operators who want a store
// collocated with rimsky's control-plane database declare the store
// with the same DSN as RIMSKY_DB_URL — explicit, no implicit sharing.
func openPoolForStore(name string, cfg map[string]any) (*pgxpool.Pool, error) {
	connRaw, hasConn := cfg["connection"]
	if !hasConn {
		return nil, fmt.Errorf("postgres store %q: missing required `connection:` field (a Postgres DSN)", name)
	}
	conn, ok := connRaw.(string)
	if !ok {
		return nil, fmt.Errorf("postgres store %q: connection must be string, got %T", name, connRaw)
	}
	if conn == "" {
		return nil, fmt.Errorf("postgres store %q: connection must be non-empty", name)
	}
	pool, err := pgxpool.New(context.Background(), conn)
	if err != nil {
		return nil, fmt.Errorf("postgres store %q: open pool: %w", name, err)
	}
	return pool, nil
}

// parsePickPolicy validates and constructs one pickPolicy from the raw
// config map.
func parsePickPolicy(storeName, selector string, raw any) (*pickPolicy, error) {
	cfg, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("postgres store %q: pick_policies[%q] must be map, got %T", storeName, selector, raw)
	}
	policyType, _ := cfg["type"].(string)

	itemsTable, err := requireString(cfg, "items_table", storeName, selector)
	if err != nil {
		return nil, err
	}
	if !validIdent(itemsTable) {
		return nil, fmt.Errorf("postgres store %q: pick_policies[%q].items_table %q is not a valid identifier (lowercase letters/digits/underscore; not starting with a digit)",
			storeName, selector, itemsTable)
	}

	onCommit, err := requireString(cfg, "on_commit_default", storeName, selector)
	if err != nil {
		return nil, err
	}
	if !validPickAction(onCommit) {
		return nil, fmt.Errorf("postgres store %q: pick_policies[%q].on_commit_default %q must be one of 'delete' | 'release_to_back' | 'release_to_head'",
			storeName, selector, onCommit)
	}

	onGiveUp, err := requireString(cfg, "on_give_up_default", storeName, selector)
	if err != nil {
		return nil, err
	}
	if !validPickAction(onGiveUp) {
		return nil, fmt.Errorf("postgres store %q: pick_policies[%q].on_give_up_default %q must be one of 'delete' | 'release_to_back' | 'release_to_head'",
			storeName, selector, onGiveUp)
	}

	visTimeoutSecs := 300
	if raw, ok := cfg["visibility_timeout_seconds"]; ok {
		v, err := asInt(raw)
		if err != nil {
			return nil, fmt.Errorf("postgres store %q: pick_policies[%q].visibility_timeout_seconds: %w", storeName, selector, err)
		}
		if v < 0 {
			return nil, fmt.Errorf("postgres store %q: pick_policies[%q].visibility_timeout_seconds must be ≥ 0, got %d", storeName, selector, v)
		}
		visTimeoutSecs = v
	}

	return &pickPolicy{
		policyType:        policyType,
		itemsTable:        itemsTable,
		onCommitDefault:   onCommit,
		onGiveUpDefault:   onGiveUp,
		visibilityTimeout: time.Duration(visTimeoutSecs) * time.Second,
	}, nil
}

// requireString pulls a non-empty string field out of cfg or returns a
// typed error.
func requireString(cfg map[string]any, key, storeName, selector string) (string, error) {
	raw, ok := cfg[key]
	if !ok {
		return "", fmt.Errorf("postgres store %q: pick_policies[%q]: missing %q field", storeName, selector, key)
	}
	s, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("postgres store %q: pick_policies[%q]: %q must be string, got %T", storeName, selector, key, raw)
	}
	if s == "" {
		return "", fmt.Errorf("postgres store %q: pick_policies[%q]: %q must not be empty", storeName, selector, key)
	}
	return s, nil
}

// asInt accepts the common YAML shapes for an integer-valued field.
func asInt(raw any) (int, error) {
	switch v := raw.(type) {
	case int:
		return v, nil
	case int64:
		return int(v), nil
	case float64:
		if v != float64(int(v)) {
			return 0, fmt.Errorf("must be an integer, got fractional %v", v)
		}
		return int(v), nil
	default:
		return 0, fmt.Errorf("must be int, got %T", raw)
	}
}

// expectedColumns lists the §12.12 column requirements: name → expected
// data_type substring. Substring match because pg reports e.g.
// "timestamp with time zone" for TIMESTAMPTZ.
var expectedColumns = []struct {
	name     string
	dataType string
}{
	{"item_id", "text"},
	{"payload", "jsonb"},
	{"state", "text"},
	{"claim_token", "text"},
	{"claimed_at", "timestamp with time zone"},
	{"enqueued_at", "timestamp with time zone"},
	{"priority", "integer"},
	{"sequence", "bigint"},
}

// verifyItemsTable runs a SELECT against information_schema.columns
// confirming the items table has the §12.12 column shape. Fails on
// missing table, missing column, or wrong type for any required column.
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
