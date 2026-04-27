// Package store defines the rimsky store abstraction: a deployment-level
// data backend whose lock state is tracked centrally in postgres while its
// data semantics remain native to the underlying primitive (filesystem path,
// claim-store row, etc.).
//
// This package owns interfaces, value types, the factory/registry, and the
// transaction-context helpers used to thread the supervisor's outer pgx.Tx
// through store mutations. Concrete implementations live in subpackages
// (`core/store/filesystem/`, `core/store/claimstorepg/`).
//
// # Vocabulary (spec §5.1–§5.6)
//
//   - Store (§5.1)   — a deployment-level data backend, configured once in
//     YAML and built into a per-process Registry. There is no
//     `rimsky_stores` database table; templates reference stores by name.
//
//   - Region (§5.2) — a portion of a store's namespace. v1 region grammars
//     are filesystem path-globs and per-claim implicit regions for
//     claim-stores.
//
//   - Lock (§5.3) — a node's exclusivity claim on a named scope or a region
//     within a store, held for the duration of one execution. All lock state
//     lives in postgres (`rimsky_lock_holders`); stores never persist lock
//     state. Stores may persist *data* state (e.g. `claim-store-postgres`
//     flips an items-table row to `in_progress`), but the question "is anyone
//     holding lock X" is answered exclusively by `rimsky_lock_holders`.
//
//   - Handle (§5.4) — a native-shape reference to the locked region(s) or
//     claim payload, passed to the executor. The executor sees the underlying
//     system in its native form — there is no rimsky-store API at the
//     executor boundary.
//
//   - Sidecar (§5.5) — a per-lock private workspace used by sidecar/versioned
//     modes. Direct-mode stores have no sidecar; the handle points at the
//     live region.
//
//   - Claim (§5.6) — the store-picks-region variant of lock acquisition. The
//     caller asks the store to pick a region from its eligible pool; the
//     store locks it and reports the choice. Multi-claim is supported.
//     Claim-and-forget is the default; opt-in `hold: true` anchors a claim
//     across a downstream pipeline (resolution algorithm in spec §5.6.4).
//
// # Modes (spec §6)
//
// A store declares one of three modes at deployment time. v1 ships only
// `direct`. `sidecar` and `versioned` are post-v1.
//
// # Package layout (spec §8.1)
//
// `core/store/` imports `core/shared/` and `pgx/v5` (the latter only for the
// transaction-context helpers; `pgx.Tx` is the only pgx symbol leaked through
// this package's public surface).
package store
