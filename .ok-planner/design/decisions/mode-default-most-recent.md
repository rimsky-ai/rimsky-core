---
decision: mode-default-most-recent
status: as-is
aliases: []
---

# Default cascade mode is `most-recent` (coalesce intermediate cascades)

## Choice

The per-template cascade-mode configuration defaults to `most-recent`, and the mode is a single per-node setting applied uniformly to every upstream feeding the node. The four legal values:

- **`most-recent`** (default): the gate evaluator deletes any prior cascade-driven stale-not-claimed run for the same (node, run-scope) at pending→stale transition; the new run takes its place. Cascade-stale depth ≤ 1 per (node, run-scope). M cascade rounds during a single in-flight period collapse to one post-settle dispatch with the latest view.
- **`sequenced`** (opt-in): no delete, no dedup. Multiple cascade-driven stales coexist; the dispatcher claims them in `sequence` order. M cascade rounds produce M dispatches.
- **`idempotent-queue`** (opt-in): drop the new run if its JCS-canonical input bag equals the prior cascade-stale's. Otherwise behaves like `sequenced`.
- **`idempotent-settled`** (opt-in): same as `idempotent-queue` but also compares against the most recent fresh-settled predecessor when no cascade-stale exists.

Non-cascade rows (`operator_invalidate`, `recalculate`, `message_delivery`) are immune to all mode rules regardless of the configured mode.

A node that needs different policies for different upstreams — a chatty feed to coalesce and an audit feed to keep in full — splits into one node per policy. That split is the supported pattern for mixed-cadence subscriptions, not a workaround.

## Rationale

Most-recent is the right default because most reactive workloads want "the latest, not the history." An executor that takes longer than its upstream's cadence will queue an unbounded number of intermediate dispatches under any non-coalescing default, falling further behind as upstreams continue. Most-recent collapses the backlog at each settle boundary so the executor catches up to the current state automatically.

The opt-in modes exist because some workloads genuinely need every round: audit-trail executors recording change history, accumulators applying every increment, monitoring executors detecting rapid state flips. For these, `most-recent`'s coalescing would destroy data the executor exists to capture. The split between `sequenced` and the idempotent variants gives authors a choice on dedup granularity: pay the JCS comparison cost (idempotent-queue / idempotent-settled) when consecutive identical inputs are plausible, skip it (sequenced) when every round is meaningfully distinct.

Choosing `most-recent` as the default also matches the conservative-safety default for queue depth. The opt-in modes open unbounded queue growth under pathological conditions (a stuck in-flight node + rapid upstream cascades). Most-recent's ≤1-cascade-stale bound is a natural backstop. Authors who need the opt-in modes are explicitly accepting the queue-growth tradeoff.

## Alternatives

Default to `sequenced` — rejected because it makes the lag-falling-behind failure mode the default. Authors must understand and opt-in to coalescing, when most workloads want coalescing automatically.

Default to `idempotent-settled` — rejected because it pays the JCS comparison cost on every cascade-driven dispatch, even for authors who don't need dedup. The cost is small per-comparison but compounds across high-frequency cascade workloads.

No default; require explicit per-template config — rejected because it adds friction to template authoring with no upside. The vast majority of templates want `most-recent`; requiring explicit declaration for the common case is paperwork.

Per-upstream-source mode configuration (a node-wide default with per-sender or per-signal-type overrides, or a mode on the subscription declaration) — rejected: no workload in the catalog motivates it, and every keying axis adds a resolution-precedence question to the cascade walk when several upstreams feed one pending run. Splitting the node carries the mixed-cadence case.
