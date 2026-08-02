---
issue: intent-parked-failed-watchdog-phantom
kind: sprint
category: intent-ledger
artifacts:
  - concept:parked-state
status: answered
opened: 2026-08-01T22:40:50Z
---

# Ledger claims a parked-to-failed watchdog that never shipped and is contradicted by the corpus

## Question

Is a parked node-run's transition to failed gated by a `max_park_duration` watchdog, per the live corpus?

## Answer

No — `concept:parked-state`'s Invariants state plainly: "A parked row is force-failed only by an instance kill, a cross-cutting termination that applies to any in-flight row and is not park-specific machinery." `concept:transition-reason` likewise carries no watchdog mention. Code confirms the mechanism is gone: `max_park_duration_seconds` was dropped by `lib/foundation/persistence/postgres/migrations/025-retire-park-reason-and-watchdog.sql` (and the sqlite equivalent), and the only remaining code hits for `max_park_duration` are tests asserting the key is now rejected as a retired template field. Instance-kill-only is already the corpus's current, correct model; only the historical intent ledger still claims the watchdog.
