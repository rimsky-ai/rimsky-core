---
issue: conflict-instance-kill-audit-row-signal-invariant
kind: audit
category: conflicting
artifacts:
  - concept:signal
  - concept:instance
  - concept:transition-reason
status: repaired
opened: 2026-07-25T03:18:31Z
---

# Does killing an instance write a per-run audit row for every run it kills?

Yes — verified in `lib/runtime/instance_kill.go::forceFailRunInstanceKilled`: for each in-flight run being killed, the handler updates the run's state and then unconditionally attempts `signalaudit.EmitSignal(...)` with a `SettlingSignalInstanceKilled` type-path signal (a warn-log-only failure path, "kill stands," not a skip) — one audit-ledger row per killed run. This matches `concept:signal`'s unconditional-emission invariant and `concept:instance`'s "settling signal ... recorded on each run" claim; only `concept:transition-reason`'s "no audit row" / "not per node-run" sentences were wrong.

The rules determine the fix and it changes no commitment: `concept:signal` and `concept:instance` already state the correct behavior and the code already implements it; `concept:transition-reason`'s narrower, still-valid point (the `instance_killed` *reason value* itself, as distinct from the signal it accompanies, is never written as an audit-event kind) survives untouched. Repaired per the mechanical-vs-judgment rule's named example — aligning a stale sentence to the commitment the code and the counterpart artifacts already agree on.

Changed `.ok-planner/design/concepts/transition-reason.md`:
- "What it is": replaced the "teardown's auditable cause is the single administrative event-log row, not the per-node reason kind (... with no audit row)" sentence with one distinguishing the reason value (never an audit-event kind) from the per-run settling signal it accompanies (which does write one audit row per `concept:signal`'s invariant), plus the administrative `instance_terminated` row on top of, not instead of, the per-run rows.
- Invariants: same correction — the reason value stays a non-audit-event kind; the per-run audit rows and the one administrative row are both now stated as writing.

Verified via code reading only (`lib/runtime/instance_kill.go`); no code change, no build/test impact.
