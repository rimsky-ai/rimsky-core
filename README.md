# Rimsky (Go)

Project-agnostic reactive node-graph orchestration platform.

## Overview

Rimsky orchestrates work as a graph of **nodes**. When a node loses or replaces its value, an `invalidate` cascades to dependents, marking them `stale`; the scheduler picks up stale nodes on subsequent ticks and recalculates them by dispatching their **executors**. Coordination across shared state goes through **claim handles** acquired against named scopes via the **claim-producer** protocol.

Internally, the codebase is organized into three Go modules plus a root modeling layer (see `docs/internal/architecture.md` for the layout):

- **Foundation** (`foundation/` Go module) — cascade engine + lock manager + integration. Owns the per-run records, claim handles, and holding-subgraph state.
- **Modeling** (root module) — templates, instances, frames, schedules, attributes, control-plane API.
- **Service protocols** (`protocols/` Go module) — `ClaimProducer`, `Executor`, `LifecycleSubscriber`.
- **Bundled services** — reference impls under `stores/` (filesystem, postgres, stub) and `executors/` (http-node, claude-agent, stub).

This module organization is implementation detail — external users and agents read `docs/concepts/`, `docs/protocols/`, `docs/humans/`, and `docs/agents/llms.txt`, none of which require the layering to make sense.

## Quick start

    docker compose -f deploy/docker-compose.yml up -d
    curl http://localhost:8080/health
    # Deploy a template, create an instance: see docs/agents/examples/minimal-template-and-instance.md
    # Operator-side install + tuning: see docs/internal/operator-guide.md

## Docs

Authoritative contracts:

- `docs/specs/2026-05-04-foundation-contract.md` — foundation layer.
- `docs/specs/2026-05-04-modeling-layer-contract.md` — modeling layer.
- `docs/specs/2026-05-04-service-protocol-contract.md` — service protocols.

Public-surface guides (cite from these):

- `docs/concepts/` — per-noun canonical reference.
- `docs/protocols/` — write your own claim-producer / executor / lifecycle-subscriber.
- `docs/humans/` — narrative onboarding for human readers.
- `docs/agents/llms.txt` and `docs/agents/llms-full.txt` — LLM-shaped indices.
- `docs/glossary.md` — public vocabulary (auto-generated from `docs/concepts/`).
- `docs/vocabulary.md` — vocabulary discipline + deprecated-term policy.

Internal engineering material (do not cite from public surfaces):

- `docs/internal/architecture.md` — implementation shape + blessed invariants.
- `docs/internal/node-graph-design.md` — conceptual reference (predecessor of `docs/concepts/`).
- `docs/internal/operator-guide.md` — deployment + operation.
- `docs/internal/glossary.md` — internal predecessor of `docs/glossary.md`.
- `docs/internal/executor-author-guide.md`, `docs/internal/claim-producer-author-guide.md` — predecessors of `docs/protocols/`.
- `docs/internal/protocol.md` — pointer to the service-protocol contract.

## Build

    make proto-gen        # regenerate proto bindings (one-time)
    make build-all        # go build across all three modules
    make test-all         # go test across all three modules
    make lint             # golangci-lint

Reference binaries (under `cmd/`):

    rimsky-scheduler
    rimsky-supervisor
    rimsky-control-api
    rimsky-migrate
    rimsky
    rimsky-executor-conformance
    rimsky-conformance-probe
    rimsky-claim-producer-conformance
    rimsky-entrypoint

## Status

Pre-v1; in active development.

## License

Rimsky is multi-licensed. The orchestrator (scheduler, supervisor,
control-API and their internal packages) is licensed under
AGPL-3.0-or-later or a Fall Guy Consulting commercial license. The
embedder layer (wire IDL, executor SDK, reference store and executor
binaries, CLI, conformance suites, deployment artifacts, documentation)
is licensed under the Apache License 2.0. See `COPYRIGHT` for the
per-layer breakdown, `LICENSE.apache` and `LICENSE.agpl` for the license
texts, and `docs/licensing.md` for an operator FAQ.
