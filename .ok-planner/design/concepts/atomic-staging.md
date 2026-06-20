---
concept: atomic-staging
status: as-is
aliases: []
---

# Atomic staging

## Definition

Producer-side stage-then-swap pattern: writers stage data into a side area; on `Commit` the producer atomically swaps the staging into the canonical view; on `Abandon` the staging is dropped. Composes naturally with subgraph-lifetime claims + co-holding verifier nodes + aggregation:

- Subgraph-lifetime claim's auto-terminal triggers `Commit` (atomic swap) on all-success, `Abandon` (drop staging) on any-failure.
- Verifier nodes co-hold the staging claim via `holds:`; their terminals contribute to the parent's aggregation.

## Boundaries

Owns: the producer-side discipline, the documented pattern, the per-substrate atomicity caveats. Does NOT own: rimsky-side mechanics (those are subgraph-lifetime + co-holdership + aggregation, each their own concept), the specific substrate (producer-internal; rimsky doesn't interpret it). Adjacent: `concept:claim-producer`, `concept:claim-lifetime`, `concept:claim-co-holdership`, `concept:auto-terminal`.

## Substrate atomicity caveats

| Substrate shape | Atomicity envelope |
|---|---|
| Transactional store | Atomic via transaction. |
| Metadata-pointer flip | Atomic if the pointer write is atomic. |
| Rename within a single volume | Atomic within the volume. |
| Copy-then-delete across volumes | Windowed; not strictly atomic. |
| Manifest pointer flip | Atomic if the manifest write is. |
| Append-log substrate | Incoherent for the stage-then-swap pattern. |
