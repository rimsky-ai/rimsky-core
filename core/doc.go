// Package core is the rimsky orchestrator module root. Sub-packages
// implement the node-graph orchestration primitives, storage,
// dispatch queue, scheduler, supervisor, control API, and library
// entry points.
//
// # Architecture overview
//
// Rimsky is a node-graph orchestration platform. Nodes communicate
// via two messages (invalidate, recalculate), interact with stores via
// claims and named locks (the two primitives — see docs/glossary.md),
// and execute work through external executor services. The store
// interface is four verbs: Open / Commit / Abandon / Release
// (spec §4.1).
//
// Sub-packages follow a feature-first layout:
//
//   - cmd/        — reference env-var binaries (rimsky-scheduler,
//     rimsky-supervisor, rimsky-control-api,
//     rimsky-migrate, rimsky-conformance).
//   - config/     — public library entry points: StartScheduler,
//     StartSupervisor, StartControlAPI. Import this
//     package to embed rimsky.
//   - controlapi/ — HTTP+JSON control API.
//   - executor/   — supervisor-side gRPC + HTTP-bridge client +
//     name→endpoint resolver.
//   - frame/      — frame-resolution model (per
//     docs/specs/2026-04-26-frame-resolution-design.md).
//   - message/    — message type definitions (invalidate, recalculate).
//   - migrations/ — embedded SQL + session-advisory-lock migration
//     runner.
//   - node/       — node template types, template validator, state
//     machine, policy evaluator, backoff, holding-
//     subgraph computation.
//   - qualityrule/— builtin quality-rule evaluators.
//   - queue/      — dispatch queue interface + Postgres implementation.
//   - scheduler/  — scheduler loop: advisory-lock-guarded tick,
//     schedule processor, pure-cascade sweep, stale-
//     heartbeat sweep, orphan reap, visibility-timeout
//     sweep over each store's pick policies.
//   - shared/     — cross-package types, sentinel errors, Clock,
//     Logger.
//   - storage/    — StorageBackend + sub-store interfaces; Postgres
//     implementations.
//   - store/      — Store interface (4 verbs) + value types
//     (ClaimSpec, NamedLockSpec, ClaimResult,
//     Capabilities, WriteSemantics, Intent),
//     ModeCoexists helper, Registry, LockHoldersClient
//     helpers. Subpackages: remote/ (gRPC client; the
//     only concrete Store in the rimsky module),
//     storetest/ (in-Go fake).
//   - supervisor/ — supervisor main loop + runner + atomic
//     acquisition (§7.3) + auto-terminal mechanism
//     (§4.10 invariant 13) + release flow (§7.6) + async callback
//     server.
//
// The module has no cross-package cycles; the import DAG is enforced
// by the package-rules in CLAUDE.md.
package core
