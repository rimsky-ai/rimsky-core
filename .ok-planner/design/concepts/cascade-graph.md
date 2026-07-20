---
concept: cascade-graph
status: as-is
aliases:
  - operator dashboard backplane
---

# Cascade graph

## What it is

The operator-dashboard HTTP-route backplane exposed by the control API: a family of read endpoints covering observability summaries, the event feed, frames, per-instance node state, node runs, dispatches, templates, instances, lock holders and claim handles, discovered peer (executor and claim-producer) status, and system health and summary counts. Most of these routes read rimsky's own persisted runtime state (frames, nodes, dispatches, events); the peer-status routes instead read the discovery cache populated by the observability handshake (see `concept:observability`). The per-instance read includes a cascade graph: the instance's nodes joined with their subscription edges, each node's run summary, and its last terminal event. This surface's own frames-read routes list and fetch frame rows filtered by instance id and lifecycle state; they do not join a frame to its triggering message row in either direction (each message triggers at most one frame — see `concept:message` and `concept:frame`).

## Purpose

Operators (and dashboards built on top of rimsky) need to see what's running, what's wedged, what events have fired, and how cascade is propagating. `cascade-graph` is the read-only HTTP surface that exposes that state without coupling consumers to internal SQL or to the per-service observability protocols.

## Boundaries

Owns: the read-route definitions, the per-route handlers, the JSON marshalling, the per-handler short-transaction discipline — this surface is the sole owner of these handlers; no adjacent concept implements or owns them. Does NOT own: per-service executor/store observability protocols (see `observability`), audit-log writes (see `event-log`), control-plane mutation endpoints (see `control-api`). Adjacent: `observability`, `control-api`, `event-log`, `frame`, `node`.

## Invariants

- Handlers that read persisted tables open a short fresh transaction per read; handlers backed by the discovery cache or the dispatch queue read those sources directly, outside any table transaction.
- Read-only: no handler in this surface mutates state.
