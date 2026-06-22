---
decision: mode-default-most-recent
status: as-is
aliases: []
---

# Default cascade mode is `most-recent` (coalesce intermediate cascades)

## Choice

The per-template cascade-mode configuration defaults to `most-recent`. The four legal values:

- **`most-recent`** (default): the gate evaluator deletes any prior cascade-driven stale-not-claimed run for the same (node, run-scope) at pending→stale transition; the new run takes its place. Cascade-stale depth ≤ 1 per (node, run-scope). M cascade rounds during a single in-flight period collapse to one post-settle dispatch with the latest view.
- **`sequenced`** (opt-in): no delete, no dedup. Multiple cascade-driven stales coexist; the dispatcher claims them in `sequence` order. M cascade rounds produce M dispatches.
- **`idempotent-queue`** (opt-in): drop the new run if its JCS-canonical input bag equals the prior cascade-stale's. Otherwise behaves like `sequenced`.
- **`idempotent-settled`** (opt-in): same as `idempotent-queue` but also compares against the most recent fresh-settled predecessor when no cascade-stale exists.

Non-cascade rows (operator_invalidate, policy_retry, infra_reenqueue) are immune to all mode rules regardless of the configured mode.

## Rationale

Most-recent is the right default because most reactive workloads want "the latest, not the history." An executor that takes longer than its upstream's cadence will queue an unbounded number of intermediate dispatches under any non-coalescing default, falling further behind as upstreams continue. Most-recent collapses the backlog at each settle boundary so the executor catches up to the current state automatically.

The opt-in modes exist because some workloads genuinely need every round: audit-trail executors recording change history, accumulators applying every increment, monitoring executors detecting rapid state flips. For these, `most-recent`'s coalescing would destroy data the executor exists to capture. The split between `sequenced` and the idempotent variants gives authors a choice on dedup granularity: pay the JCS comparison cost (idempotent-queue / idempotent-settled) when consecutive identical inputs are plausible, skip it (sequenced) when every round is meaningfully distinct.

Choosing `most-recent` as the default also matches the conservative-safety default for queue depth. The opt-in modes open unbounded queue growth under pathological conditions (a stuck in-flight node + rapid upstream cascades). Most-recent's ≤1-cascade-stale bound is a natural backstop. Authors who need the opt-in modes are explicitly accepting the queue-growth tradeoff.

## Alternatives

Default to `sequenced` — rejected because it makes the lag-falling-behind failure mode the default. Authors must understand and opt-in to coalescing, when most workloads want coalescing automatically.

Default to `idempotent-settled` — rejected because it pays the JCS comparison cost on every cascade-driven dispatch, even for authors who don't need dedup. The cost is small per-comparison but compounds across high-frequency cascade workloads.

No default; require explicit per-template config — rejected because it adds friction to template authoring with no upside. The vast majority of templates want `most-recent`; requiring explicit declaration for the common case is paperwork.

Default to a hybrid "most-recent for attribute cascades, sequenced for message cascades" — rejected as the per-cascade-source-mode generalization (see `tension:per-cascade-source-mode`). One mode per node is the chosen surface; the generalization is undecided and tracked as a tension.
