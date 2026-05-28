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

## Notes

Introduced by spec:2026-05-15-data-platform-extensions-design. The pattern is producer-side discipline; no rimsky-level surface change is required (subgraph-lifetime claims + co-holdership + aggregation existed before, in earlier form; this concept names the recurring shape and points at a reference impl).

- 2026-05-19 — Reference impl set extends to the **SQL-substrate verifier role** demonstrated by a fused producer-plus-executor binary (`concept:claim-producer` + `concept:executor` on one binary), per spec:2026-05-19-multi-instance-template-ergonomics-design. The verifier role runs read-only aggregate SQL against a staging schema named by the claim's address selector and emits success / verifier-failed-error outcomes shaped to the supervisor's terminal-routing contract; aggregation across co-holding verifiers fires Commit on all-success and Abandon on any-failure per the existing atomic-staging pattern. The **producer-side staging-schema lifecycle for SQL substrates is not yet shipped**: the postgres store's open verb echoes the selector as the address (no schema creation, no scope-reservation, no staging setup), so the example template's staging producer is illustrative of the held-claim shape, not a working schema creator. Operators wanting an SQL substrate that also creates the staging schema must wrap the postgres store or supply a sidecar producer. Substrate-atomicity table unchanged.
- 2026-05-25 — Codebase citations removed + cross-refs repaired for self-containment per spec:2026-05-25-concept-doc-self-containment.
