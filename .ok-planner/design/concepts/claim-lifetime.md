---
concept: claim-lifetime
status: as-is
aliases: []
---

# Claim lifetime

## Definition

Per-claim property in the `claims:` block: `lifetime: subgraph | durable` (default `subgraph`). Governs auto-terminal behavior:

- **`subgraph`** (default) — auto-terminal fires `Commit` (all-success) or `Abandon` (any-failed) at holding-subgraph completion; the claim handle row is **promoted** to a committed (or abandoned) state and preserved for forensics. The retention sweep reaps the row after the configured trailing window elapses (default 30d).
- **`durable`** — auto-terminal still fires `Commit` (or `Abandon`); on success, the claim handle row is promoted to a committed state and is **exempt from the retention sweep** (asset surface). The handle is available for future dispatches to co-hold via `holds:` and for asset-presentation queries. Released only by explicit operator action (the asset-release endpoint) or instance termination (the held-durable-release path); Release goes through the absence-guarded resolved-row delete path (no promotion → already non-active when Release fires).

## Boundaries

Owns: the `lifetime:` template field, the lifetime column on the claim-handle ledger, the auto-terminal skip rule for non-active rows (so committed-durable rows survive past promotion), the retention sweep's exemption for committed-durable rows, the orphan-claim reaper's skip rule for non-active rows. Does NOT own: the asset presentation surface (see `concept:asset`), the DataProcessing protocol (see `concept:data-processing`). Adjacent: `concept:claim`, `concept:claim-handle`, `concept:asset`, `concept:auto-terminal`.

## Invariants

- `lifetime: durable` requires the claim's producer advertise data-processing capability for the claim to qualify as an asset. A `durable` claim against a non-DataProcessing producer is still durable (the row persists), just not surfaced as an asset.
- Held-durable claim handles persist across instance dispatches (`@blessed-invariant 22`, refreshed post-2026-05-17). Auto-terminal commit on a `lifetime: durable` claim promotes the row to a committed state; the retention sweep skips committed-durable rows so they live until explicit Release.
- The orphan-claim reaper skips all non-active rows; the expiry timestamp is meaningful only for active rows.
- The recursive parent-claim resolver treats any non-active child (committed-durable, committed-subgraph, abandoned) the same as a resolved-and-released child: it doesn't block the parent's auto-terminal.
- Conflict detection includes committed-durable rows (the producer still occupies the scope until Release); committed-subgraph rows do NOT participate (the producer Released the scope at Commit).

## Notes

Introduced by `spec:2026-05-15-data-platform-extensions-design` as the core enabler of the asset pattern. The default `subgraph` lifetime preserves the existing held-claim semantics (auto-terminal promotes to committed; retention sweep reaps at cutoff); `durable` opts into asset semantics (retention sweep skips; only Release reaps).

State-column refactor per `spec:2026-05-17-post-data-platform-cleanup`: replaced the held-durable flag setter with a uniform promote-to-committed transition plus a lifetime-aware retention sweep. The terminal-decision engine is now uniform (one promote path for both lifetimes); the asset-vs-not distinction lives entirely in the row's lifetime field.

- 2026-05-25 — Codebase citations removed + cross-refs repaired for self-containment per spec:2026-05-25-concept-doc-self-containment.
