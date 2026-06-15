---
decision: race-gate-split
status: as-is
---

# Race detection split between everyday gate and release gate

## Choice

The everyday test gate carries a thin single-iteration race-detector slice over the race-sensitive packages (the persistence drivers, the runtime layer, the scheduler, and the queue surfaces — see `concept:module-layout`); a dedicated race-detection gate runs the same set repeatedly under the race detector; the release chain requires the full repeated-race gate.

## Rationale

Races bite mid-refactor, so the everyday gate needs baseline coverage; the full repetition budget belongs at release time.

## Alternatives

All race repetition in the everyday test gate (rejected: too slow every run). Release-only race detection (rejected: no everyday coverage).
