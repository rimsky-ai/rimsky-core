# Rimsky in the orchestration ecosystem

This document positions rimsky relative to other orchestration and
data-engineering platforms. It exists for two audiences:

- **External evaluators** — engineers deciding whether rimsky fits
  their workload, looking for an honest read on where it overlaps with
  tools they already know and where it diverges.
- **Internal contributors** — designers weighing whether a proposed
  primitive earns its place in rimsky, looking for a rubric grounded
  in how the broader ecosystem has navigated the same shape.

The framing is descriptive, not promotional. Each comparator is a
mature, well-respected platform; rimsky's divergences are deliberate
choices about scope, not implied criticisms of the comparators.

## Rimsky's identity

Before comparing, name what rimsky is.

Rimsky is a **reactive node-graph orchestrator** with:

- **Content-addressed templates** that bind to instances at creation.
  Template hashes are stable; tags are movable aliases.
- **Cascade as the core animation primitive.** Node-state propagation
  is the engine; recalculation is a scheduler action rather than a
  service message.
- **Held-subgraph aggregate-outcome resolution.** A subgraph stages
  work behind a held claim; aggregate-success commits, any-failure
  abandons.
- **Out-of-process service protocols** (claim producers, executors,
  lifecycle subscribers) speaking gRPC over Postgres-mediated state.
- **Three independent runtime processes** (scheduler, supervisor,
  control-api) that communicate only through Postgres.

The shape it's most directly comparable to: **Airflow / Dagster /
Prefect / Temporal**. Streaming engines (Beam, Flink, Spark) and pure
transformation frameworks (dbt) overlap on adjacent concerns but
aren't drop-in alternatives.

## At-a-glance matrix

Where rimsky lands today, across the dimensions that distinguish
mature orchestrators. ✅ = first-class, 🟡 = workable pattern but not
first-class, ❌ = not in scope or not present, ⏳ = planned.

|                                      | Airflow | Dagster | Prefect | Temporal | dbt | rimsky |
| ------------------------------------ | :-----: | :-----: | :-----: | :------: | :-: | :----: |
| Graph / workflow primitive           |   ✅    |   ✅    |   ✅    |    ✅    | ✅  |   ✅   |
| Scheduled runs (cron)                |   ✅    |   ✅    |   ✅    |    🟡    | 🟡  |   ✅   |
| Ad-hoc / triggered runs              |   ✅    |   ✅    |   ✅    |    ✅    | ✅  |   ✅   |
| Sensors / external triggers          |   ✅    |   ✅    |   ✅    |    ✅    | ❌  |   🟡 / ⏳  |
| Partitions as first-class            |   🟡    |   ✅    |   🟡    |    ❌    | 🟡  |   ⏳   |
| Asset / lineage as the model         |   ❌    |   ✅    |   ❌    |    ❌    | ✅  |   🟡   |
| Materialization strategies           |   ❌    |   🟡    |   ❌    |    ❌    | ✅  |   ⏳   |
| Data quality tests                   |   🟡    |   ✅    |   ❌    |    ❌    | ✅  |   ⏳   |
| Backfills as a parametrized op       |   ✅    |   ✅    |   ✅    |    ❌    | 🟡  |   ⏳   |
| Durable workflow state               |   🟡    |   🟡    |   🟡    |    ✅    | ❌  |   ✅   |
| Concurrency gating / claims          |   🟡    |   ❌    |   🟡    |    🟡    | ❌  |   ✅   |
| Held-subgraph stage-then-promote     |   ❌    |   ❌    |   ❌    |    ❌    | ❌  |   ✅   |
| Out-of-process workers               |   ✅    |   ✅    |   ✅    |    ✅    | ❌  |   ✅   |
| Content-addressed graph definitions  |   ❌    |   🟡    |   ❌    |    🟡    | ❌  |   ✅   |
| Cron-style schedule advances on row  |   ❌    |   ❌    |   ❌    |    n/a   | n/a |   ✅   |
| Stream / window / watermark          |   ❌    |   ❌    |   ❌    |    ❌    | ❌  |   ❌   |

The two rows where rimsky is distinctively ahead are
**held-subgraph aggregate-outcome resolution** and
**content-addressed graph definitions**. The rows where it's
distinctively behind are **partitions**, **asset thinking**,
**materialization strategies**, **data quality**, and **backfills**.
The data-platform and partitions work on the roadmap closes most of
those gaps.

## Per-framework treatment

Grouped by adjacency to rimsky's shape. The most directly comparable
platforms first.

### Airflow

The original workflow-as-DAG orchestrator. Load-bearing primitives:
DAGs, scheduled and ad-hoc runs, **sensors** (poll external state and
trigger), **XCom** (small inter-task data), **hooks** (clients to
external systems), **connections**, **operators** (work units),
parameter-driven runs, **backfills** as a first-class operation.

