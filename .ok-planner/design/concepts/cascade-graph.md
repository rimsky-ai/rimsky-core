---
concept: cascade-graph
status: as-is
aliases:
  - operator dashboard backplane
references:
  - _discover/observability-cascade-graph-endpoint.md
---

# Cascade graph

## What it is

The operator-dashboard HTTP-route backplane exposed by rimsky-control-api: `/observability/*`, `/events`, `/frames`, `/nodes/{instance}/{type}`, `/dispatches`. Routes mounted via `go-chi/chi`. Reads rimsky's own runtime state (frames, nodes, dispatches, events) and serves JSON to dashboards and operator tooling.

## Purpose

Operators (and dashboards built on top of rimsky) need to see what's running, what's wedged, what events have fired, and how cascade is propagating. `cascade-graph` is the read-only HTTP surface that exposes that state without coupling consumers to internal SQL or to the per-peer observability protocols.

## Boundaries

Owns: the route definitions, the per-route handlers, the JSON marshalling, the `inTx`-per-handler discipline. Does NOT own: per-peer executor/store observability protocols (see `observability`), audit-log writes (see `event-log`), control-plane mutation endpoints (see `control-api`). Adjacent: `observability`, `control-api`, `event-log`, `frame`, `node`.

## Invariants

- All cascade-graph HTTP handlers run inside a short fresh transaction (`inTx`).
- Read-only: no handler in this surface mutates state.
- Routes are mounted at bare paths (no `/v1/` prefix), matching the parent `control-api` versioning discipline.

## Aliases and historical names

The HTTP surface was previously documented inside `observability`; promoted to its own concept under the `2026-05-11-design-log-convergence` spec.

## Open within this concept

(no live tensions.)
