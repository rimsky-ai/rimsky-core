---
tension: blessed-invariant-14-retired
category: vestigial
status: resolved
affects:
  - claim-handle
---

# A retired claim-handle invariant lingers as a numbered hole in the list

## What is muddy

A historical claim-handle invariant — long retired post-v3 — was preserved as an annotated gap in the numbered list so existing numerical references stayed stable. A reader scanning the list sees the gap and may wonder whether the retired constraint is still referenced somewhere in source comments.

## Why it matters

A grep for the retired numeric reference should return zero hits (or only a documented retirement note). If any source comment still cites it, that's dead code/comment. The retirement is documented but a project-wide audit hasn't been performed against the comment surface.

## Resolution candidates (do NOT pick)

- Record in `concept:claim-handle` that the retired constraint is permanently retired and the slot is reserved (must not be reused), so a reader scanning the numbered list interprets the gap as deliberate.
- Renumber the invariants (would break every external reference to specific numbers).

## Evidence

- CLAUDE.md carries no "Blessed invariants" section; the numbered-invariant list this tension describes no longer exists as a documentation surface. `concept:claim-handle` states its invariants as prose with sparse parenthetical numbers (4, 9a, 20), not a numbered list with a reserved gap.

## Resolution

Numbered-invariant references (including any reserved gap for a retired number) were banned outright from source code, error strings, tests, and repo-root docs — invariants live in concept docs under descriptive names, and diagnostics describe the violated rule in plain language (`concept:conformance`, 2026-06-19). With the numbering scheme itself retired as a citation surface, the "does a stray comment still cite the old gap" question this tension raised no longer applies: there is no numbered list left for a gap to appear in, and a repo-wide audit confirms zero surviving references to the retired slot outside historical archive material.

