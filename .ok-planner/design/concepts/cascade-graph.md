---
concept: cascade-graph
status: as-is
aliases:
  - operator dashboard backplane
---

# Cascade graph

## What it is

The operator-dashboard HTTP-route backplane exposed by the control API: a read-only family of endpoints giving operators visibility into rimsky's own persisted runtime state, and, for discovered peer status, into the discovery cache populated by the observability handshake (see `concept:observability`). Membership of the route family is owned by the control-api code, not enumerated here. The per-instance read includes a cascade graph — the concept's namesake capability: the instance's nodes joined with their subscription edges, each node's run summary, and its last terminal event.

## Purpose

Operators (and dashboards built on top of rimsky) need to see what's running, what's wedged, what events have fired, and how cascade is propagating. `cascade-graph` is the read-only HTTP surface that exposes that state without coupling consumers to internal SQL or to the per-service observability protocols.

## Boundaries

Owns: the read-route definitions, the per-route handlers, the JSON marshalling, the per-handler short-transaction discipline — this surface is the sole owner of these handlers; no adjacent concept implements or owns them. Does NOT own: per-service executor/store observability protocols (see `observability`), audit-log writes (see `event-log`), control-plane mutation endpoints (see `control-api`). Adjacent: `observability`, `control-api`, `event-log`, `frame`, `node`.

## Invariants

- Handlers that read persisted tables open a short fresh transaction per read; handlers backed by the discovery cache or the dispatch queue read those sources directly, outside any table transaction.
- Read-only: no handler in this surface mutates state.
- The frames-read routes join each returned frame to its triggering message row, surfacing the message's type, sender, and sender kind alongside the frame (each message triggers at most one frame — see `concept:message` and `concept:frame`).
