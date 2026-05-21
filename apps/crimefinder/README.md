# Crimefinder

Code-review-as-a-rimsky-graph: a separate tool that consumes rimsky's
orchestration primitives to run zone-partitioned, design-doc-aware,
auto-fixing review passes over an arbitrary git repository.

## What it does

- **Partitions** the target repo into logical zones (file groups by
  directory, split/merged to respect a max-files-per-zone cap).
- **Fans out** one review-zone subagent per zone. Each subagent surveys its
  zone with `Read`/`Glob`/`Grep` and emits findings through the
  `review_finding` MCP gate.
- **Classifies** findings 1 (correctness), 2 (security), 3 (perf),
  4 (clarity), 5a (architecture), 5b (the design doc itself may be
  wrong). Class-5b is server-enforced: a class-1-4 finding that cites a
  concept slug without quoting ≥ 8 contiguous tokens from that concept's
  `Boundaries:` or `Invariants:` is auto-routed to 5b.
- **Dedups** within and across batches via a stable fingerprint
  (file + symbol + normalized description; line numbers ignored).
- **Fixes** class-1-4 findings in a bounded fix-cycle: a fix-zone agent
  edits files, runs tests, and commits via `review_commit_fix`. The
  commit transaction is atomic (git add → commit → JSONL status:fixed,
  all under a producer-side mutex; a `Resolves: <finding_id>` footer
  lets startup recovery reconstruct missing JSONL on crash).
- **Persists** findings, status updates, coverage, and pass rows as
  git-tracked JSONL under `.crimefinder/`.

## Quickstart

1. Make sure the rimsky stack is running. Merge the compose + rimsky.yml
   fragments under `deploy/` into your consumer-side files.
2. Build the producer image: `docker build -f apps/crimefinder/deploy/Dockerfile.producer -t crimefinder/producer:latest .`
3. Start the host executor: `cd apps/crimefinder/executor && npm run build && node dist/main.js`.
4. From the repo you want to review:
   - `crimefinder up` (or `docker compose up -d`)
   - `crimefinder pass --repo $(pwd) --mission "convergence pass"`
   - `crimefinder status --repo $(pwd)`

## Config

A consumer-repo's `.crimefinder/config.yml` configures tests, coverage
threshold, partitioning, and the design-docs lookup. See the
`ConfigSchema` in `producer/src/config.ts` for the full surface and
defaults.

## Architecture

Two TypeScript services + a template + a CLI wrapper:

- `producer/` (containerized) implements `rimsky.v1.ClaimProducer` and
  `crimefinder.v1.CrimefinderState` on the same gRPC listener. Owns
  partitioning, JSONL, git, the atomic commit-fix transaction.
- `executor/` (host process) implements `rimsky.v1.Executor`, spawns
  Claude CLI per dispatch, and hosts a loopback MCP server exposing
  nine `review_*` tools. The MCP gates delegate to the producer over
  gRPC.
- `templates/code-review-pass.yml` is the rimsky template the CLI
  registers. It declares the main graph (open-pass → discover-context →
  review-fan-out → aggregate → dedup → class-split → fix-iter-1/2/3 →
  class-5-finalize → report) and the `fix-iteration` sub-graph
  (iter-guard → fix-fan-out → re-review-affected → iter-aggregate).
- `cli/` is a thin wrapper over `rimsky template register` /
  `rimsky instance create`.

## Design source of truth

`.ok-planner/specs/2026-05-19-crimefinder-design.md` is the full spec.
Architectural decisions, named-event vocabulary, the gate error
taxonomy, and the JSONL row shapes all live there.
