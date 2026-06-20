---
tension: blessed-invariant-14-retired
category: vestigial
status: open
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

- CLAUDE.md "Blessed invariants" retirement note for the retired claim-handle slot.

