---
issue: intent-cascade-what-it-is-stale-walk-vocabulary
kind: sprint
category: intent-ledger
artifacts:
  - concept:cascade
status: repaired
opened: 2026-08-01T22:41:40Z
---

# concept:cascade's What-it-is section still described the retired walk model

Question: does the walk described in `concept:cascade`'s What-it-is table (scheduler-tick-driven, topology-ordered traversal) match how cascade fires today?

No. Code fires the walk (`cascadeSignalInTxWithFilter` / `cascadeSubscribersStaleInTx`, `lib/runtime/runner_terminal.go`, `lib/runtime/child_execution.go`, tagged `@concept: cascade`) inline inside the transaction that settles the triggering terminal or attribute-changed signal — not on a scheduler tick. Only the fallthrough behavior's actual node advancement (no-dispatch fresh-roll for executor-less nodes) runs off a tick, via the separate `ProcessPureCascade` sweep (`lib/runtime/scheduler/pure_cascade.go`). `concept:cascade`'s own Invariants section already stated this correctly ("The cascade walker operates entirely within a single frame. It never creates a new frame..."), so the What-it-is table's "scheduler-tick-driven traversal (topology-ordered)" framing for **walk** contradicted the same file's Invariants — a corpus-side repair under `{{MECHANICAL-VS-JUDGMENT-RULE}}` (aligning a stale sentence to the commitment the file's own Invariants section and the code already agree on), not a commitment change.

Repaired `.ok-planner/design/concepts/cascade.md`'s What-it-is table: **walk** now reads as the event-driven traversal fired inline inside the settling transaction; **fallthrough** now names the pure-cascade sweep as tick-driven explicitly (it already did); added a closing sentence distinguishing the event-driven walk from the tick-driven fallthrough sweep. Verified: the rewritten section is textually consistent with the Invariants section's "operates entirely within a single frame" / "never creates a new frame" language and with the code call sites cited above (`grep -n "@concept: cascade" lib/runtime/runner_terminal.go`).
