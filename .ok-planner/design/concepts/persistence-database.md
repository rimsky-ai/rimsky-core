---
concept: persistence-database
aliases:
  - persistence-driver
---

# Persistence database

## What it is

The persistence database is the umbrella over rimsky's whole persistence layer: the runtime handle a process opens once, holds for its lifetime, and closes at shutdown. In the split deployment each of the three runtime roles runs as its own process and holds its own handle; in the unified deployment the three roles run in one process and share one handle. The handle hands back a bundle of per-ledger accessors, one per persisted ledger. Most callers need only a few of them, and the bundle keeps their startup wiring compact. Rimsky carries two implementations behind the one interface: a client-server backend, the default everywhere except the all-in-one deployment, and an embedded-file backend, which the all-in-one deployment uses. One migration runner serves both, so the schema cannot fork between them. The driver name — the value that selects which implementation to load — is a separate thing from the handle: driver names the shape of the backend, not the runtime object.

An attribute bag and a node-run's scratch are byte-column values in their own rows, whatever their size. The engine never reads inside them, and the engine's own per-value cap bounds them (see `decision:attribute-bytes-in-the-row`).

## Purpose

The persistence database gives graph and control code one abstraction to reach durable state through, so no caller reaches a backend driver directly and an import boundary the build enforces keeps that driver behind the interface. It lets a fast embedded backend stand in for the client-server backend under test, and it admits a further backend without changing a caller.

## Boundaries

The persistence database owns the handle interface, the accessor bundle, the per-ledger accessors, the two implementations, and the migration runner. It owns the schema; executor state that must outlive a dispatch rides the generic surfaces the protocols expose (see `decision:scratch-column`).

It does not own what the schema says, which the migrations carry, nor connection-pool sizing, which an operator configures. It does not own the named locks it hands out, which belong to `concept:advisory-lock`.

see also: `advisory-lock`, `node-run`, `attribute`

## Aliases

- persistence-driver
