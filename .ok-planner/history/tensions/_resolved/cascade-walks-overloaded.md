---
resolved_by: spec:2026-05-12-nomenclature-resolution
tension: cascade-walks-overloaded
category: overloaded
status: open
affects:
  - cascade
---

# "Cascade" covers two distinct walks (cascade-on-terminal and pure-cascade)

## What is muddy

`cascade` in code and prose refers to two distinct downstream walks:

- **Cascade-on-terminal** (`cascadeChildrenStaleInTx` + `fanoutRecalculate`) — fires on `last_outcome == fresh_changed`. Marks dependents `stale`.
- **Pure-cascade** (`cascade_recalculate.go`) — rolls `stale → fresh` when all upstreams resolved `fresh_unchanged`. No executor invocation.

Both are part of the same engine but have different triggers and different effects. `docs/concepts/cascade.md` describes both under the single word.

## Why it matters

A reader debugging "why did this cascade fire" needs to know which walk. A developer extending the engine has to keep both code paths in mind. The single word obscures the bifurcation.

## Resolution candidates (do NOT pick)

- Rename one of them in prose: e.g., "cascade-propagation" vs "cascade-recalculation".
- Keep one name and clarify the two phases inline at all citation sites.
- Treat them as two concepts with distinct files.

## Evidence

- `_discover/2026-05-10-cascade-fires-on-last-outcome.md` — cascade-on-terminal description.
- `foundation/integration/cascade_invalidate.go` vs `foundation/integration/cascade_recalculate.go`.
- `docs/concepts/cascade.md` "lazy + last_outcome-driven" framing.

