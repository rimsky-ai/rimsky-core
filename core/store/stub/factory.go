// Package stub implements an in-process Store used by scenario tests.
// Satisfies store.Store against purely in-memory state so scenario
// tests can exercise runner / state-machine semantics without standing
// up real store infrastructure.
//
// Two well-known kinds are supported:
//
//   - "stub_filesystem" — region-direct semantics; default
//     write_semantics=direct. Mirrors the filesystem store's default
//     surface for tests that want a filesystem-shaped store without
//     touching the real disk.
//
//   - "stub_postgres" — region-direct + optional pick policies; default
//     write_semantics=direct. Mirrors the postgres store's surface for
//     tests that want pick-policy behavior without spinning up
//     testcontainers.
//
// Imports stdlib + `core/store/` only. No pgx dependency.

package stub

import (
	"fmt"

	"github.com/fallguy/rimsky/core/store"
)

// KindFilesystem is the canonical kind string for the filesystem-shaped
// stub.
const KindFilesystem = "stub_filesystem"

// KindPostgres is the canonical kind string for the postgres-shaped
// stub. (Renamed from KindClaimStore — the prior "claim_store" kind
// dissolved per spec §11.1; pick policies are configured per-store.)
const KindPostgres = "stub_postgres"

// Factory builds *Store instances for one of the stub kinds. Two
// factories are registered separately (one per kind) so the registry's
// per-kind dispatch from spec §11.1 still works.
type Factory struct {
	StubKind string // KindFilesystem | KindPostgres
}

// FilesystemFactory returns a Factory that builds filesystem-shaped
// stub stores.
func FilesystemFactory() Factory { return Factory{StubKind: KindFilesystem} }

// PostgresFactory returns a Factory that builds postgres-shaped stub
// stores.
func PostgresFactory() Factory { return Factory{StubKind: KindPostgres} }

// Kind returns the stub kind this factory builds.
func (f Factory) Kind() string { return f.StubKind }

// MaxWriteSemantics returns staged_async — the stub is permissive for
// tests that need to exercise async-mode codepaths (per spec §22:
// staged_async protocol verbs are present but no v1 substrate registers
// state). Per-store config can downgrade.
func (f Factory) MaxWriteSemantics() store.WriteSemantics { return store.WriteSemanticsStagedAsync }

// Build constructs a *Store with kind-appropriate defaults. Optional cfg
// keys (all may be omitted; types must match if present):
//
//   - write_semantics (string) — override default; defaults to "direct".
//   - pick_policies (map[string]any) — keyed by recognized selector form
//     (e.g. "@queue"). Each value is a map with optional fields:
//     "on_commit_default" (string), "on_give_up_default" (string).
//   - initial_items ([]any) — for tests that want to seed pick policies
//     at construction. Each entry is a map with "selector" and "payload"
//     keys; the payload is JSON-encoded.
//
// Returns an error on unknown kind or wrong-type cfg values.
func (f Factory) Build(name string, cfg map[string]any) (store.Store, error) {
	switch f.StubKind {
	case KindFilesystem, KindPostgres:
	default:
		return nil, fmt.Errorf("stub store %q: unknown kind %q (want %q or %q)",
			name, f.StubKind, KindFilesystem, KindPostgres)
	}

	caps := store.Capabilities{WriteSemantics: store.WriteSemanticsDirect}
	if wsRaw, ok := cfg["write_semantics"]; ok {
		wsStr, ok := wsRaw.(string)
		if !ok {
			return nil, fmt.Errorf("stub store %q: write_semantics must be string, got %T", name, wsRaw)
		}
		caps.WriteSemantics = store.WriteSemantics(wsStr)
	}

	s := newStore(name, f.StubKind, caps)

	if pickRaw, ok := cfg["pick_policies"]; ok {
		policies, ok := pickRaw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("stub store %q: pick_policies must be map, got %T", name, pickRaw)
		}
		for selector, polRaw := range policies {
			pol, ok := polRaw.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("stub store %q: pick_policies[%q] must be map, got %T", name, selector, polRaw)
			}
			onCommit, _ := pol["on_commit_default"].(string)
			onGiveUp, _ := pol["on_give_up_default"].(string)
			s.ConfigurePickPolicy(selector, onCommit, onGiveUp)
		}
	}
	return s, nil
}
