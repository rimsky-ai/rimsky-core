---
tension: userdata-overrides-fold-into-userdata
category: overloaded
status: resolved
spec: 2026-05-11-design-log-convergence
affects:
  - userdata
  - userdata-overrides
resolution:
  shape: fold-into-userdata
  dropped: concepts/userdata-overrides.md
  folded-into: concepts/userdata.md (Per-instance overrides subsection)
  summary: |
    Folded userdata-overrides into userdata as a "Per-instance
    overrides" subsection. The override mechanism has no independent
    existence (exists only to mutate userdata); userdata's Boundaries
    already claimed the merge mechanism. The subsection covers
    routing-key shape, merge order, validation discipline preserving
    @blessed-invariant 11, platform-extensions provenance.
---

# `userdata-overrides` is a thin sub-mechanism of `userdata`, not a separable concept

## What is muddy

`concepts/userdata.md` Boundaries already claims ownership of "the per-instance override merge mechanism, the routing-key validation." `concepts/userdata-overrides.md` then independently documents the same mechanism with its own Invariants and Purpose. The two concepts have non-trivial content overlap; the split is partial-already-folded.

The override mechanism has no independent existence — it exists only to mutate userdata. Its routing-key shape (`by_executor`, `by_node`), merge order (`template → by_executor → by_node`), and routing-key-only validation discipline (preserves `@blessed-invariant 11`) are non-trivial, but structurally they are sub-aspects of "how userdata reaches the executor."

## Why it matters

- Reader confusion: a reader hitting `userdata.md` learns overrides exist in Boundaries and may not look further; one hitting `userdata-overrides.md` does not get the underlying opacity / no-substitution / no-inspection invariants that govern the bytes being overridden.
- Catalog parsimony: 46 concepts → 45 after fold; this kind of "the host concept already claims the mechanism" overlap is the cleanest consolidation pattern.

## Resolution candidates (do NOT pick)

- **Fold** `userdata-overrides.md` into `userdata.md` as a `Per-instance overrides` subsection. Move the routing-key shape, merge order, validation discipline, deep-merge helper reference, platform-extensions provenance into the subsection. Drop the standalone file. Update any `Adjacent: userdata-overrides` references to point at `userdata`.
- **Keep split** (status quo).

(Pre-decided shape: fold.)

## Evidence

- `concepts/userdata.md` Boundaries.
- `concepts/userdata-overrides.md`.
- `_discover/2026-05-10-userdata-overrides-by-instance.md`.
- `review-notes.md` "Possible merges / splits to reconsider" / `userdata` vs `userdata-overrides` bullet.

