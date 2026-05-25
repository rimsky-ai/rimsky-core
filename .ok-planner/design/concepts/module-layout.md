---
concept: module-layout
status: as-is
aliases:
  - workspace-layout
references:
  - _discover/2026-05-10-three-go-module-split.md
  - _discover/2026-05-10-depguard-enforced-package-boundaries.md
  - _discover/2026-05-10-stdlib-slog-and-minimal-deps.md
  - _discover/licensing-boundary-map.md
---

# Module layout

## What it is

`go.work` ties three Go modules into one workspace plus the MCP-server module. The root module itself has a four-way split (`graph/` + `runtime/` + `control/` + supporting dirs) under the 2026-05-13 four-layer restructure (post-2026-05-12 nomenclature resolution):

- **`protocols/`** (`github.com/fallguyconsulting/rimsky/protocols`) — Go interfaces + protobuf bindings for `ClaimProducer`, `Executor`, `LifecycleSubscriber`. Stdlib + grpc + protobuf + uuid only.
- **`foundation/`** (`github.com/fallguyconsulting/rimsky/foundation`) — primitives only. Cascade engine, claim/lock primitives, persistence drivers, infrastructure shared types (`foundation/shared/`: `Clock`, `Logger`, `UUID`, `DeepMergeJSON`), and state-machine enums (`foundation/cascade/`: `NodeState`, `LastOutcome`, `ErrIllegalTransition`). Depends on `protocols` + pgx + uuid + modernc.org/sqlite. `foundation/go.mod` is self-contained — no `replace` directive against the root module.
- **Root** (`github.com/fallguyconsulting/rimsky`) — graph layer (`graph/`) + runtime layer (`runtime/`) + control layer (`control/`) + cmd binaries (`cmd/`) + bundled stores (`stores/`) + bundled executors (`executors/`) + bundled sensors (`sensors/`) + bundled lifecycle-subscribers (`subscribers/`) + reference examples (`examples/`) + dashboards (`dashboards/`). Pulls heavier libs (jsonschema, robfig/cron, jcs, testcontainers). The shared pgtest fixture lives at `internal/pgtest/`.
  - `graph/` — cascade model: templates, instances, frames, attributes, quality rules, scheduler, scenario harness. Imports `foundation` + `protocols`.
  - `runtime/` — bridge layer: supervisor runner, conductor, sweeps, orphan reapers, auto-terminal, terminal-decision engine, callback server, `remote/` (gRPC client to ClaimProducer impls), `executor/` (executor gRPC client pool). Imports `foundation` + `graph` + `protocols`.
  - `control/` — operator surfaces: `controlapi/`, `cli/`, `observability/`, `config/`. Imports `runtime` + `graph` + `foundation` + `protocols`.
- **`mcp-servers/control-api/`** (`github.com/fallguyconsulting/rimsky/mcp-servers/control-api`) — separate Go module for the operator MCP shim.

Layer ordering: `foundation/` → `graph/` → `runtime/` → `control/`. `control/` reads everything below it; `graph/` never reads `runtime/` or `control/` (one-way, enforced by depguard `graph-purity`); `runtime/` never reads `control/` (`runtime-purity`); `foundation/` never reads `graph/`/`runtime/`/`control/` (`foundation-purity`).

One documented residual (per-file depguard exemption; flagged for separate follow-up):

- `graph/scheduler/{scheduler,pure_cascade}.go` imports `runtime/` for the sweep entry points the scheduler tick orchestrates.

The previously-documented foundation → graph back-import (`foundation/persistence/{,postgres/,sqlite/,conformance/}` importing `graph/node` for `TemplateSpec`/`NodeSpec` row types) was eliminated in the 2026-05-13 back-import cleanup cycle: the persistable row-type primitives moved into a new `foundation/spec/` package; the per-file depguard exemptions are retired; `foundation-purity` applies unconditionally.

## Purpose

Layered import-budget discipline. An external implementer of `ClaimProducer` imports only `protocols/`. The root module pulls heavier libraries that those implementers never see transitively. The four-layer split inside the root module isolates the bridge concerns (supervisor + sweeps + remote clients) in `runtime/` so the cascade-model code in `graph/` stays a clean dependency target.

## Boundaries

