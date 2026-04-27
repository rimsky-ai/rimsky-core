package claimstorepg

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/fallguy/rimsky/core/internal/pgtest"
)

// TestFactory_Build_Validation exercises the cfg-validation branches that
// don't require a real items table. The factory still requires a non-nil
// pool (for the schema-verify step), so we spin one up once here and reuse
// it across all sub-cases.
func TestFactory_Build_Validation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool, teardown := pgtest.StartPostgres(ctx, t)
	t.Cleanup(teardown)

	// Create a valid items table once so the "happy path" sub-case has a
	// real table to verify against. The negative cases all fail before
	// the schema-verify step, so they don't need the table.
	mustCreateItemsTable(t, pool, "factory_items")

	cases := []struct {
		name      string
		cfg       map[string]any
		wantError bool
	}{
		{
			name: "happy path",
			cfg: map[string]any{
				"backend":                    "postgres",
				"items_table":                "factory_items",
				"on_commit_default":          "delete",
				"on_give_up_default":         "release_to_head",
				"visibility_timeout_seconds": 300,
			},
			wantError: false,
		},
		{
			name: "missing backend",
			cfg: map[string]any{
				"items_table":                "factory_items",
				"on_commit_default":          "delete",
				"on_give_up_default":         "release_to_head",
				"visibility_timeout_seconds": 300,
			},
			wantError: true,
		},
		{
			name: "non-postgres backend",
			cfg: map[string]any{
				"backend":                    "redis",
				"items_table":                "factory_items",
				"on_commit_default":          "delete",
				"on_give_up_default":         "release_to_head",
				"visibility_timeout_seconds": 300,
			},
			wantError: true,
		},
		{
			name: "missing items_table",
			cfg: map[string]any{
				"backend":                    "postgres",
				"on_commit_default":          "delete",
				"on_give_up_default":         "release_to_head",
				"visibility_timeout_seconds": 300,
			},
			wantError: true,
		},
		{
			name: "non-existent items_table",
			cfg: map[string]any{
				"backend":                    "postgres",
				"items_table":                "no_such_table",
				"on_commit_default":          "delete",
				"on_give_up_default":         "release_to_head",
				"visibility_timeout_seconds": 300,
			},
			wantError: true,
		},
		{
			name: "invalid identifier",
			cfg: map[string]any{
				"backend":                    "postgres",
				"items_table":                "Bad-Name",
				"on_commit_default":          "delete",
				"on_give_up_default":         "release_to_head",
				"visibility_timeout_seconds": 300,
			},
			wantError: true,
		},
		{
			name: "bad on_commit_default",
			cfg: map[string]any{
				"backend":                    "postgres",
				"items_table":                "factory_items",
				"on_commit_default":          "bogus",
				"on_give_up_default":         "release_to_head",
				"visibility_timeout_seconds": 300,
			},
			wantError: true,
		},
		{
			name: "bad on_give_up_default",
			cfg: map[string]any{
				"backend":                    "postgres",
				"items_table":                "factory_items",
				"on_commit_default":          "delete",
				"on_give_up_default":         "bogus",
				"visibility_timeout_seconds": 300,
			},
			wantError: true,
		},
		{
			name: "missing visibility_timeout_seconds",
			cfg: map[string]any{
				"backend":            "postgres",
				"items_table":        "factory_items",
				"on_commit_default":  "delete",
				"on_give_up_default": "release_to_head",
			},
			wantError: true,
		},
		{
			name: "negative visibility_timeout_seconds",
			cfg: map[string]any{
				"backend":                    "postgres",
				"items_table":                "factory_items",
				"on_commit_default":          "delete",
				"on_give_up_default":         "release_to_head",
				"visibility_timeout_seconds": -1,
			},
			wantError: true,
		},
		{
			name: "fractional visibility_timeout_seconds",
			cfg: map[string]any{
				"backend":                    "postgres",
				"items_table":                "factory_items",
				"on_commit_default":          "delete",
				"on_give_up_default":         "release_to_head",
				"visibility_timeout_seconds": 1.5,
			},
			wantError: true,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			s, err := Factory{Pool: pool}.Build("inbound", tc.cfg)
			if tc.wantError {
				if err == nil {
					t.Fatalf("expected error, got nil; store=%v", s)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			cs, ok := s.(*Store)
			if !ok {
				t.Fatalf("Build returned %T, want *Store", s)
			}
			if cs.Name() != "inbound" {
				t.Fatalf("Name() = %q, want %q", cs.Name(), "inbound")
			}
			if cs.Kind() != "claim_store" {
				t.Fatalf("Kind() = %q, want %q", cs.Kind(), "claim_store")
			}
			if cs.ItemsTable() != "factory_items" {
				t.Fatalf("ItemsTable() = %q, want %q", cs.ItemsTable(), "factory_items")
			}
			if cs.OnCommitDefault() != "delete" {
				t.Fatalf("OnCommitDefault() = %q, want %q", cs.OnCommitDefault(), "delete")
			}
			if cs.OnGiveUpDefault() != "release_to_head" {
				t.Fatalf("OnGiveUpDefault() = %q, want %q", cs.OnGiveUpDefault(), "release_to_head")
			}
			if cs.VisibilityTimeout() != 300*time.Second {
				t.Fatalf("VisibilityTimeout() = %v, want %v", cs.VisibilityTimeout(), 300*time.Second)
			}
			caps := cs.Capabilities()
			if !caps.SupportsClaim || !caps.SupportsDiscard || !caps.SupportsResume {
				t.Fatalf("Capabilities = %+v, want SupportsClaim+Discard+Resume = true", caps)
			}
			if caps.SupportsRegionLock || caps.SupportsRestore {
				t.Fatalf("Capabilities = %+v, want SupportsRegionLock+Restore = false", caps)
			}
		})
	}
}

