---
issue: intent-parked-failed-watchdog-phantom
kind: sprint
category: intent-ledger
artifacts:
  - concept:parked-state
status: open
opened: 2026-08-01T22:40:50Z
---

# Ledger claims a parked-to-failed watchdog that never shipped and is contradicted by the corpus

## Problem

The transition-reason dossier claims parked→failed is legal only via a `max_park_duration` watchdog. No such mechanism exists (`max_park_duration` has zero code hits; migration 025 dropped the park-watchdog columns), and `concept:parked-state` states parked rows are force-failed only by instance kill or cross-cutting termination. The watchdog's retirement is recorded in the parked-state dossier's own resolved conflicts, but the transition-reason dossier was never updated.

Evidence tier: artifact.

## Candidates

- Retire the watchdog claim; instance-kill-only is the ratified model.
- Restore a park-duration watchdog (contradicts the recorded 2026-07-14 retirement ruling).
