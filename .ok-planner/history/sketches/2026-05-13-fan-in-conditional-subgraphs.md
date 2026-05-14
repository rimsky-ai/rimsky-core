# Fan-in for conditional subgraphs

**Date:** 2026-05-13
**Status:** sketch / investigation
**Companion sketches:** (none direct)

## The problem

Rimsky `concept:node` dependencies are strict-AND: a node becomes eligible
for dispatch when **all** its declared dependencies are `fresh`. There's no
"any-of" semantics, no optional dependencies, no soft dependencies.

This works cleanly for fixed-shape pipelines where every node always runs.
It works less cleanly for templates where some subgraphs are
**conditionally activated** — present in the template, but only active for
some instances or some runs.

Concrete shape that exposes the friction:

```
intake → spine_a → spine_b → ... → spine_z → finalize
            ↓
        optional_subgraph_1 → optional_check_1
            ↓
        optional_subgraph_2 → optional_check_2
```

`finalize` wants to depend on all of `spine_z`, `optional_check_1`,
`optional_check_2` — *but only when those optional subgraphs were
applicable for this run*. If they're not applicable, `finalize` should
proceed without them.

Today's options are workable but verbose. This sketch surveys the options
and proposes investigation into whether a primitive earns its place.

## Today's pattern: readiness nodes + parked

The rimsky-native answer today, using existing primitives:

1. The optional subgraph has no spine dependency. Its entry node has
   `dependencies: []`. It's only reached via an `on_event` invalidate
   from `intake` (the node that knows the routing decision).
2. `intake`'s `on_event` handler fires `invalidate: { targets:
   [optional_subgraph_entry] }` for each applicable subgraph.
3. A **readiness node** sits between the spine and `finalize`:
   - Hard dep on `spine_z` (always required).
   - `on_event` handlers listening for completion signals from optional
     subgraphs.
   - On dispatch, reads `intake.value.applicable_subgraphs`. If empty,
     completes immediately. If non-empty, emits `Snooze` (parked) and waits
     for completion-signal invalidates from each applicable subgraph.
   - When all applicable subgraphs have signaled, the readiness node's
     resume dispatch completes, propagating to `finalize`.

This works. It uses existing primitives. It's correct. But it requires the
template author to design and implement a readiness node with non-trivial
state-machine logic, and the resulting graph has an extra node per fan-in
point with subtle wiring.

For workflows with many conditional subgraphs and many fan-in points, this
pattern multiplies — every fan-in needs a custom readiness node, or a
generic "wait for these named completion events" executor.

## Candidate primitives

Three directions to investigate.

### Direction A: optional dependencies

Template DSL extension:

```yaml
nodes:
  - type: finalize
    dependencies:
      - spine_z              # required
      - optional_check_1?    # optional — wait if running, ignore if never invalidated
      - optional_check_2?    # same
```

Semantics:

- An optional dependency that's currently `running` or about to be eligible
  must complete before the dependent runs.
- An optional dependency that's `fresh` (whether changed or unchanged) is
  treated like a normal dep.
- An optional dependency that's `stale` AND no upstream has scheduled it
  for dispatch (no invalidate has reached it, no `on_event` will reach it)
  is treated as if it doesn't exist.

The hard question: how does rimsky distinguish "stale and pending dispatch"
from "stale and never going to run"? Today's state machine doesn't
encode this — `stale` means "needs recalculation"; if no eligibility path
exists, the node sits there indefinitely.

Possible mechanism: a third state, `inert` or `unreachable`, computed by
the scheduler when it can prove no invalidate path exists to this node.
This is a graph-reachability analysis on every scheduling tick — non-
trivial but tractable.

Or: explicit `inert` declaration via lifecycle handler. If the upstream
that would normally invalidate this node decides not to, it sends an
explicit "this node is inert for this run" signal. Simpler at the
mechanism level; pushes the decision to the upstream.

### Direction B: first-fresh-of-set

```yaml
nodes:
  - type: finalize
    dependencies:
      - spine_z              # required
    dependencies_any_of:
      - [skipped_path, completed_path]   # at least one must be fresh
