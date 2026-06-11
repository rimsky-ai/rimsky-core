---
decision: module-split
status: as-is
---

# Five-module Go workspace

## Choice

Root + the foundation module + the protocols module + the services module + the examples module tied by the workspace definition with local-path `replace`s.

## Rationale

Services-side ship as standalone containers with no rimsky-internal access; protocols are the implementer-facing contract with zero internal deps; the examples module gives each protocol a copy-and-modify reference implementation independent of the orchestrator.
