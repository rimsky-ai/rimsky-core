---
decision: design-link-annotations
status: as-is
---

# Code → design citation

## Choice

Source-code annotations linking sites to their concept, story, or decision slug at the point of enforcement.

## Rationale

Traceability from code to design model. The link direction matters: a slug-keyed annotation in code survives file moves and refactors, while a path-keyed pointer in a design doc rots with every reorganization.

## Alternatives

- Design docs citing code paths (the reverse link direction) — rejected: file paths rot with every refactor, and each move silently orphans the design doc's pointer.
- No links, navigation by grep of shared names alone — rejected: a reader chasing a design artifact from code cannot tell whether a missing match means retired, renamed, or never written.
