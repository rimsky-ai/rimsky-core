// Package stub implements an in-process Store used by the migrated
// scenario tests in `test/scenarios/*` (spec §16.1). It satisfies
// store.Store, store.ClaimableStore, and store.ResumableStore against
// purely in-memory state so scenario tests can exercise runner /
// state-machine semantics without standing up a real filesystem store
// or a real postgres-backed claim store.
//
// Two well-known kinds are supported:
//
//   - "stub_filesystem" — region-lock semantics on a string-set region
//     grammar; no claim support. Mirrors the filesystem store's
//     LockEligible / RegionsConflict surface for scenario tests that
//     want a filesystem-shaped store without touching the real disk.
//
//   - "stub_claim_store" — claim semantics on an in-memory FIFO queue
//     of items; no region-lock support. Mirrors the claim-store-
//     postgres surface for tests that want claim-pick / release behaviour
//     without spinning up testcontainers.
//
// Capability flags are configurable via cfg so scenario tests can mock
// odd combinations (e.g. claim store with discard disabled). The defaults
// match the production-equivalent stores.
//
// Imports stdlib + `core/store/` only. No pgx dependency: the supervisor
// passes a *pgx.Tx via store.WithTx for uniformity with real stores,
// but the stub does not read it.
package stub

import (
	"fmt"

	"github.com/fallguy/rimsky/core/store"
)

// KindFilesystem is the canonical kind string for the filesystem-shaped
// stub. Use this when registering a Factory with a *store.Registry.
const KindFilesystem = "stub_filesystem"

// KindClaimStore is the canonical kind string for the claim-store-shaped
// stub. Use this when registering a Factory with a *store.Registry.
const KindClaimStore = "stub_claim_store"

// Factory builds *Store instances for one of the two stub kinds. Two
// factories are registered separately (one per kind) so the registry's
// per-kind dispatch from spec §8.1 still works; instantiate with the
// appropriate kind.
//
// Factory is value-typed and stateless: the configured kind is fixed at
// construction so that Kind() is well-defined.
type Factory struct {
	StubKind string // KindFilesystem | KindClaimStore
}

// FilesystemFactory returns a Factory that builds region-lock-capable
// stub stores. Equivalent to Factory{StubKind: KindFilesystem}.
func FilesystemFactory() Factory { return Factory{StubKind: KindFilesystem} }

// ClaimStoreFactory returns a Factory that builds claim-capable stub
// stores. Equivalent to Factory{StubKind: KindClaimStore}.
func ClaimStoreFactory() Factory { return Factory{StubKind: KindClaimStore} }

// Kind returns the stub kind this factory builds.
func (f Factory) Kind() string { return f.StubKind }

// Build constructs a *Store with the kind-appropriate defaults. Optional
// cfg keys (all may be omitted; types must match if present):
//
//   - supports_region_lock (bool) — override default for region-lock
//     capability; defaults true for KindFilesystem, false otherwise.
//   - supports_claim (bool) — override default for claim capability;
//     defaults true for KindClaimStore, false otherwise.
//   - supports_discard (bool) — override default for discard; defaults
//     true for KindClaimStore, false otherwise.
//   - supports_resume (bool) — override default for resume; defaults
//     true for both kinds (mirrors production behaviour).
//   - supports_restore (bool) — override default for restore; defaults
//     false (post-v1 feature).
//   - initial_items ([]any) — KindClaimStore only: payloads to seed the
//     in-memory FIFO queue with. Each entry becomes one claimable item
//     with an auto-generated claim ID. Ignored for KindFilesystem.
//
// Returns an error on unknown kind or wrong-type cfg values.
func (f Factory) Build(name string, cfg map[string]any) (store.Store, error) {
	switch f.StubKind {
	case KindFilesystem:
	case KindClaimStore:
	default:
		return nil, fmt.Errorf("stub store %q: unknown kind %q (want %q or %q)",
			name, f.StubKind, KindFilesystem, KindClaimStore)
	}

	caps := defaultCapabilities(f.StubKind)
	if err := readBoolOverride(cfg, "supports_region_lock", &caps.SupportsRegionLock, name); err != nil {
		return nil, err
	}
	if err := readBoolOverride(cfg, "supports_claim", &caps.SupportsClaim, name); err != nil {
		return nil, err
	}
	if err := readBoolOverride(cfg, "supports_discard", &caps.SupportsDiscard, name); err != nil {
		return nil, err
	}
	if err := readBoolOverride(cfg, "supports_resume", &caps.SupportsResume, name); err != nil {
		return nil, err
	}
	if err := readBoolOverride(cfg, "supports_restore", &caps.SupportsRestore, name); err != nil {
		return nil, err
	}

	s := newStore(name, f.StubKind, caps)

	if f.StubKind == KindClaimStore {
		if err := seedItems(s, cfg, name); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// defaultCapabilities returns the default capability flags for the given
// stub kind. Tests typically take these as-is; the cfg overrides exist
// for the rare scenario test that needs to flip a flag.
func defaultCapabilities(kind string) store.Capabilities {
	switch kind {
	case KindFilesystem:
		return store.Capabilities{
			SupportsRegionLock: true,
			SupportsClaim:      false,
			SupportsDiscard:    false,
			SupportsResume:     true,
			SupportsRestore:    false,
		}
	case KindClaimStore:
		return store.Capabilities{
			SupportsRegionLock: false,
			SupportsClaim:      true,
			SupportsDiscard:    true,
			SupportsResume:     true,
			SupportsRestore:    false,
		}
	}
	return store.Capabilities{}
}

// readBoolOverride pulls an optional bool field out of cfg into *dst. If
// the key is missing the default is preserved; if the key is present but
// not a bool, an error is returned.
func readBoolOverride(cfg map[string]any, key string, dst *bool, storeName string) error {
	raw, ok := cfg[key]
	if !ok {
		return nil
	}
	v, ok := raw.(bool)
	if !ok {
		return fmt.Errorf("stub store %q: %q must be bool, got %T", storeName, key, raw)
	}
	*dst = v
	return nil
}

// seedItems consumes the optional "initial_items" slice from cfg (for
// claim-store-kind stubs) and pushes each entry onto the in-memory queue.
func seedItems(s *Store, cfg map[string]any, storeName string) error {
	raw, ok := cfg["initial_items"]
	if !ok {
		return nil
	}
	items, ok := raw.([]any)
	if !ok {
		return fmt.Errorf("stub store %q: initial_items must be []any, got %T", storeName, raw)
	}
	for _, payload := range items {
		s.SeedItem(payload)
	}
	return nil
}
