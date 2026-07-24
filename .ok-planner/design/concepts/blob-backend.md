---
concept: blob-backend
status: as-is
aliases: []
---

# Blob backend

## What it is

The blob-backend interface is the abstraction that backs spilled byte streams from two surfaces: attribute values and scratch. It exposes byte-stream IO with a backend-name accessor. Multiple pluggable backends exist, distinguished by where bytes live.

## Purpose

A small attribute value and a large attribute blob need to behave the same to substitution consumers. Spilling above a configurable threshold keeps inline columns small; a pluggable backend lets operators pick the storage shape.

## Boundaries

Owns: the abstraction, its implementations, the spill threshold, the orphan-blob ledger and sweep. Does NOT own: substitution (see `attribute`), claim-payload bytes (those are claim-handle-owned). Adjacent: `attribute`, `inertness`, `persistence-database`.

## Invariants

- Blob content is inert in rimsky (invariant 21). It is read only at the substitution path-walk leaf, the persistence-layer fetch on read, and the runtime-layer scratch load at dispatch acquisition.
- The in-memory backend is legal only in the single-process deployment mode — all roles running in one process, where one in-process map is genuinely shared, cross-role blob reads work, and the orphan-blob sweep reaps spilled blobs. It is startup-rejected in any per-role process, because separate processes cannot share an in-process map.
- Handles are self-describing strings carrying a backend prefix; on a backend-name mismatch, reads fall back to the inline data column rather than erroring — a deliberate silent storage downgrade for that row, favoring continuity over strictness.
- **Unresolvable-handle interpretation is backend-keyed.** An unresolvable handle whose backend prefix names the in-memory backend, encountered after the writing process's exit, is structural absence — not data loss. The memory backend is ephemeral by physics (see `story:single-process-all-in-one`, `decision:memory-gate-premise-corrected`), so its handles do not survive the process that wrote them. An unresolvable handle whose prefix names any other backend indicates data loss and warrants investigation. Post-mortem readers of the persisted event log distinguish the two cases by inspecting the handle's backend prefix.
- Orphan blobs go to a persisted orphan-blob ledger and are swept after a retention window, by a sweep scoped to the currently-configured backend — a ledger row whose `backend` differs from the running backend (e.g. after an operator switches backends pre-v1) is skipped every sweep and retained indefinitely, since a process can only reach the bytes of its own configured backend.
