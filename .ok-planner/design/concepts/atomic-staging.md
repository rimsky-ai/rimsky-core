---
concept: atomic-staging
status: as-is
aliases: []
---

# Atomic staging

## What it is

Producer-side stage-then-swap pattern: writers stage data into a side area; on `Commit` the producer atomically swaps the staging into the canonical view; on `Abandon` the staging is dropped. This is the producer-side discipline that realizes `concept:write-semantics`'s staged-asynchronous mode: readers see the pre-stage snapshot until a swap commits. Composes naturally with subgraph-lifetime claims + co-holding verifier nodes + aggregation:

- Subgraph-lifetime claim's auto-terminal triggers `Commit` (atomic swap) on all-success, `Abandon` (drop staging) on any-failure.
- Verifier nodes co-hold the staging claim via `holds:`; their terminals contribute to the parent's aggregation.

## Boundaries

Owns: the producer-side discipline, the documented pattern, the per-substrate atomicity caveats. Does NOT own: rimsky-side mechanics (those are subgraph-lifetime + co-holdership + aggregation, each their own concept), the specific substrate (producer-internal; rimsky doesn't interpret it). Adjacent: `concept:claim-producer`, `concept:claim-lifetime`, `concept:claim-co-holdership`, `concept:auto-terminal`, `concept:write-semantics`.

## Invariants

- A transactional-store substrate swaps atomically via its transaction.
- A metadata-pointer-flip substrate swaps atomically if the pointer write itself is atomic.
- A rename-within-a-single-volume substrate swaps atomically within that volume.
- A copy-then-delete-across-volumes substrate swaps within a window, not strictly atomically.
- A manifest-pointer-flip substrate swaps atomically if the manifest write itself is atomic.
- An append-log substrate is incoherent for the stage-then-swap pattern; atomic staging does not apply to it.
