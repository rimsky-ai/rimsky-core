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

Owns: the producer-side discipline, the documented pattern, a filesystem-substrate reference implementation, the per-substrate atomicity caveats. Does NOT own: rimsky-side mechanics (those are subgraph-lifetime + co-holdership + aggregation, each their own concept), the specific substrate (filesystem rename, Postgres tx, Iceberg manifest pointer, etc.). Adjacent: `concept:claim-producer`, `concept:claim-lifetime`, `concept:claim-co-holdership`, `concept:auto-terminal`.

## Substrate atomicity caveats

| Substrate | Atomicity envelope |
|---|---|
| Postgres schema swap | Atomic via transaction. |
| Iceberg branch fast-forward | Atomic via metadata pointer. |
| POSIX filesystem `rename` | Atomic within a filesystem. |
| S3 copy+delete | Windowed; not strictly atomic. |
| Manifest pointer flip | Atomic if the manifest write is. |
| Kafka | Incoherent for the pattern. |
