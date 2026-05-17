---
concept: atomic-staging
status: as-is
aliases: []
references:
  - ../../specs/2026-05-15-data-platform-extensions-design.md
---

# Atomic staging

## Definition

Producer-side stage-then-swap pattern: writers stage data into a side area; on `Commit` the producer atomically swaps the staging into the canonical view; on `Abandon` the staging is dropped. Composes naturally with subgraph-lifetime claims + co-holding verifier nodes + aggregation:

- Subgraph-lifetime claim's auto-terminal triggers `Commit` (atomic swap) on all-success, `Abandon` (drop staging) on any-failure.
- Verifier nodes co-hold the staging claim via `holds:`; their terminals contribute to the parent's aggregation.

## Boundaries

Owns: the producer-side discipline, the documented pattern, the reference impl (`examples/atomic-staging-fs-producer/`), the per-substrate atomicity caveats. Does NOT own: rimsky-side mechanics (those are subgraph-lifetime + co-holdership + aggregation, each their own concept), the specific substrate (filesystem rename, Postgres tx, Iceberg manifest pointer, etc.). Adjacent: `concept:claim-producer`, `concept:claim-lifetime`, `concept:claim-co-holdership`, `concept:auto-terminal`.

## Substrate atomicity caveats

| Substrate | Atomicity envelope |
|---|---|
| Postgres schema swap | Atomic via transaction. |
| Iceberg branch fast-forward | Atomic via metadata pointer. |
| POSIX filesystem `rename` | Atomic within a filesystem. |
| S3 copy+delete | Windowed; not strictly atomic. |
| Manifest pointer flip | Atomic if the manifest write is. |
| Kafka | Incoherent for the pattern. |

## Annotation sites

- `code:examples/atomic-staging-fs-producer/` — reference impl on POSIX filesystem.
- `docs/agents/examples/atomic-staging.md` — operator-facing pattern doc.
- `code:test/scenarios/atomic_staging/` — scenario coverage.

## Notes

Introduced by `.ok-planner/specs/2026-05-15-data-platform-extensions-design.md`. The pattern is producer-side discipline; no rimsky-level surface change is required (subgraph-lifetime claims + co-holdership + aggregation existed before, in earlier form; this concept names the recurring shape and points at a reference impl).
