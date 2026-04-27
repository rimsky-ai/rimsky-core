// Package store defines the rimsky store abstraction (spec §11; see also
// docs/glossary.md). A store is a deployment-level data backend whose
// lock state is tracked centrally in postgres while its data semantics
// remain native to the underlying primitive (filesystem path, postgres
// table, S3 bucket, etc.).
//
// This package owns interfaces, value types, the factory/registry, the
// transaction-context helpers used to thread the supervisor's outer
// pgx.Tx through store mutations, the rimsky_lock_holders postgres
// helpers, and the pure ModeCoexists predicate (spec §8.5). Concrete
// implementations live in subpackages (`core/store/filesystem/`,
// `core/store/postgres/`, `core/store/stub/`).
//
// # Two primitives (spec §5.1)
//
//   - Claim — substrate-bound. ClaimSpec carries (StoreName, Selector,
//     Intent, Alias). Substrate parses Selector and decides what it
//     means (regional access vs. configured pick policy).
//
//   - Named lock — non-substrate. NamedLockSpec carries (Name) only.
//     Limit lives in operator config (§15.2).
//
// Both are persisted as rows in `rimsky_lock_holders` (spec §12.10) but
// the two specs are distinct types with no common interface.
//
// # Five protocol verbs (spec §6 / §11.5)
//
//   - Open(ctx, ClaimSpec) → ClaimResult
//   - Commit(ctx, region, address, policyOverride)
//   - Abandon(ctx, region, address, policyOverride)
//   - Delete(ctx, region)
//   - Release(ctx, region, address)
//
// # write_semantics (spec §8)
//
// Per-store config: direct | staged_blocking | staged_async. Determines
// (a) whether reads can dispatch concurrently with writes on the same
// region, and (b) whether the supervisor calls staging-related verbs.
// Operator-configured; bounded above by the store kind's
// MaxWriteSemantics().
//
// # Package layout (spec §11.1)
//
// `core/store/` imports `core/shared/` and `pgx/v5` (the latter only
// for the transaction-context helpers and lock-holder helpers; pgx.Tx
// is the only pgx symbol leaked through this package's public surface).
package store
