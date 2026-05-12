---
tension: licensing-boundary-fold-candidate
category: overloaded
status: resolved
spec: 2026-05-11-design-log-convergence
affects:
  - licensing-boundary
  - module-layout
resolution:
  shape: fold-into-module-layout
  dropped: concepts/licensing-boundary.md
  folded-into: concepts/module-layout.md (Licensing boundary subsection)
  summary: |
    Folded licensing-boundary into module-layout as a final subsection.
    Repo-organization concern, not a runtime noun; module-layout already
    cited it as Adjacent. Standalone file dropped.
---

# `licensing-boundary` is a repo-organization concern, not a runtime noun

## What is muddy

`concepts/licensing-boundary.md` exists as a standalone concept, but its content (Apache vs AGPL line at the `eval/` directory boundary, what crosses the line, what stays under permissive licensing) is structurally a sub-aspect of `module-layout`. `module-layout` already cites `licensing-boundary` as `Adjacent:`. The system doesn't *do* anything with the licensing boundary at runtime; it's a static repo property.

## Why it matters

Catalog parsimony: 46 concepts is well over the 15–25 heuristic flagged in review-notes. Concepts that are not nouns-the-system-traffics-in are the most defensible consolidation candidates. A reader scanning the noun catalog should not need to skim a repo-organization page.

## Resolution candidates (do NOT pick)

- **Fold** `licensing-boundary.md` into `module-layout.md` as a final subsection ("Licensing boundary: Apache surface vs. AGPL `eval/`"). Drop the standalone file. Update any `Adjacent: licensing-boundary` refs to `module-layout`.
- **Keep** the standalone concept (status quo). Document the rationale explicitly in `module-layout`'s `Adjacent:` block to defend the split.

## Evidence

- `concepts/licensing-boundary.md` (current standalone).
- `concepts/module-layout.md` (already cites `licensing-boundary` as Adjacent).
- `concepts/quality-rule.md` (the Apache/AGPL split is mentioned only in passing here).
- `review-notes.md` "Possible merges / splits to reconsider".

