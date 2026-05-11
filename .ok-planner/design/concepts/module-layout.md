---
concept: module-layout
status: as-is
aliases:
  - three-go-modules
references:
  - _discover/2026-05-10-three-go-module-split.md
  - _discover/2026-05-10-depguard-enforced-package-boundaries.md
  - _discover/2026-05-10-stdlib-slog-and-minimal-deps.md
  - _discover/licensing-boundary-map.md
---

# Module layout

## What it is

`go.work` ties three Go modules into one workspace plus the MCP-server module:

- **`protocols/`** (`github.com/fallguy/rimsky/protocols`) — Go interfaces + protobuf bindings for `ClaimProducer`, `Executor`, `LifecycleSubscriber`. Stdlib + grpc + protobuf + uuid only.
- **`foundation/`** (`github.com/fallguy/rimsky/foundation`) — cascade engine, claim/lock primitives, integration runner, persistence drivers. Depends on `protocols` + pgx + uuid + modernc.org/sqlite.
- **Root** (`github.com/fallguy/rimsky`) — modeling layer, cmd binaries, bundled stores, bundled executors. Pulls heavier libs (jsonschema, robfig/cron, jcs, testcontainers).
- **`mcp-servers/control-api/`** (`github.com/fallguy/rimsky/mcp-servers/control-api`) — separate Go module for the operator MCP shim.

## Purpose

Layered import-budget discipline. An external implementer of `ClaimProducer` imports only `protocols/`. The root module pulls heavier libraries that those implementers never see transitively.

## Boundaries

Owns: per-module `go.mod`, `go.work`, depguard lint rules, the alias pattern (`foundation/locks/` aliases protocols types). Does NOT own: package-internal layout (that's per-feature), proto wire content (lives in `protocols/proto/v1/`). Adjacent: `persistence-driver`, `claim-producer`, `executor`, `lifecycle-subscriber`, `licensing-boundary`.

## Invariants

- depguard `pgx-isolation` denies pgx imports outside an allow-list.
- depguard `foundation-internal-isolation` denies imports of `foundation/internal/` from outside `foundation/`.
- Logging is stdlib `log/slog` only; HTTP routing is `go-chi/chi`; Postgres is `pgx/v5`; SQLite is `modernc.org/sqlite` (pure-Go, no CGO); cron is `robfig/cron/v3`.
- The three runtime processes (scheduler, supervisor, control-api) never import each other; cross-process state flows through Postgres only.

## Aliases and historical names

Pre-layer-crystallization (`.ok-planner/specs/2026-05-04-layer-crystallization-design.md`), the codebase was a single Go module.

## Open within this concept

(no specific live tensions distinct from `persistence-driver` and `licensing-boundary`)

