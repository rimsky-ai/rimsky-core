---
resolved_by: spec:2026-05-14-subscription-cascade-and-quality-of-life-design
tension: send-vs-subscribe-asymmetry
category: directionality
status: resolved
affects:
  - lifecycle-handler
  - error-policy
  - subscription
  - invalidate
---

# Push-style `invalidate.targets` coexisted with pull-style `dependencies:`

## What was muddy

Pre-2026-05-14, reactive coupling was declared in two opposite directions:

- **Pull-style** (receiver declares): `dependencies: [A, B, C]` on receiver R meant "I'm coupled to A, B, C; mark me stale when they change." The dependency edge is impactee-side.
- **Push-style** (sender declares): `on_executor_complete.invalidate.targets: [X, Y]` on sender S meant "when I complete, invalidate X and Y." The targets-list edge is impacter-side. The same shape appeared on `on_acquire_unavailable`, `on_executor_errored`, `on_event` entries, and `error_types: action: invalidate`.

The two surfaces coexisted on the same node-pair. A template author wanting "R fires after S's failure" could declare either `S.error_types.foo: action: invalidate, targets: [R]` (push) or `R.dependencies: [S]` plus a state-watch (no clean way pre-spec). Operators couldn't tell which surface the runtime would consult; both could fire, and the resulting cascade was hard to predict.

## Why it mattered

Two declarations of the same coupling meant two places to keep in sync, two failure modes, two error messages. Template authors picked one based on which file they were editing rather than a principled choice. Operators reading a stuck instance had to grep two surfaces to find the coupling.

## Resolution

Resolved by `.ok-planner/specs/2026-05-14-subscription-cascade-and-quality-of-life-design.md`. The send-style surfaces retire across the lifecycle-handler family and `error_types`:

- `on_acquire_unavailable.invalidate`, `on_executor_complete.invalidate`, `on_executor_errored.invalidate` retire.
- `on_event:` map retires entirely (replaced by receiver-side `subscribes: [{node, on: event, name}]`).
- `error_types: action: invalidate` retires (rejected by the validator with a migration message).

Receiver-side `subscribes:` is the single surface for declaring reactive coupling. Operators reading "who fires when S fails?" grep one place: the `subscribes:` blocks across the template.
