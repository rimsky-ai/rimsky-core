---
decision: race-gate-split
status: as-is
---

# Race detection split between everyday gate and release gate

## Choice

The everyday test gate carries a thin single-iteration race-detector slice over the race-sensitive packages (the persistence drivers, the runtime layer, the scheduler, and the queue surfaces — see `concept:module-layout`); a dedicated race-detection gate repeats the race detector over the load-bearing subset of that surface — the runtime layer and the scheduler; the persistence drivers are deliberately excluded from repetition since their race surface is mostly driver contention rather than Go data races, and the everyday gate's single-iteration slice already covers them on every run. The release chain requires the full repeated-race gate.

## Rationale

Races bite mid-refactor, so the everyday gate needs baseline coverage; the full repetition budget belongs at release time.

## Alternatives

All race repetition in the everyday test gate (rejected: too slow every run). Release-only race detection (rejected: no everyday coverage).
