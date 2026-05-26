---
concept: sdk
status: as-is
aliases:
  - rimsky-sdk
  - sdk/go
---

# SDK

## What it is

`pkg:github.com/fallguyconsulting/rimsky/sdk/go` is the canonical Go-side implementer-facing surface for building services that rimsky talks to. A peer Go module within the rimsky repo, alongside `pkg:protocols/` and `pkg:foundation/`. Houses:

- Server scaffolding for claim-producer / executor / lifecycle-subscriber / blob-backend / publisher protocols
- Publisher-side helpers (message-emit retry+backoff, idempotency-key header, callback POST handling)
- Conformance library (`pkg:sdk/go/conformance`) — invocable from service authors' Go tests in addition to the thin CLI wrappers in `pkg:cmd/rimsky-*-conformance`
- Testcontainer helpers (`pkg:sdk/go/testpg`) — plain Postgres (`StartFreshPostgresDSN`) for services testing their own state-DB schema. (Migrations-applying variants stay rimsky-internal at `pkg:foundation/internal/pgtest` and `pkg:internal/pgmigrate`; they can't sit in the SDK because they import `pkg:foundation/persistence`, which `sdk-purity` forbids.)
- Ops glue — `slog` setup, healthcheck HTTP endpoint, DSN env-var parser

## Purpose

Remove footguns from third-party and bundled service authors (canonical example: the TS `kind`-vs-`type` body-key bug documented in `file:CLAUDE.md`'s gotcha list). Provide one paved path to "implement a service rimsky calls."

## Boundaries

Owns: the implementer-facing surface listed above. Does NOT own: the calling-side wire code (rimsky-internal infrastructure tightly coupled to `concept:supervisor`, `concept:terminal-resolution`, `concept:discovery-cache` — stays in rimsky's `pkg:runtime/peer`). Does NOT own: non-Go languages (a future `pkg:sdk/ts` would be a separate concept if/when it lands).

## Invariants

- `sdk-purity` depguard rule: `pkg:sdk/go` imports only `pkg:protocols/` + stdlib + minimal third-party. No imports from `foundation/`, `graph/`, `runtime/`, `control/`, or `cmd/`.
- Lockstep tagging with rimsky-core: root module tagged `v0.X.0`, sub-module tagged `sdk/go/v0.X.0`, both cut by the same release script.
- Break-freely pre-v1 license per `file:.claude/rules/rules.md`. No deprecation-alias discipline; CHANGELOG entries are the visibility surface for breaks.

## Aliases and historical names

`rimsky-sdk` informally; `sdk/go` in path-form. Created in this reorganization (spec `2026-05-24-repo-reorganization-design`).

## Notes

- 2026-05-24: created as part of the repo reorganization. SDK birth covered in spec `2026-05-24-repo-reorganization-design` phase P2.
- 2026-05-24: `pkg:stores/common/action` (cross-implementer claim-producer action vocabulary) promoted into the SDK at `pkg:sdk/go/stores/action` during the P3 bundled-services migration — it's implementer-facing surface, so it belongs on the SDK side of the boundary. Pass 5 ride-along (spec `2026-05-24-repo-reorganization-design`).
