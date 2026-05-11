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

Cascade is the engine that propagates "this node changed" downstream. Implemented in `foundation/cascade/` and `foundation/integration/cascade_invalidate.go` + `cascade_recalculate.go`. Comes in two flavors: a cascade-on-terminal walk (stale-marking downstream on `fresh_changed`) and a pure-cascade walk (rolling `stale → fresh` for nodes whose upstreams all resolved `fresh_unchanged`).

## Purpose

A reactive graph orchestrator only earns its keep if a single executor's "I changed" signal causes the right downstream nodes to recompute and no others. Cascade is the mechanism that turns one terminal outcome into the set of downstream node-state transitions.

## Boundaries

Owns: the firing-gate predicate, the downstream walk, the two propagation modes (changed vs pure). Does NOT own: invalidate emission (see `invalidate`), frame scheduling (see `frame`), terminal handler resolution (see `lifecycle-handler`). Adjacent: `node`, `last-outcome`, `frame`, `invalidate`, `lifecycle-handler`.

## Invariants

- Cascade fires iff `last_outcome == fresh_changed` (not the raw `Complete.changed`).
- Cascade does not propagate from `parked` or `failed`.
- Cascade always happens in a frame (`docs/concepts/cascade.md`).
- Cascade-on-commit and pure-cascade walks are scheduler actions and are NOT configurable via the per-emit `frame: in | next` discipline.

## Aliases and historical names

The phrase "reactive cascade" appears in sketches and human-facing onboarding docs. Internally, "cascade" is the unambiguous name.

## Open within this concept

- The "cascade" word covers two distinct walks (cascade-on-terminal and pure-cascade) — overloaded. See `tensions/cascade-walks-overloaded.md`.

