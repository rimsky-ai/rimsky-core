# Data-platform extensions wishlist — index

**Date:** 2026-05-13
**Status:** index over a suite of related sketches
**Origin:** design conversations pressing rimsky against the data-engineering
shape of consumer workloads.

## What this is

A coordinated set of sketches that, taken together, expand rimsky from "an
orchestration platform that happens to handle data workloads with effort"
into "an orchestration platform whose data-engineering surface is first-
class and ergonomic."

The sketches were produced together because they're related — each addresses
a different facet of the same overall direction. They can be reviewed
together or independently; the inter-sketch dependencies are flagged in
each.

This index exists so future readers see them as a coherent direction rather
than as scattered individual asks.

## The six sketches

### `2026-05-13-blessed-typed-attributes.md`

The conceptual centerpiece. Proposes a **small, bounded, opinionated
standard library of attribute types** — `blob` (evolution of today's blob
backend), `table` (row-oriented dataset with COW versioning), `geo`
(geospatial features with spatial-aware semantics).

Rimsky picks the substrate, owns the implementation, predetermines the
locking and lifetime semantics. Substrate is hidden from consumers above
the attribute surface; the existing claim + producer machinery remains as
the clean escape hatch for substrates the blessed types don't cover.

This is **not** an attempt at full substrate abstraction. Bounded
discipline; substrate-aware tooling; explicit escape hatch.

### `2026-05-13-per-language-executor-sdks.md`

Python and TypeScript SDKs over the existing executor protocol. Hide the
gRPC ceremony; expose a decorator/builder API; resolve blessed-typed-
attribute handles into native types via substrate adapters.

Pairs with the blessed-types sketch (the adapters are per-type, per-
language).

### `2026-05-13-verifier-executor-convention.md`

Collapses today's `concept:quality-rule` into the executor model. Quality
checks become verifier nodes; in-process Go evaluators get deprecated;
bundled verifier executors (`verifier-shape-checks`, `verifier-http`) cover
the common cases; library wrappers (Great Expectations, Soda, Deequ) get
shape they can plug into.

Template authoring sugar (`verifiers:` block) preserves today's readability
while the underlying mechanism becomes uniform nodes.

### `2026-05-13-atomic-staging-pattern.md`

A pattern doc + worked example for custom `ClaimProducer`s with stage-then-
swap-on-Commit semantics. Generic across substrates (Postgres schema swap,
S3 prefix rename, Iceberg branch fast-forward, filesystem directory move,
manifest pointer flip).

Lands as `docs/agents/examples/atomic-staging.md` upstream. Demonstrates
the held-subgraph-Commit/Abandon machinery in a real shape consumers will
build against.

### `2026-05-13-fan-in-conditional-subgraphs.md`

Investigation into ergonomics for conditional subgraph fan-in. Today's
readiness-node + on-event pattern works but is verbose. Proposes a bundled
`barrier` executor as the lowest-commitment first step; flags two foundation-
level directions (optional dependencies, first-fresh-of-set) as candidates
if the bundled executor proves insufficient.

### `2026-05-13-parked-state-dashboard-surface.md`

Small usability extension: typed `parked_reason` enum on the `Snooze`
event, so dashboards and diagnostics can distinguish time-wait, signal-
wait, awaiting-human, barrier-wait, retry-backoff. Low-risk; high-value-
per-line-of-code.

## How the sketches relate

```
              blessed-typed-attributes (centerpiece)
                 /                  \
                /                    \
   per-language-executor-sdks      verifier-executor-convention
        (SDKs surface              (operate against blessed types
         blessed types)             OR claim-backed substrates)

                  +
                  
   atomic-staging-pattern (independent — applies to custom producers,
                            not blessed types; same held-subgraph
                            mechanism though)

                  +

   fan-in-conditional-subgraphs (independent — orthogonal to data shape)
                  |
                  +-- needs parked_reason from
                      parked-state-dashboard-surface
```

## Ordering / phasing across the suite

If the directional commitment lands, work can sequence roughly as:

1. **Blessed typed-attributes design lockdown** (the centerpiece — design
   work, not implementation; informs everything else).
2. **`blob` evolution** as the first blessed type — refactor of existing
   blob backend into the new surface; teaches the implementation pattern.
3. **Python executor SDK** first ship — without typed-attribute adapters
   yet; just the executor-protocol ergonomics.
4. **`table` first ship** + Python adapter — first real data-engineering
   blessed type.
5. **Verifier-executor convention + `verifier-shape-checks` bundled
   executor** — replaces quality-rule for the basic case.
6. **`verifier-http`** + deprecation of `graph/qualityrule/eval/`.
7. **TypeScript executor SDK** first ship + `claude-agent` refactor.
8. **`geo` first ship** + Python adapter.
9. **`barrier` bundled executor** + `parked_reason` extension.
10. **Atomic-staging worked example doc** (can land at any point; consumer-
    facing pattern doc, not blocking other work).
11. **Library wrappers** (`verifier-great-expectations`, etc.) as demand
    emerges.

Each step is reviewable independently; each contributes to the overall
direction.

## What this isn't

- **A v1 commitment.** Pre-v1; break cleanly. The conceptual shape is more
  important than wire-stability through the rollout.
- **A demand to deprecate any existing rimsky surface.** Claims, producers,
  the executor protocol — all stay. Blessed types and verifier executors
  are additive (with one exception: `graph/qualityrule/` eventually
  retires once `verifier-shape-checks` is stable).
- **An attempt at full substrate abstraction.** Reread the
  blessed-typed-attributes sketch — the bounded-discipline framing is the
  load-bearing observation. Don't bless types we can't make excellent;
  don't paper over substrate quirks the consumer needs to know about.
- **A fixed plan.** These are wishlist sketches. The actual brainstorm /
  spec / plan cycles per sketch are where the work hardens.
