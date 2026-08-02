---
decision: module-split
---

# Five-module Go workspace

## Choice

Root + the foundation module + the protocols module + the services module + the examples module tied by the workspace definition with local-path `replace`s.

## Rationale

Services-side ship as standalone containers with no rimsky-internal access; protocols are the implementer-facing contract with zero internal deps; the examples module gives each protocol a copy-and-modify reference implementation independent of the orchestrator.

## Alternatives

- One Go module for everything — rejected: consumers of the protocols contract or the bundled services would drag rimsky's whole internal dependency graph, and import isolation would rest on lint alone rather than module boundaries.
- Separate repositories per module — rejected: cross-cutting changes (proto regeneration, shared-type changes) span the modules constantly pre-v1; multi-repo coordination cost with no consumer yet to shield.
