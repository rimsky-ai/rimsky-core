---
concept: cascade-graph
status: as-is
aliases:
  - operator dashboard backplane
---

# Cascade graph

## What it is

The operator-dashboard HTTP-route backplane exposed by the control API: a family of read endpoints covering observability summaries, the event feed, frames, per-instance node state, and dispatches. Reads rimsky's own runtime state (frames, nodes, dispatches, events) and serves JSON to dashboards and operator tooling.

## Purpose

Operators (and dashboards built on top of rimsky) need to see what's running, what's wedged, what events have fired, and how cascade is propagating. `cascade-graph` is the read-only HTTP surface that exposes that state without coupling consumers to internal SQL or to the per-service observability protocols.

## Boundaries

Owns: the read-route definitions, the per-route handlers, the JSON marshalling, the per-handler short-transaction discipline. Does NOT own: per-service executor/store observability protocols (see `observability`), audit-log writes (see `event-log`), control-plane mutation endpoints (see `control-api`). Adjacent: `observability`, `control-api`, `event-log`, `frame`, `node`.

## Invariants

- All cascade-graph HTTP handlers run inside a short fresh transaction.
- Read-only: no handler in this surface mutates state.
- Routes are mounted under the control API's versioned prefix, matching the parent `control-api` versioning discipline.