```

Semantics:

- All `dependencies` must be fresh (today's strict-AND).
- For each `dependencies_any_of` group, at least one member must be fresh.

Use case: explicit either-or routing. `intake` decides which branch runs;
both branches end at a synthetic terminator (or both feed `finalize`
directly with `dependencies_any_of`). Whichever branch completes first
satisfies the any-of group.

Limitation: doesn't compose well with multiple optional subgraphs that
might all run (where we want `finalize` to wait for *all* applicable ones,
not just one).

### Direction C: explicit completion signals + barriers

A new node type: `barrier`. Userdata declares "wait for these named
completion signals to fire before completing":

```yaml
nodes:
  - type: barrier
    executor: barrier
    dependencies: [spine_z]
    userdata:
      wait_for: [optional_check_1.completed, optional_check_2.completed]
      timeout_seconds: 300    # optional
      on_timeout: proceed     # or fail
    inputs_from: [intake]
    cascade_when:
      # only wait if intake declared the corresponding subgraph applicable
      filter: applicable_subgraphs
```

A `barrier` is essentially the readiness-node pattern as a bundled executor.
It reads upstream attributes to determine which signals it should wait for,
parks until those signals arrive (via `on_event` handlers attached to the
barrier itself), then completes.

Direction C is the "ship the readiness pattern as a first-class executor"
move. Doesn't change rimsky's foundation; ships a bundled executor with
known semantics.

## Trade-offs

**Direction A (optional dependencies)**:
- Most ergonomic for template authors.
- Substantial foundation work: graph-reachability analysis, new state
  semantics, scheduling logic changes.
- Risk of subtle bugs in reachability analysis under cascade interactions.
- Once shipped, it's a permanent foundation feature.

**Direction B (first-fresh-of-set)**:
- Simpler foundation work — small scheduler change.
- Limited use case coverage (any-of, not all-applicable).
- Composes awkwardly with multi-subgraph fan-in.

**Direction C (barrier executor)**:
- No foundation changes; just a bundled executor.
- Slightly more verbose at the template level.
- The complexity is in the barrier executor's userdata schema, which can
  be evolved without protocol changes.
- Lowest commitment level — if it doesn't earn its place, ship a better
  primitive later.

## Recommended investigation

Try Direction C first. Ship a bundled `barrier` executor that implements
the readiness-node pattern with a clean userdata schema. Use it across a
real consumer workload with multiple conditional subgraphs. Measure:

- How often template authors reach for it.
- How readable the resulting templates are.
- What edge cases the userdata schema misses.

If the bundled `barrier` proves ergonomic enough, the foundation-level
work for Direction A may never need to happen. If template authors
consistently want syntactic sugar that hides the barrier behind cleaner
dependency notation, that's a signal that Direction A earns its place;
the bundled `barrier` becomes the underlying mechanism, and the optional-
dependency syntax desugars to barriers at canonicalization time.

If neither Direction A nor C feels right after consumer experience, revisit.

## Why not just declare conditional dependencies via on_event everywhere?

The existing `on_event` mechanism does cover this — it's exactly the
readiness-pattern building block. The friction isn't "rimsky can't express
this"; it's "expressing this requires the template author to be a graph
designer and a state-machine designer simultaneously, every time."

A bundled `barrier` executor centralizes the state-machine design once,
documents it, exposes it via a userdata schema, and frees template authors
to declare "wait for these signals" without having to implement the
waiting themselves.

## Open design questions

1. **Signal semantics.** What counts as a "completion signal" — a named
   event emitted by the upstream subgraph's terminal node? A `fresh`
   transition on a specific named upstream? Both? Designing the signal
   vocabulary is the bulk of Direction C's design work.
2. **Timeout behavior.** What happens when a barrier times out? Fail the
   instance? Proceed with whatever signals fired? Both should be
   expressible via userdata.
3. **Idempotency under invalidate.** If an upstream subgraph is invalidated
   after the barrier already saw its completion signal, what happens?
   Probably: the barrier should re-park and wait again. Needs explicit
   semantics.
4. **Composition with held subgraphs.** Can a barrier be a member of a
   holding subgraph? If yes, what happens at auto-terminal — does the
   barrier's parked state delay the holding-subgraph aggregate outcome?
   Probably yes, but needs explicit modeling.
5. **Relationship to `concept:parked` heartbeating discipline.** The
   barrier is parked while waiting. `concept:parked` says parked nodes
   don't heartbeat, the orphan reaper skips them, held claims persist.
   The barrier pattern fits this model — verify no surprises.

## Phasing

**Phase 1**: design the `barrier` executor's userdata schema and signal
vocabulary. Walk through several real consumer scenarios; pressure-test the
shape.

**Phase 2**: ship `executors/barrier/` as a bundled Go executor.
Conformance run. Worked example.

**Phase 3**: observe consumer usage. Decide whether to lift to Direction A
or stay with the bundled executor.