// TestFactory_NoPool ensures Build fails fast when constructed without a
// pool — there's no graceful fallback in v1.
func TestFactory_NoPool(t *testing.T) {
	t.Parallel()
	_, err := Factory{}.Build("x", map[string]any{
		"backend":                    "postgres",
		"items_table":                "factory_items",
		"on_commit_default":          "delete",
		"on_give_up_default":         "release_to_head",
		"visibility_timeout_seconds": 300,
	})
	if err == nil {
		t.Fatalf("expected error from pool-less factory")
	}
}

// TestFactory_BadColumnShape covers the schema-verify failure path: the
// table exists but a required column is missing or the wrong type.
func TestFactory_BadColumnShape(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool, teardown := pgtest.StartPostgres(ctx, t)
	t.Cleanup(teardown)

	// Wrong shape: payload is TEXT instead of JSONB.
	if _, err := pool.Exec(ctx, `CREATE TABLE bad_payload_items (
		item_id     UUID PRIMARY KEY,
		payload     TEXT NOT NULL,
		enqueued_at TIMESTAMPTZ NOT NULL DEFAULT now(),
		state       TEXT NOT NULL DEFAULT 'available',
		claim_token UUID,
		claimed_at  TIMESTAMPTZ
	)`); err != nil {
		t.Fatalf("create bad_payload_items: %v", err)
	}
	if _, err := (Factory{Pool: pool}).Build("inbound", map[string]any{
		"backend":                    "postgres",
		"items_table":                "bad_payload_items",
		"on_commit_default":          "delete",
		"on_give_up_default":         "release_to_head",
		"visibility_timeout_seconds": 300,
	}); err == nil {
		t.Fatalf("expected error for non-JSONB payload column")
	}

	// Missing column: no claim_token.
	if _, err := pool.Exec(ctx, `CREATE TABLE missing_col_items (
		item_id     UUID PRIMARY KEY,
		payload     JSONB NOT NULL,
		enqueued_at TIMESTAMPTZ NOT NULL DEFAULT now(),
		state       TEXT NOT NULL DEFAULT 'available',
		claimed_at  TIMESTAMPTZ
	)`); err != nil {
		t.Fatalf("create missing_col_items: %v", err)
	}
	if _, err := (Factory{Pool: pool}).Build("inbound", map[string]any{
		"backend":                    "postgres",
		"items_table":                "missing_col_items",
		"on_commit_default":          "delete",
		"on_give_up_default":         "release_to_head",
		"visibility_timeout_seconds": 300,
	}); err == nil {
		t.Fatalf("expected error for missing claim_token column")
	}
}

// mustCreateItemsTable creates the §9.10 items-table shape under the given
// name. Used by all tests that need a real table to verify against.
func mustCreateItemsTable(t *testing.T, pool *pgxpool.Pool, name string) {
	t.Helper()
	q := `CREATE TABLE ` + name + ` (
		item_id     UUID PRIMARY KEY,
		payload     JSONB NOT NULL,
		enqueued_at TIMESTAMPTZ NOT NULL DEFAULT now(),
		state       TEXT NOT NULL DEFAULT 'available',
		claim_token UUID,
		claimed_at  TIMESTAMPTZ
	)`
	if _, err := pool.Exec(context.Background(), q); err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
}
