// Package core is the rimsky orchestrator module root. Sub-packages implement
// the node-graph orchestration primitives, storage, dispatch queue, scheduler,
// supervisor, control API, and library entry points.
//
// # Architecture overview
//
// Rimsky is a cell-graph (node-graph) orchestration platform. Nodes
// communicate via two messages (invalidate, recalculate), operate on
// versioned resources, and execute work through external executor services.
//
// Sub-packages follow a feature-first layout enforced by import-graph rules
// (see ../docs/specs/2026-04-23-rimsky-go-port-design.md §4.1):
//
//   - cmd/       — reference env-var binaries (rimsky-scheduler, rimsky-
//                  supervisor, rimsky-control-api, rimsky-migrate).
//   - config/    — public library entry points: StartScheduler, StartSupervisor,
//                  StartControlAPI. Import this package to embed rimsky.
//   - controlapi/— HTTP+JSON control API (templates, instances, nodes, events,
//                  resources, health).
//   - executor/  — supervisor-side gRPC + HTTP-bridge client + name→endpoint
//                  resolver.
//   - message/   — message type definitions (invalidate, recalculate).
//   - migrations/— embedded SQL + session-advisory-lock migration runner.
//   - node/      — node template types, template validator, state machine
//                  (with blessed-invariant: no same-state short-circuit),
//                  policy evaluator, backoff.
//   - qualityrule/— builtin quality-rule evaluators.
//   - queue/     — dispatch queue interface + Postgres implementation (with
//                  blessed-invariant: tag counts from dispatch rows).
//   - resource/  — Resource + Factory interfaces; inline-jsonb reference impl.
//   - scheduler/ — scheduler loop: advisory-lock-guarded tick, schedule
//                  processor, pure-cascade sweep, stale-heartbeat sweep,
//                  orphaned-claim sweep, ready sweep.
//   - shared/    — cross-package types, sentinel errors, Clock, Logger.
//   - storage/   — StorageBackend + 8 sub-store interfaces; Postgres
//                  implementations.
//   - supervisor/— supervisor main loop + runner + commit flow + on_error
//                  + terminal_outcome + async callback server.
//
// The module has no cross-package cycles; the import DAG is documented in
// the design spec.
package core
