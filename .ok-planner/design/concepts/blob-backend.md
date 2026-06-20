---
concept: blob-backend
status: as-is
aliases: []
---

# Blob backend

## What it is

The blob-backend interface is the abstraction that backs spilled byte streams from one surface: attribute values. It exposes byte-stream IO with a backend-name accessor. Multiple pluggable backends exist, distinguished by where bytes live.

## Purpose

A small attribute value and a large attribute blob need to behave the same to substitution consumers. Spilling above a configurable threshold keeps inline columns small; a pluggable backend lets operators pick the storage shape.

## Boundaries

Owns: the abstraction, its implementations, the spill threshold, the orphan-blob ledger and sweep. Does NOT own: substitution (see `attribute`), claim-payload bytes (those are claim-handle-owned), userdata (always inline). Adjacent: `attribute`, `inertness`, `persistence-database`.

## Invariants

- Blob content is inert in rimsky (invariant 21). It is read only at the substitution path-walk leaf and at the persistence-layer fetch on read.
- The in-memory backend is legal only in the single-process deployment mode — all roles running in one process, where one in-process map is genuinely shared, cross-role blob reads work, and the orphan-blob sweep reaps spilled blobs. It is startup-rejected in any per-role process, because separate processes cannot share an in-process map.
- Handles are self-describing strings carrying a backend prefix; the single-backend-per-process configuration means cross-prefix reads fail.
- Orphan blobs go to a persisted orphan-blob ledger and are swept after a retention window.
