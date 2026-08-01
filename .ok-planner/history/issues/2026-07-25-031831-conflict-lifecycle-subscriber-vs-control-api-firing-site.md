---
issue: conflict-lifecycle-subscriber-vs-control-api-firing-site
kind: audit
category: conflicting
artifacts:
  - concept:lifecycle-subscriber
  - concept:control-api
  - concept:frame
status: repaired
opened: 2026-07-25T03:18:31Z
---

# Which process fires run-scope-terminal for the ordinary-completion case?

The scheduler's frame engine, at settlement — confirmed by code (`lib/runtime/scheduler/scheduler.go` and `lib/runtime/lifecycle_fanout.go` fire it there; `lib/control/controlapi/lifecycle.go` is the separate administrative-termination firing site). `concept:control-api`'s own Boundaries already say control-api does NOT own "the settlement-time root run-scope-terminal fan-out (fired by the scheduler's frame engine when a frame settles)," and `concept:frame` already describes settlement closing the root scope and firing the fan-out "at settlement." Only `concept:lifecycle-subscriber` was the odd one out: its Boundaries and Invariants named control-api as firing "main-scope run-scope-terminal" without qualification, omitting the scheduler's frame-engine firing site for the ordinary-completion case (the common case) entirely.

The rules determine the fix and it changes no commitment: `concept:control-api` and `concept:frame` already state the correct, code-verified firing sites; only `concept:lifecycle-subscriber`'s enumeration needed to match them — a wording-only correction, per the mechanical-vs-judgment rule's named example (aligning a stale sentence to the commitment the code and the counterpart artifacts already agree on). No behavior change, so the parked outbox-decoupling redesign sketch is unaffected and stays parked.

Changed `.ok-planner/design/concepts/lifecycle-subscriber.md`:
- Boundaries: "the underlying state transitions" clause now splits control-api's part into "template/instance events and the administrative-termination run-scope-terminal" and adds "the scheduler's frame engine for the settlement-time root run-scope-terminal" as its own clause; the "three delivery sites" sentence now enumerates four delivery sites across three processes, naming the scheduler's frame engine explicitly for the ordinary-completion case.
- Invariants: same correction — the first invariant now names the scheduler's frame engine as the settlement-time root run-scope-terminal's firing process, distinct from control-api's administrative-termination firing.

Verified via code reading only (`lib/runtime/scheduler/scheduler.go`, `lib/runtime/lifecycle_fanout.go`, `lib/control/controlapi/lifecycle.go`); docs-only change, no build/test impact.
