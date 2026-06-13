---
decision: race-gate-split
status: as-is
---

# Race detection split between everyday gate and release gate

## Choice

The everyday test-all gate carries a thin `-race -count=1` slice over the race-sensitive packages (the Postgres and SQLite persistence drivers, the runtime layer, the scheduler, and the queue paths); a dedicated test-race target runs `-race -count=3` over the same set; the release chain requires the full test-race target.

## Rationale

Races bite mid-refactor, so the everyday gate needs baseline coverage; the full repetition budget belongs at release time.

## Alternatives

All race repetition in test-all (rejected: too slow every run). Release-only race detection (rejected: no everyday coverage).