Where rimsky overlaps: DAG + templates + instances + admin force-fire
covers the same ground for most cases. Cascade gives rimsky a stronger
story for "node X changed; recompute its dependents" than Airflow's
manual re-runs.

Where rimsky diverges: rimsky has nothing equivalent to **sensors**
yet (a planned roadmap item). Operators-as-Python-classes are
in-process; rimsky's executors are out-of-process gRPC services.
Airflow's XCom is essentially the same shape as rimsky's small typed
attributes; rimsky's blob backend (and the planned blessed typed
attributes) handle the larger cases that Airflow punts to external
storage.

### Dagster

Reframes the orchestration primitive from task-or-op to **asset** —
the graph IS the lineage graph. Other load-bearing primitives:
**ops** (work units), **partitions** (slice an asset by dimension),
**IOManagers** (per-asset I/O abstraction), **sensors**, **schedules**,
**observability surface** (asset history, materialization records,
dependency views), **backfills** targeting partition ranges,
**code locations** plus code versioning.

Where rimsky overlaps: cascade + nodes + claims-as-IOManager-shape +
content-addressed templates give rimsky most of what Dagster's
asset-thinking gives, but with different vocabulary. Both center the
graph as the lineage graph.

Where rimsky diverges: rimsky's public surface is task-shaped (nodes
and their attributes) where Dagster's is asset-shaped (named outputs
and their materializations). Rimsky lacks partitions as first-class
(planned) and the rich observability surface Dagster ships out of the
box. Asset-thinking is on the roadmap as a presentation layer, not a
primitive change.

### Prefect

A more recent take on workflow orchestration, oriented around
**flows**, **tasks**, **deployments** (parametrized flow + trigger
pairings), runtime state semantics, **blocks** (reusable configured
infrastructure), and **runners**.

Where rimsky overlaps: templates and instances cover most of what
Prefect's flows-plus-deployments do. Claim-producers-as-blocks is a
reasonable mapping.

Where rimsky diverges: rimsky's discipline around content-addressed
templates and held-subgraph resolution doesn't have a Prefect
equivalent. Prefect's hosted offering and managed runners aren't
rimsky concerns — rimsky ships as Docker images, a Go module, or a
git submodule for the consumer to deploy.

### Temporal

A different shape — workflow-as-durable-execution. Load-bearing
primitives: **workflows** (durable executions that survive process
restarts), **activities** (RPC-shaped units of work), **signals**
(external events into workflows), **queries** (read workflow state),
**versioning** (run old workflow versions to completion; new versions
start fresh), **patches** (in-flight migration support).

Where rimsky overlaps: instances-as-workflows, executor-dispatch-as-
activities, admin-invalidate-as-signals. Rimsky's content-addressed
templates plus movable tags cover most of what Temporal's versioning
covers.

Where rimsky diverges: Temporal is event-sourced; rimsky is
Postgres-state-backed. Temporal's durable execution model means
workflow code itself is the source of truth; rimsky's templates are
declarative graph definitions. Temporal has no equivalent of cascade
or held-subgraph resolution. Rimsky has no equivalent of Temporal's
mid-flight patch-based migration (and explicitly chooses not to —
the content-hash-plus-tag pattern is the rimsky-native answer).

### dbt

A transformation framework, not an orchestrator. Load-bearing
primitives: **sources** (declared external inputs), **models**
(transforms), **tests** (declarative data assertions), **seeds**
(static reference data), **snapshots** (historical slowly-changing-
dimension capture), **materializations** (table, view, incremental,
ephemeral, snapshot), **exposures** (downstream consumers),
**semantic layer** (metric definitions).

Where rimsky overlaps: nodes-as-models is the obvious mapping.
Verifier-executors-as-tests covers the data-quality surface (planned).
The blessed typed-attribute work makes nodes-that-produce-tables a
first-class shape comparable to dbt models.

Where rimsky diverges: dbt is SQL-warehouse-native; rimsky is
data-store-agnostic. dbt's incremental materializations are richer
than anything rimsky has today (planned for after partitions). dbt's
semantic layer is application-level — out of rimsky's scope. dbt's
snapshots have no direct rimsky analog.

The two often complement each other: rimsky orchestrates the broader
graph (extract, load, dbt-run, downstream-publish); dbt runs the SQL
transformation slice.

### Beam, Spark, Flink

Streaming and batch data-plane engines. Beam is a unified streaming
model with pluggable runners. Spark is the dominant batch-with-some-
streaming engine (RDDs, DataFrames, partitions, narrow vs wide
dependencies, shuffle, Catalyst query planning). Flink is the dominant
exactly-once streaming engine (stream operators, keyed state,
watermarks, savepoints, event-time windowing).

