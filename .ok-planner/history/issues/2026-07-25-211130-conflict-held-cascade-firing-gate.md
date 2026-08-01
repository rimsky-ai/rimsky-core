---
issue: conflict-held-cascade-firing-gate
kind: audit
category: conflicting
artifacts:
  - concept:cascade
  - concept:signal
  - concept:auto-terminal
status: repaired
opened: 2026-07-25T21:11:30Z
---

# Does the running-to-held transition emit a terminal signal?

Partially, and immediately — verified in `lib/runtime/runner_terminal.go` (the held branch of `applyTerminalComplete`) and `lib/runtime/signal_emit.go`. At the `running → held` transition, the code builds a genuine `terminal/success` (or `terminal/error/<class>`) signal and calls `cascadeSignalInTxWithFilter`, which drives the cascade walk (member-filtered) but — unlike `emitSignalInTxWithFilter`, its unfiltered sibling used elsewhere — does **not** call `signalaudit.EmitSignal`. So the cascade walk to holding-subgraph co-members fires immediately (matching `concept:cascade`'s and `concept:auto-terminal`'s held-defer text, both already correct), while the audit-ledger row and the walk to non-member subscribers are genuinely deferred to the auto-terminal handler's Commit/Abandon, which calls the audited, unfiltered `emitSignalInTxWithFilter`. `concept:signal`'s "emits NO terminal signal" sentence was doubly wrong: it denied both the immediate member-filtered cascade walk (which does happen) and mischaracterized what actually is deferred (only non-member delivery and the audit-log write, not the signal's construction or its cascade-firing).

The rules determine the fix and it changes no commitment: `concept:cascade` and `concept:auto-terminal` already state the correct, code-verified mechanism (immediate member-filtered walk, deferred non-member walk); only `concept:signal`'s sentence needed to match, with the added precision (drawn directly from the code, not invented) that the audit-log write is also part of what defers. Repaired per the mechanical-vs-judgment rule's named example.

Changed `.ok-planner/design/concepts/signal.md`: the held-transition sentence in the `terminal/*` taxonomy section now reads that the transition "builds and cascades its terminal signal ... immediately, filtered to holding-subgraph co-members only," with "delivery to non-member subscribers, and the signal's audit-log write, ... both deferred to the auto-terminal handler," replacing the "emits NO terminal signal" / "cascade walk is deferred" claim.

Verified via code reading only (`lib/runtime/runner_terminal.go`, `lib/runtime/signal_emit.go`); docs-only change, no build/test impact.
