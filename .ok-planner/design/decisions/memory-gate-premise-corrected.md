---
decision: memory-gate-premise-corrected
status: as-is
---

# The memory-blob gate stays, with a true premise

## Choice

The in-memory blob backend remains startup-rejected outside `RIMSKY_PROCESS_ROLE=unified`; the gate's error text and comments describe the single-process mode as the reason — the one deployment where one in-process map is genuinely shared, the orphan-blob sweep reaps it, and cross-role reads work (see `concept:blob-backend`, `decision:single-process-mode`).

## Rationale

Cross-process memory blobs are broken by physics, not policy. The asymmetry with ungated SQLite is justified and recorded: SQLite multi-process is made safe (see `decision:sqlite-multiproc-safety`); memory multi-process cannot be.
