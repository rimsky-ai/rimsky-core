---
resolved_by: spec:2026-05-12-nomenclature-resolution
tension: region-vs-scope-legacy
category: vestigial
status: resolved
affects:
  - claim-scope
  - claim
---

# `region` is a deprecated synonym for `scope` but still appears in comments and prose

## What is muddy

`scope` is the current canonical name (`scope_data` column, `ScopesByteEqual`). `region` is the deprecated v2-era synonym. `docs/concepts/scope.md` lists `[region]` under `deprecated_terms` but:

- `foundation/locks/conflict.go:14-18` still cites "v2's per-store RegionsConflict / UnmarshalRegion methods" by historical name.
- Older prose snippets reference `region` directly.

## Why it matters

A reader grepping for `region` finds historical comments that look like they reference live code. The proto field `scope` is unambiguous; the inline historical comment is the residue.

## Resolution candidates (do NOT pick)

- Scrub `region` from current source comments (keep only in historical archive / pre-v3 design docs).
- Add a `vocabulary-lint-ignore: region` annotation at each historical reference.

## Evidence

- `_discover/2026-05-10-byte-equal-scope-conflict.md` Observations bullet "region term".
- `foundation/locks/conflict.go:14-18` comment.
- `docs/concepts/scope.md` `deprecated_terms` block.

## Notes

- 2026-05-22 — Frontmatter status reconciled (was `open` despite file living under `_resolved/`). The 2026-05-22 ClaimScope rename per spec `.ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md` is consistent with this resolution's original qualified-naming spirit — `Scope` (a vestigial-region synonym) became `ClaimScope` (a self-disambiguating qualified term), continuing the same nomenclature discipline.

