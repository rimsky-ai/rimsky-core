# Rimsky (Go)

Project-agnostic reactive node-graph orchestration platform.

## Overview

Rimsky orchestrates work as a graph of **nodes** that communicate via two messages (`invalidate`, `recalculate`) and operate on versioned **resources**. Nodes execute work through external **executors** — peer services that speak the rimsky node-executor protocol (gRPC + HTTP+JSON bridge).

Three collections compose the platform:

- **Orchestrator** (this Go module) — scheduler, supervisor, control API, dispatch queue, storage, migrations.
- **Resources** (`core/resource/`) — pluggable data-commit backends. Ships with `inline-jsonb` and `external-sql`.
- **Executors** (`executors/`) — pluggable work-runner peers. Ships with `http-node` (Go) and `claude-agent` (TypeScript).

## Quick start

    docker compose -f deploy/docker-compose.yml up -d
    curl http://localhost:8080/health
    # Deploy a template, create an instance: see docs/operator-guide.md

## Docs

- `docs/node-graph-design.md` — conceptual reference
- `docs/architecture.md` — implementation shape + blessed invariants
- `docs/protocol.md` — node-executor protocol reference
- `docs/operator-guide.md` — deployment + operation
- `docs/executor-author-guide.md` — write your own executor (any language)
- `docs/resource-author-guide.md` — write your own resource implementation (Go)

## Build

    make proto-gen        # regenerate proto bindings (one-time)
    go test ./...
    go build ./...
    make lint             # golangci-lint

Reference binaries:

    ./core/cmd/rimsky-scheduler
    ./core/cmd/rimsky-supervisor
    ./core/cmd/rimsky-control-api
    ./core/cmd/rimsky-migrate
    ./core/cmd/rimsky-conformance
    ./core/cmd/rimsky-conformance-probe

## Status

v1 in development. First real-world consumer is anticipated; migration guides live in consumer repos.

## License

TBD (will be permissive open-source at v1 ship).
