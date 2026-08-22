---
concept: breakpoint
---

# Breakpoint

## What it is

A breakpoint is a pause-point an operator installs on a live instance while that instance runs (see `concept:instance`). It carries a stable identity and four settings. A matcher names the dispatches it applies to. A checkpoint position says where in the supervisor's handling of a dispatch it fires: before the executor runs, or after the dispatch reaches a terminal outcome. A mode says whether a hit blocks the runner or only records. A policy says what to do with hits that arrive faster than an operator drains them. A breakpoint may also carry a lifetime after which it deletes itself, and a filter on the kind of settling signal it observes. rimsky keeps the installed breakpoints in a per-instance ledger and the hits they record in a second one.

## Purpose

A breakpoint lets an agent debug a running instance. The agent installs breakpoints at the dispatches it cares about, pauses execution, inspects the snapshot the hit records, and amends the paused dispatch through a one-shot overlay before resuming it. An unresumed pause also opens `concept:control-api`'s debug-override channel, through which the agent writes node attributes and invalidates nodes against the paused instance — a persistent mutation path, separate from the one-shot resume overlay. A breakpoint is the runtime-cooperative half of the debugger surface `concept:control-api` exposes; the instance-level hold is the other half (see `concept:instance`).

## Boundaries

A breakpoint owns the two ledgers, the evaluation of a matcher at each checkpoint, the recording of a hit, the overlay a resume feeds into the paused dispatch, and the policy that governs undrained hits. The overlay is the last layer over a dispatch's attribute bag, applied after the override merge that `concept:attribute` owns.

The matcher grammar belongs to `concept:attribute`, which shares it with the by-match overrides. A breakpoint matcher accepts any executor the deployment declares, where an attribute override accepts only an executor the template uses (see `decision:breakpoint-matcher-executor-scope-permissive`). Delivering a hit to the operator belongs to `concept:control-api`: this concept owns the ledger, not the transport. Invoking evaluation at each checkpoint, and holding a blocked runner until a resume arrives, belong to `concept:supervisor`: this concept owns what happens when a checkpoint is evaluated, not where in the dispatch flow the evaluation is invoked from. A pause the executor itself asks for is `concept:parked-state`; every breakpoint is operator-injected at runtime and no template declares one. The audit record of the breakpoint surface's own calls belongs to `concept:event-log`.

see also: `supervisor`, `control-api`, `attribute`, `instance`, `signal`, `permission`, `parked-state`, `event-log`
