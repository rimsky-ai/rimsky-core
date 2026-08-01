---
decision: memory-gate-premise-corrected
status: as-is
---

# In-memory blob backend is gated to single-process mode

## Choice

The in-memory blob backend is startup-rejected outside the single-process unified-role mode; the gate's error surface describes the single-process mode as the reason — the one deployment where one in-process map is genuinely shared, the orphan-blob sweep reaps it, and cross-role reads work (see `concept:blob-backend`, `decision:single-process-mode`).

## Rationale

Cross-process memory blobs are broken by physics, not policy. The asymmetry with ungated SQLite reflects that SQLite multi-process is made safe (see `decision:sqlite-multiproc-safety`); memory multi-process cannot be.

## Alternatives

- Leave the memory backend ungated and document the single-process restriction — rejected: cross-role blob reads fail confusingly at runtime instead of loudly at startup.
- Drop the memory backend entirely — rejected: it legitimately serves the single-process mode's zero-dependency dev and test deployments.
