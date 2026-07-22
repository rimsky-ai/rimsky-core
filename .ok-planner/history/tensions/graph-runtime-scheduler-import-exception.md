---
tension: graph-runtime-scheduler-import-exception
status: open
category: layering
affects:
  - module-layout
---

# Graph layer's runtime-import exception for scheduler sweeps

## What is muddy

The graph layer is otherwise pure (no runtime-layer imports), but a
documented scheduler exception permits the runtime layer to be
imported for sweep entry points so the scheduler tick can drive
runtime sweeps. This leaves a single layering carve-out whose
durability is not settled — the exception either belongs in the
layering rule as a first-class allowance, or the sweep entry point
belongs on the runtime side with the scheduler invoking it through
a narrower seam.

## Evidence

The depguard ruleset enforces graph purity with the named scheduler
exception, and the scheduler tick currently invokes runtime-layer
sweep entry points directly.

## Resolution candidates

- Promote the exception to a first-class part of the layering
  rule, with the seam fully described as graph calling into the
  runtime layer for sweeps and named as such in `decision:depguard-graph-purity-with-scheduler-exception`.
- Invert the seam so the runtime layer owns the sweep entry point
  and the scheduler invokes it through a narrower interface that
  preserves graph purity, retiring the exception.
- Move the scheduler-tick site itself out of the graph layer so
  its runtime-import need no longer requires an exception.
