# CLAUDE.md (crimefinder)

## What this is

Crimefinder is a code-review tool that consumes rimsky's orchestration
primitives. It ships a custom executor, a custom claim-producer, template
YAML, and a thin CLI wrapper. It does not modify rimsky itself.

## Where to look first

- Architecture and contracts: `.ok-planner/specs/2026-05-19-crimefinder-design.md`.
- Cold-read style: `../cold-read/cold-read-cheatsheet.md` (inherited from
  rimsky root; crimefinder follows the same conventions).
- Prototype lineage: `/Users/patrick/Documents/projects/research/crimefinder/`
  is the original sketch; ported code carries `@source:` annotations.
- Workspace layout:
  - `shared/` — types shared between producer + executor + CLI (zod schemas,
    JSONL row shapes, gate I/O, error classes, scope addresses, named events).
  - `producer/` — containerized gRPC service; ClaimProducer + CrimefinderState
    on one listener. Owns JSONL append, git ops, zone partitioning, atomic
    commit-fix, class-5b auto-routing, recovery scan.
  - `executor/` — host-process gRPC service; spawns Claude CLI per dispatch,
    hosts an internal MCP server, exposes the nine `review_*` gates.
  - `cli/` — thin host wrapper over rimsky's control-api (`crimefinder pass`,
    `crimefinder status`, `crimefinder up`, `crimefinder down`).
  - `templates/` — `code-review-pass.yml` plus the per-mission prompts under
    `templates/prompts/`. The CLI registers via `rimsky template register`.
  - `deploy/` — Dockerfile + compose / rimsky.yml fragments consumers merge in.
  - `test/scenarios/` — vitest scenarios driving the producer surface via
    direct gate calls; runs in-process for speed. The `e2e/smoke.test.ts`
    is gated behind `CRIMEFINDER_E2E=1` and exercises the real Claude CLI.
  - `test/integration/` — vitest scenarios that spawn the real rimsky
    stack as host subprocesses (sqlite-backed) plus crimefinder-producer
    and stub-mode crimefinder-executor. Exercises the wire surface
    (template parser, control-api, gRPC Capabilities handshake) that
    the in-process scenarios skip. Run via
    `npm run test:integration` from `apps/crimefinder/` — NOT included
    in the default `npm test`. Requires `bin/rimsky-{migrate,
    control-api,scheduler,supervisor,rimsky}` to be built first (from
    the rimsky repo root: `for n in rimsky-migrate rimsky-control-api
    rimsky-scheduler rimsky-supervisor rimsky; do go build -o bin/$n
    ./cmd/$n; done`); the harness fails fast with the build command
    in the error message when binaries are missing.

## After Code Changes

1. Run `npm run typecheck && npm test` from `apps/crimefinder/`.
2. Update `apps/crimefinder/CHANGELOG.md`.
3. Update `apps/crimefinder/feature-index.md` if a feature was added or
   removed.
4. Update `@source:` annotations if you modified prototype-ported code.
