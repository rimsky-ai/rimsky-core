---
concept: cascade
status: as-is
aliases:
  - reactive-cascade
references:
  - _discover/2026-05-10-cascade-fires-on-last-outcome.md
  - _discover/2026-05-10-frame-resolution-model.md
  - _discover/2026-05-10-state-machine-no-self-loop.md
---

# Cascade

## What it is

Cascade is the engine that turns one node-state transition into the set of downstream node-state transitions. Per the 2026-05-12 nomenclature resolution, three precise words name its parts:

| Word | Meaning | Implementation site |
|---|---|---|
| **walk** | The scheduler-tick-driven traversal of the graph (topology-ordered). The mechanism. | `code:runtime/conductor.go::tick` |
| **propagation** | Cascade-of-stale on `fresh_changed`. Mark dependents stale and recurse. | `code:runtime/cascade_invalidate.go::InvalidateNode` (handler for `concept:invalidate`) |
| **fallthrough** | No-dispatch fresh-roll on `pure_cascade`. Roll fresh state forward without running the node. | Per-node detection in `code:runtime/cascade_recalculate.go::RecalculateNode`; executed by the scheduler's pure-cascade sweep. |

One walk; two node-level behaviors (propagation, fallthrough).

## Purpose

A reactive graph orchestrator only earns its keep if a single executor's "I changed" signal causes the right downstream nodes to recompute and no others. Cascade is the mechanism that turns one terminal outcome into the set of downstream node-state transitions.

## Boundaries

Owns: the firing-gate predicate, the downstream walk, the two node-level behaviors (propagation vs fallthrough). Does NOT own: invalidate emission (see `concept:invalidate`), frame scheduling (see `concept:frame`), terminal-handler resolution (see `concept:lifecycle-handler`). Adjacent: `concept:invalidate`, `concept:last-outcome`, `concept:transition-reason`, `concept:frame`, `concept:lifecycle-handler`.

## Invariants

- Cascade fires iff `last_outcome == fresh_changed` (not the raw `Success.changed`).
- Cascade does not propagate from `parked` or `failed`.
- Cascade always happens in a frame.
- The walk + per-node behaviors are scheduler actions; they are NOT configurable via the per-emit `frame: in | next` discipline.

## Aliases and historical names

The phrase "reactive cascade" appears in sketches and human-facing onboarding docs. Internally, "cascade" is the unambiguous name. Pre-2026-05-12 prose sometimes referred to "two walks"; the current vocabulary is one walk + two node-level behaviors.

## Notes

- Three-word vocabulary (walk / propagation / fallthrough) introduced per `spec:2026-05-12-nomenclature-resolution` (audit cross-layer #10). Resolves `tension:cascade-walks-overloaded`.
