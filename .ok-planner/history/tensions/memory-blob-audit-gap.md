---
tension: memory-blob-audit-gap
status: open
category: durability
---

# memory-blob-audit-gap

## What is muddy

The memory variant of `concept:blob-backend` stores blob bodies in an in-process map; the persisted event log and node-run rows reference those bodies by handle. When the unified process exits, the in-process map vanishes — but the persisted rows survive, referencing handles that no longer resolve. For long-running unified deployments using the memory blob backend, "blobs are ephemeral after process exit" is the documented and intended semantic. The muddiness is what that means for the audit trail: an operator (or post-mortem tool) reading the persisted event log encounters blob handles that resolve to nothing, with no in-band indicator distinguishing structural absence (memory blobs never persisted) from data loss (a backend that lost its data). A reader holding only the persisted event log cannot tell which case they are looking at.

## Evidence

The memory backend is implemented at `code:lib/foundation/persistence/blob_memory.go` and gated to `env:RIMSKY_PROCESS_ROLE=unified` in `code:lib/foundation/persistence/blob_config.go`. Event-log writes reference blob handles uniformly across backends; no per-backend metadata flag indicates resolution-time absence semantics.

## Resolution candidates

- Annotate memory-backend handles with a flag at write time so a reader knows they will not resolve after process exit, and surface that flag in event-log responses.
- Restrict the memory backend further: legal only when no persisted audit consumer is configured (e.g., no lifecycle subscriber, no operator dashboard), so the gap cannot leak to a post-mortem reader.
- Retire the memory backend; require unified-mode deployments to use inline or filesystem blobs.
- Document the gap as a known characteristic of the memory backend and leave the resolution semantics unchanged.