These are **data-plane** systems, not orchestrators. They live below
rimsky's layer of concern: rimsky orchestrates the graph that includes
"run a Spark job"; Spark runs the job.

Where rimsky overlaps: zero, by design. An executor that invokes
Spark, Flink, or Beam from a rimsky node is the integration shape.

Where rimsky diverges: comprehensively. Rimsky's invocation model is
discrete dispatch; these engines' invocation models are
continuously-running operators. Rimsky deliberately doesn't absorb
windowing, watermarks, late-data semantics, or per-key state. The
partitions concept is the only one of these that generalizes well
into orchestrator-shape — rimsky's planned partitions work draws from
Dagster's shape, not Spark's.

## Cross-cutting concept comparison

Per primitive, where each platform lands. Helpful when evaluating
"does rimsky's [concept] do what I expect?"

### Graph definition

- **Airflow**: Python DAG code, imported and parsed at scheduler
  startup.
- **Dagster**: Python asset definitions with optional code locations.
- **Prefect**: Python flow code with deployments binding flow +
  parameters + triggers.
- **Temporal**: Workflow and activity code as the source of truth.
- **dbt**: SQL files plus YAML schema.
- **rimsky**: Declarative template YAML, content-hashed, registered
  via control-api. Hashes pin behavior; tags move.

The split: rimsky and dbt are declarative; the others derive the
graph from imperative code. The trade-off is well-trodden — rimsky's
declarative position simplifies content addressing and makes
multi-language executors natural (the template doesn't bind to one
runtime), at the cost of less code-shaped expressiveness.

### Concurrency gating

- **Airflow**: Pools (named queues with limited concurrency). Coarse.
- **Dagster**: Run-level concurrency limits; less granular than
  Airflow pools.
- **Prefect**: Concurrency limits as block-style configuration.
- **Temporal**: Workflow-level signals are the closest equivalent;
  not a gating primitive.
- **dbt**: Not applicable (dbt doesn't dispatch; the warehouse
  handles concurrency).
- **rimsky**: Claims plus claim producers. A node declares the
  scope-shaped state it intends to use; the claim producer's conflict
  matrix arbitrates. Multi-claim acquisition is atomic and uses
  deterministic sort order to prevent deadlock.

Claims plus held-subgraph resolution is rimsky's most distinctive
primitive. None of the comparators have a direct equivalent.

### Lineage and observability

- **Airflow**: Run history per task instance; weak structural
  lineage.
- **Dagster**: Asset materialization history with full structural
  lineage; rich observability UI.
- **Prefect**: Run state UI; less lineage-centric than Dagster.
- **Temporal**: Workflow event history (the durable execution log
  IS the lineage).
- **dbt**: Lineage graph from model dependencies; `dbt docs`
  generates a queryable view.
- **rimsky**: Cascade graph plus events log. Structural lineage is
  the cascade graph. Content lineage (what specific values produced
  this value) is on the roadmap, not yet present.

Rimsky has the structural lineage for free (cascade walks it), but
lacks Dagster's polished observability UI and dbt's auto-generated
lineage docs.

### Tests and data quality

- **Airflow**: Operator-shaped check tasks; ergonomics are weak.
- **Dagster**: Asset checks plus IOManager-level expectations.
- **Prefect**: Notification-shaped patterns; no first-class testing.
- **Temporal**: Not in scope.
- **dbt**: Declarative tests (unique, not_null, accepted_values,
  relationships) plus generic tests.
- **rimsky**: Today's `quality-rule` primitive (planned for collapse
  into verifier executors). Held-subgraph resolution gives the
  "bad data never reaches canonical state" guarantee that dbt and
  Dagster achieve via runtime checks.

Once the verifier-executor convention lands, rimsky's testing surface
will be more flexible than dbt's (language-agnostic, executable
out-of-process) but at the start it will have fewer batteries-
included checks than mature dbt installations.

### Partitions

- **Airflow**: Param-driven; awkward.
- **Dagster**: First-class; partition spec attaches to assets;
  backfills target partition ranges; UI displays per-partition state.
- **Prefect**: Param-driven; awkward.
- **Temporal**: Not applicable.
- **dbt**: Materialization-config level (e.g. `partition_by` for
  BigQuery models); first-class for the warehouses that support it.
- **rimsky**: Not present; on the roadmap as the single largest
  pending extension.

Partitions are the clearest gap. Closing it is roadmap work, and
rimsky's eventual shape will most resemble Dagster's.

### External triggers

