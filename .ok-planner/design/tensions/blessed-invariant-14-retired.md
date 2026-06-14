---
tension: blessed-invariant-14-retired
category: vestigial
status: open
affects:
  - claim-handle
---

# `@blessed-invariant 14` is retired post-v3 but the number remains in the list

## What is muddy

CLAUDE.md "Blessed invariants" enumerates by number; §14 is annotated as "retired post-v3." The numbering preserves slot 14 as a hole so existing numerical references remain stable.

This is a deliberate convention (don't renumber after removal), but a reader scanning the list sees a gap and may wonder whether `@blessed-invariant 14` is still referenced somewhere in source comments.

## Why it matters

A grep for `@blessed-invariant 14` should return zero hits (or only a documented retirement note). If any source comment still cites it, that's dead code/comment. The retirement is documented but a project-wide audit hasn't been performed against the comment surface.

## Resolution candidates (do NOT pick)

- Record in `concept:claim-handle` that invariant slot 14 is permanently retired and the slot number is reserved (must not be reused), so a reader scanning the numbered invariant list interprets the gap as deliberate.
- Renumber the invariants (would break every external reference to specific numbers).

## Evidence

- CLAUDE.md "Blessed invariants" §14 retirement note (`(Invariant 14 is retired post-v3.)`).