Owns: per-module `go.mod`, `go.work`, depguard lint rules, the alias pattern (`foundation/locks/` aliases protocols types), the four-layer ordering inside the root module. Does NOT own: package-internal layout (that's per-feature), proto wire content (lives in `protocols/proto/v1/`). Adjacent: `persistence-database`, `claim-producer`, `executor`, `lifecycle-subscriber`.

## Invariants

- depguard `pgx-isolation` denies pgx imports outside an allow-list.
- depguard `foundation-internal-isolation` denies imports of `foundation/internal/` from outside `foundation/`.
- depguard `foundation-purity` denies imports of `graph/`, `runtime/`, `control/`, `cmd/`, `stores/`, `executors/`, `dashboards/` from `foundation/`. Applies unconditionally — there are no per-file exemptions.
- depguard `graph-purity` denies imports of `runtime/`, `control/`, `cmd/`, `stores/`, `executors/`, `dashboards/` from `graph/`. Per-file exemption for `graph/scheduler/{scheduler,pure_cascade}.go` → `runtime/`. `graph/scenario/` fully exempt (boots the full stack).
- depguard `runtime-purity` denies imports of `control/`, `cmd/`, `stores/`, `executors/`, `dashboards/` from `runtime/`.
- (Retired: `graph-control-isolation`; subsumed by `graph-purity`.)
- Logging is stdlib `log/slog` only; HTTP routing is `go-chi/chi`; Postgres is `pgx/v5`; SQLite is `modernc.org/sqlite` (pure-Go, no CGO); cron is `robfig/cron/v3`.
- The three runtime processes (scheduler, supervisor, control-api) never import each other; cross-process state flows through Postgres only.

## Aliases and historical names

Pre-layer-crystallization (`.ok-planner/specs/2026-05-04-layer-crystallization-design.md`), the codebase was a single Go module. Between the 2026-05-12 nomenclature resolution and the 2026-05-13 four-layer restructure, the root module had a two-way `graph/` + `control/` split with `foundation/integration/` (which imported back into the root via a `replace` directive) carrying the supervisor and sweeps; that directory was moved to `runtime/` at the root module, and `graph/executor/` was moved to `runtime/executor/`. `graph/shared/` retained graph-specific types (`Severity`, `BackoffKind`, `JitterKind`, `AccessKind`); the infrastructure primitives (`Clock`, `Logger`, `UUID`, `DeepMergeJSON`) moved to `foundation/shared/` and the state-machine enums (`NodeState`, `LastOutcome`, `ErrIllegalTransition`) moved to `foundation/cascade/`.

## Licensing boundary

Per-directory Apache-2.0-vs-AGPL-3.0 mapping in `licensing.yml`, enforced by `rimsky-license-check` with longest-prefix-match-wins. Apache surface covers protocols, foundation, graph (excluding `eval/`), runtime, control, CLI binaries; AGPL surface covers `graph/qualityrule/eval/` and any directories explicitly mapped under AGPL. Repo-organization concern; not a runtime noun. The check is build-step enforcement, not runtime.

(Adjacent: previously documented as a standalone concept; folded here under `2026-05-11-design-log-convergence`.)

## Open within this concept

(no specific live tensions distinct from `persistence-database`)

## Notes

- **2026-05-13: four-layer restructure.** Split the root module into `graph/` → `runtime/` → `control/` ordering. `foundation/integration/` → `runtime/`; `graph/executor/` → `runtime/executor/`. `foundation/go.mod` lost its `replace` against the root module — foundation is self-contained except for one documented residual (`foundation/persistence` → `graph/node` for row types) allowed via per-file depguard exemption. New depguard rules: `foundation-purity`, `graph-purity`, `runtime-purity`. Retired: `graph-control-isolation` (subsumed by `graph-purity`).
- **2026-05-13: foundation → graph back-import eliminated.** The persistable row-type primitives (`TemplateSpec`, `TemplateNodeDef`, `EvaluatorState`, `ErrorTypePolicy`, `QualityRuleSpec`, `Severity`, `BackoffKind`, `JitterKind`, frame-resolution + resolve constants, etc.) moved from `graph/node/` into a new `foundation/spec/` package. The graph algorithms that operate on these types (`Evaluate`, `HoldingSubgraphsForTemplate`, `ValidateTemplate`, `RequiredStores`) remain in `graph/node/`; `graph/node`, `graph/shared`, and `graph/qualityrule` keep type-aliases pointing at `foundation/spec` for backward compatibility. Foundation is now fully self-contained (`cd foundation && go mod tidy` is clean); `foundation-purity` applies unconditionally.
- **2026-05-15: bundled-deliverables expansion.** Three new top-level directories sit at the same level as `stores/` / `executors/` / `dashboards/`: `sensors/` (bundled `Sensor`-protocol reference impls: `sensor-cron`, `sensor-http`, `sensor-object-store`, `sensor-webhook`), `subscribers/` (bundled `LifecycleSubscriber`-protocol reference impls: `openlineage`), and `examples/` (reference impls demonstrating patterns, e.g. `atomic-staging-fs-producer/`). Each is consumption-side (binary or example), consumes `foundation/` + `protocols/` + the root module via go.work but is not imported back into the layered packages. `pgx-isolation` depguard rule extended to allow pgx in `sensors/` and `subscribers/` (sensor-cron state DB, openlineage cursor state DB). Also retired: `graph/qualityrule/` (replaced by verifier-executor pattern), the per-node `schedule:` template field (replaced by `sensor-cron`), and the `rimsky_schedules` table.