- **Airflow**: Sensors as first-class — poll-and-trigger building
  block.
- **Dagster**: Sensors as first-class.
- **Prefect**: Sensors plus webhook deployments.
- **Temporal**: External signals into workflows.
- **dbt**: Out of scope; relies on external orchestration.
- **rimsky**: Achievable today via a polling executor that parks and
  resumes; not first-class. Planned as a roadmap item, lower-
  commitment first move as a bundled executor with a conventional
  expected-attributes schema.

## Scope test heuristics

When evaluating whether a new primitive earns its place in rimsky,
the following questions apply. These are the rubric — useful for both
external readers ("is rimsky going to absorb X?") and internal
design discussions ("should we propose X?").

### 1. Is this a control-plane concern or a data-plane concern?

Rimsky orchestrates the graph. Data-plane concerns — windowing,
watermarks, per-key state, query planning, shuffle — live in the
engines rimsky dispatches to. Anything that pulls rimsky into
operating on data values rather than orchestrating around them is
out of scope.

**Examples of "yes, control plane"**: partitions (slicing the unit
of work), sensors (deciding when to trigger work), materialization
strategies (deciding the shape of writes).

**Examples of "no, data plane"**: windowed aggregation,
exactly-once stream processing, distributed shuffle.

### 2. Is it a primitive, a pattern, a worked example, or
documentation-only?

The default answer for any new ask is **doc**. The next default is
**worked example**. Then **pattern**. Then **bundled executor or
bundled producer**. Only after a clear pattern emerges does the
question of **primitive** come up.

A primitive that could have been a pattern is overhead. A pattern
that could have been an example is overhead. Drop one level if
unsure.

### 3. Can rimsky make it excellent, or will it be half-baked?

This applies most directly to blessed typed-attribute candidates but
generalizes. If rimsky absorbs a primitive, it commits to making it
load-bearing. Half-baked primitives accumulate as dead weight because
consumers route around them.

**Test**: would a serious consumer prefer rimsky's implementation
over rolling their own? If unclear, defer the primitive and keep the
escape hatch open.

### 4. Does it earn its place across multiple consumer shapes, or is
it consumer-specific?

Rimsky is project-agnostic. Templates and examples use generic names.
A primitive that earns its place only for one consumer's shape
doesn't belong in core — it belongs in that consumer's executors,
producers, or templates.

### 5. Is the escape hatch sufficient?

Rimsky has three escape hatches for almost any structural ask:

- Custom executors over the existing protocol.
- Custom claim producers over the existing protocol.
- Worked-example patterns that compose existing primitives.

If a primitive's only justification is "this is what most
orchestrators do," but the existing escape hatches handle real
consumer cases reasonably well, prefer to leave it out and document
the pattern.

### 6. Does it close optionality, or open it?

Pre-v1 the platform breaks freely. Once v1 ships, every primitive
becomes wire-stable. New primitives should expand the platform's
reach without locking in shapes that future consumers will resent.

**Open-optionality moves**: bundled executors with declared
capabilities, new claim-producer protocols, new typed-attribute
types within the bounded standard library.

**Closed-optionality moves**: wire-protocol additions that all
executors must support, semantic changes to existing primitives,
breaking changes to held-subgraph aggregate-outcome resolution.

When in doubt, open-optionality moves are cheaper to revisit.

## Summary positioning

Rimsky sits in the same neighborhood as Airflow, Dagster, Prefect,
and Temporal. Its distinctive primitives are:

- **Cascade as reactive recomputation** (closer to a build system
  than a workflow engine).
- **Held-subgraph aggregate-outcome resolution** (the stage-then-
  promote-or-discard pattern as machinery).
- **Out-of-process service protocols** (claim producers, executors,
  lifecycle subscribers) speaking gRPC.
- **Content-addressed templates** with movable tags.
- **Three independent runtime processes** communicating only through
  Postgres.

Its current limitations relative to the more mature comparators are
in the data-engineering surface (partitions, materializations, asset
thinking, polished data-quality batteries) and the observability
surface (dashboards, lineage queries). Most of those gaps are on the
roadmap.

Rimsky is the right fit if you want:

- A reactive orchestrator with a strong story for "data flows through
  staged-and-verified work" via held subgraphs.
- Multi-language executors over a clean gRPC protocol.
- Content-addressed graph definitions.
- A platform you self-host as Docker images, Go modules, or a git
  submodule.

Rimsky is not the right fit (today) if you want:

- A polished asset-and-lineage UI out of the box.
- Streaming data-plane semantics.
- Mature partitioned-table primitives for petabyte-scale analytics.
- A managed hosted offering.

The roadmap addresses several of the "not today" items. The
non-goals list above commits to the "not ever" items.
