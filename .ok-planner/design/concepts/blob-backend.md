---
concept: blob-backend
status: as-is
aliases: []
---

# Blob backend

## What it is

The blob-backend interface is the abstraction that backs spilled byte streams from three surfaces: attribute values, parked-node payloads, and named-event payloads. It exposes five methods (write, read, ranged read, delete, and a backend-name accessor). Four implementations: inline (default; spill disabled), Postgres large-object, filesystem, and an in-memory dev-only backend.

## Purpose

A 50KB attribute value, a 200MB parked payload, and a 10-byte event payload all need to behave the same to substitution consumers. Spilling above a configurable threshold (default 64KB) keeps inline JSONB columns small; a pluggable backend lets operators pick the storage shape (Postgres large-object, shared filesystem, etc.).

## Boundaries

Owns: the abstraction, the four impls, the spill threshold, the orphan-blob ledger and sweep. Does NOT own: substitution (see `attribute`), claim-payload bytes (those are claim-handle-owned), userdata (always inline). Adjacent: `attribute`, `parked-state`, `named-event`, `inertness`, `persistence-database`.

## Invariants

- Blob content is inert in rimsky (`@blessed-invariant 21`). It is read only at the substitution path-walk leaf and at the persistence-layer fetch on read.
- The in-memory backend is rejected at startup unless the process is running in the single-process unified role; the per-process binaries cannot share an in-process map.
- Handles are self-describing strings carrying a backend prefix (inline, Postgres large-object, filesystem, in-memory); current single-backend-per-process means cross-prefix reads fail.
- Orphan blobs go to a persisted orphan-blob ledger and are swept after a retention window.
