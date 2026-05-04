# Rimsky (Go)

Project-agnostic reactive node-graph orchestration platform.

## Overview

Rimsky orchestrates work as a graph of **nodes** that communicate via two messages (`invalidate`, `recalculate`) and that acquire **claim handles** against named scopes via the **claim-producer** protocol. Nodes execute work through external **executors** — peer services that speak the executor protocol (gRPC + HTTP+JSON bridge).

Four conceptual layers (see `docs/architecture.md`):

- **Foundation** (`foundation/` Go module) — cascade engine + lock manager + integration. Tables: `rimsky_worker_request`, `rimsky_claim_handle`, `rimsky_claim_holders`, `rimsky_nodes` (split-owned).
- **Modeling** (root module) — templates, instances, frames, schedules, attributes, control-plane API. Tables: `rimsky_templates`, `rimsky_instances`, `rimsky_schedules`, `rimsky_frames`, `rimsky_events`, `rimsky_lifecycle_idempotency`.
- **Service protocols** (`protocols/` Go module) — `ClaimProducer`, `Executor`, `LifecycleSubscriber`.
- **Bundled services** — reference impls under `stores/` (filesystem, postgres, stub) and `executors/` (http-node, claude-agent, stub).

## Quick start

    docker compose -f deploy/docker-compose.yml up -d
    curl http://localhost:8080/health
    # Deploy a template, create an instance: see docs/operator-guide.md

## Docs

Authoritative contracts:

- `docs/specs/2026-05-04-foundation-contract.md` — foundation layer.
- `docs/specs/2026-05-04-modeling-layer-contract.md` — modeling layer.
- `docs/specs/2026-05-04-service-protocol-contract.md` — service protocols.

Operator + author guides:

- `docs/architecture.md` — implementation shape + blessed invariants.
- `docs/node-graph-design.md` — conceptual reference.
- `docs/operator-guide.md` — deployment + operation.
- `docs/glossary.md` — vocabulary.
- `docs/executor-author-guide.md` — write your own executor.
- `docs/claim-producer-author-guide.md` — write your own claim producer.
- `docs/protocol.md` — pointer to the service-protocol contract.

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
    rimsky-cli
    rimsky-conformance
    rimsky-conformance-probe
    rimsky-claim-producer-conformance
    rimsky-entrypoint

## Status

Pre-v1; in active development.

## License

TBD (will be permissive open-source at v1 ship).
