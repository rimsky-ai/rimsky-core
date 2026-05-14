# Data + agentic platform roadmap

**Date:** 2026-05-13
**Status:** intermediate roadmap (synthesis; to be walked through item-by-item
and broken into focused sketches)
**Companion sketches:** the seven `2026-05-13-*` sketches in this directory
cover the foundational data-platform ergonomics. This roadmap identifies the
**expansion territory** beyond those — what's needed to make rimsky credible
across the full data-processing landscape and to leverage AI agents in ways
the current "agent as just another executor" model misses.

## Frame

The seven foundational sketches (blessed-typed-attributes, executor SDKs,
verifier-executor convention, atomic-staging pattern, fan-in for conditional
subgraphs, parked-state dashboard surface, plus the index) cover the basic
data-platform ergonomics. They make rimsky competent at the data-engineering
shape of work.

This roadmap covers two further directions:

1. **Closing the conceptual gap with established data-processing frameworks
   at the concept level.** Surveys Airflow, Dagster, Prefect, Beam, Spark,
   Flink, dbt, Temporal. Identifies primitives those frameworks treat as
   first-class that rimsky lacks.
2. **AI agents as first-class control-plane participants, not just
   executors.** Today agents are dispatched as workers. The killer-app
   pattern is agents that observe runs and act on the graph itself.

Both directions can ride alongside the existing wishlist. Neither is a v1
commitment; both are sketches-of-direction.

---

## Part 1: Comparative survey

A compressed view of what each major framework brings as load-bearing
primitives.

### Airflow

DAGs, scheduled and ad-hoc runs, sensors (poll external state and trigger),
XCom (small inter-task data), hooks (external system clients), connections,
operators (work units), parameter-driven runs, backfills as a first-class
operation.

Rimsky equivalents: cascade DAG, templates + instances, partial (`force-fire`
admin endpoint). Missing: sensors, parameter-driven historical backfills.

### Dagster

Assets (artifacts with single owners, the graph IS the lineage graph),
ops (work units), partitions (slice an asset by dimension), IOManagers
(per-asset I/O abstraction), sensors, schedules, observability surface
(asset history, materialization records, dependency views), backfills
targeting partition ranges, code locations + code versioning.

Rimsky equivalents: cascade, nodes, claims-as-IOManager. Missing:
asset-thinking presentation, partitions, observability surface as
first-class.

### Prefect

Flows (DAGs), tasks, deployments (parametrized flow + trigger pairings),
runtime state semantics, blocks (reusable configured infrastructure),
runners.

Rimsky equivalents: templates, instances, claim-producers-as-blocks.
Mostly covered or close.

### Beam

PCollections (distributed datasets), PTransforms, windowing (event-time,
processing-time), watermarks (late-data semantics), triggers (when to
materialize a window), runners (Flink, Spark, Dataflow).

These are data-plane streaming concerns. Out of rimsky's intended scope.

### Spark

RDDs, DataFrames, partitions (with full execution + scheduling awareness),
narrow vs wide dependencies, shuffle, executors-as-JVMs, Catalyst query
planning, structured streaming.

Most is data plane. The partitions concept generalizes well.

### Flink

Stream-shaped operators, keyed state, watermarks, exactly-once semantics,
savepoints, event-time windowing.

Streaming data plane; out of rimsky's intended scope.

### dbt

Sources (declared external inputs), models (transforms), tests (declarative
data assertions), seeds (static reference data), snapshots (historical
slowly-changing-dimension capture), materializations (table | view |
incremental | ephemeral | snapshot), exposures (downstream consumers),
semantic layer (metric definitions).

Rimsky equivalents: nodes-as-models, verifier-executors-as-tests. Missing:
materialization strategies (incremental especially), seeds-as-immutable-
reference shape, snapshots-as-historical-capture shape.

### Temporal

Workflows (durable executions), activities (RPC-shaped units of work),
signals (external events into workflows), queries (read workflow state),
versioning (run old workflow versions to completion; new versions start
fresh), patches (in-flight migration support).

Rimsky equivalents: instances-as-workflows, executor-dispatch-as-activities,
admin-invalidate-as-signals. Versioning is partially covered by content-
addressed templates; in-flight migration story is less developed.

---

## Part 2: Real conceptual gaps in rimsky

The gaps that emerge from this survey, prioritized by impact.

### Gap 1: Partitions

**The biggest.** Every mature data framework treats "sliceable datasets and
parallel sub-units of work" as first-class. Rimsky's `concept:node` is the
unit of work; partitions would be the unit of data within a unit of work.

What changes if rimsky adopts partitions:

- Blessed `table` attributes have partition specs (daily / hourly /
  by-dimension / hash-based).
- Cascade walks at partition granularity — only affected partitions
  invalidate, only affected partitions recompute. Today's
  "invalidate-the-whole-node" is too coarse for analytics workloads.
- Nodes can declare per-partition or aggregate execution.
- Dashboards present state per partition; verifiers run per partition;
  barriers aggregate across partitions.
- Backfills become tractable as parametrized operations over partition
  ranges.

This touches `concept:cascade`, `concept:node-state`, `concept:claim-handle`,
the scheduler's eligibility logic, the persistence schema. It's a real
foundation-level change. Pairs tightly with the blessed-typed-attribute
work (a partitioned `table` is a `table` with a partition spec).

Worth its own sketch — possibly its own multi-spec design effort.

### Gap 2: Sensors / external triggers

The trigger surface today: `cron`, admin `force_fire`, in-graph
`invalidate`, held-claim resolution. The watch-external-state shape
(new file in S3, topic offset advanced, row in audit table, webhook
arrived) is achievable via a polling executor that parks and resumes —
but it's not a first-class concept and the pattern is verbose.

A `concept:sensor` or `concept:trigger` primitive would:

- Declare a watch condition: substrate + scope + predicate.
- Either be implemented as a special bundled executor with conventional
  userdata, or as a control-plane concept distinct from nodes.
- Emit `invalidate(target)` against named template nodes when the watch
  condition fires.

The "executor with conventional userdata" framing is probably the right
first move — same logic as the verifier-executor convention. Lower-
commitment; pattern emerges; primitive can be lifted later if it earns
its place.

### Gap 3: Materialization strategies / incremental computation

Today every blessed-type write produces a new version of the whole
attribute. dbt's `incremental` / `append_only` / `merge` /
`partition_overwrite` materializations let a node produce only the delta.

For petabyte-scale tables, full re-materialization is operationally
infeasible. The fix interacts with partitions: a `partition_overwrite`
materialization is "produce this partition's contents; merge it into the
table." Without partitions, incremental modes are much harder to model
honestly.

Sequence: partitions first, then incremental materializations. Both are
properties of blessed types; the type registry declares which
materializations each type supports; the substrate driver implements the
strategy.

### Gap 4: Content lineage on top of structural cascade

Rimsky's cascade graph IS the structural lineage graph — "node X depends
on node Y." What it doesn't track is **value lineage** — "this writeback
value v3 was derived from these specific source values: input A version
v2, input B version v1, by executor E version 1.0."

Tools like OpenLineage, DataHub, Marquez, Atlas track this for audit,
impact analysis, debugging. Rimsky could attach a content-lineage record
to each terminal:

```
(node_id, attribute_version, sources: [{path, version}],
 producer_executor, producer_version, params_snapshot)
```

Small primitive; most of what it records is already captured in the
events log. The value is making it queryable as a lineage view: "what
produced this bad value" debugging, "what would be affected if I change
this input" impact analysis, compliance audit trails.

### Gap 5: Asset-thinking as a presentation layer

Dagster's biggest conceptual move: reframe the unit from task/op to asset.
The graph IS the asset graph; the asset graph IS the lineage graph. Data
engineers find this idiom natural — "the parcels asset depends on the
boundary asset, refreshed when raw-parcels arrives."

Rimsky's `concept:node` is task-shaped. The blessed-typed-attribute work
already nudges toward asset-thinking — a `table` attribute is asset-shaped —
but the public surface stays task-first.

This is a presentation reframe, not a primitive change. The question is
whether the public surface needs a "node-and-its-output" pairing that
reads as an asset, or whether keeping node-as-unit-of-work and attribute-
as-output is fine for the data audience.

Possibly: a doc-level reframe (concept:asset = "the named output of a
node, in the context of the lineage graph"), without touching protocol
or primitives. Cheap to add; significant onboarding-doc value.

### Gap 6: Backfills as a parametrized operation

Once partitions exist, "rerun this template over this partition range"
becomes a natural operation. Today the closest thing is "create N
instances with N different param values," which doesn't track as a unified
backfill.

The primitive: a control-api operation that, given a template + a
partition range, schedules per-partition runs and surfaces the rolled-up
status. Falls out of partitions; not standalone work.

---

## Part 3: Adjacent but probably out-of-scope

Listed for completeness — these would be tempting to chase but probably
belong in the data plane, not in rimsky.

- **Event-time windowing, watermarks, late-data handling.** Streaming
  data-plane concerns. Flink and Beam live here. Adding them to rimsky
  would pull the orchestrator into data-plane responsibilities and erode
  the substrate-agnostic position.
- **Per-key state stores.** Flink keyed state, Spark Structured Streaming
  state. Same reason.
- **Streaming/batch unification.** Rimsky's invocation model is discrete
  dispatch. A node either runs or doesn't; it doesn't represent a
  continuously-running stream operator.
- **CPU/memory-aware scheduling, fair-share queueing.** Cluster scheduler
  concern; k8s / YARN / Kubernetes job. Rimsky's `concept:named-lock`
  gives basic capacity gating; deeper scheduling lives downstream.
- **Semantic layer / metric definitions.** Application concern. dbt's
  domain. Not rimsky's.
- **Workflow versioning / in-flight migration.** Temporal has this as
  first-class. Rimsky's content-addressed templates + movable tags give
  ~80% of this for free. The "in-flight instance, template changed"
  story could use a doc — but probably not a new primitive.

---

## Part 4: AI agents as first-class control-plane participants

### Today's state

Agents are executors. The bundled `executors/claude-agent` is a fine
worker-shape: receive ExecuteRequest with substituted attributes, do work,
return terminal. Same protocol surface as `http-node`. Same retry policy,
same error routing.

This is **conceptually clean but under-leverages what agents can do.** A
worker-shape agent: "given this input, produce that output." A control-
plane participant: "watch the run, understand what's happening, act on the
graph itself."

The killer-app pattern moves to the second.

### Frame A: Bundled MCP server for the control-api

Smallest move, concrete, high leverage.

A bundled `mcp-servers/rimsky-control-api/` exposes rimsky's HTTP control-
api as MCP tools an LLM can invoke natively:

- Read tools: `list_instances`, `get_instance`, `list_templates`,
  `get_template_spec`, `get_events`, `get_node_attribute`,
  `get_claim_handle_state`.
- Write tools: `invalidate_node`, `force_fire_schedule`,
  `terminate_instance`, `create_instance`.
- Subscribe tool: `subscribe_lifecycle_events` (streaming).

This is packaging, not protocol. The control-api already exists; the
MCP server is a translation layer over it. Concrete consumer wins:

- LLMs can investigate runs interactively. "Why did instance X fail
  yesterday?" → introspects events, attributes, claim state, explains.
- Operator dialogue becomes natural. "Show parked nodes older than an
  hour" → MCP tool call → table → "wake the third one" → another MCP
  tool call.
- Agents become *operators* in the literal sense, with appropriate access
  controls.

Bundled and shipped as reference. Consumers deploy or fork as needed.

### Frame B: Lifecycle-subscriber-as-agent worked example

`concept:lifecycle-subscriber` is already an opt-in protocol for peers
that react to template/instance state transitions. Six methods, firing at
relevant transitions.

A lifecycle-subscriber that **hosts an LLM and reacts to events with
autonomous action** is the unlock:

- `OnInstanceTerminated` with failure → agent investigates, decides
  retry / escalate / file ticket.
- `OnInstanceCreated` for a new run → agent observes early progress; if
  discovery is unusually slow, intervenes.
- Periodic walks across all running instances → agent surfaces patterns,
  drift, anomalies that wouldn't trip any individual quality rule.

No new rimsky primitive. The pattern is "host an LLM in a lifecycle-
subscriber; give it MCP tool access to the control-api; let it decide
what to do."

This is the **"agent as platform supervisor"** pattern. Most data platforms
don't have this surface. Documented as a worked example.

### Frame C: Cross-instance knowledge claim-producer pattern

Today's agents start fresh per instance — no memory of how previous
instances of the same template went. A `cross-instance-knowledge` claim-
producer:

- Scope shape: `knowledge/{template-name}` (or finer-grained per template
  + scope dimension).
- `r`-claims from agent executors in new instances let them consult prior
  lessons.
- `rw`-claims from agent executors in terminating instances let them
  distill new lessons.
- Updates are mutable; substrate is the producer's choice (filesystem,
  S3, vector DB for embeddings-shaped knowledge).

Not a new rimsky primitive — a claim-producer shape. High leverage when
paired with Frame B (the supervising agent reads the knowledge claim;
distills lessons after each instance terminates).

Documented as a worked example and possibly a bundled reference
implementation.

### Frame D: Meta-agent primitive (speculative)

The more ambitious move. Today repair lives at the node level via
`on_executor_errored` and consumer-built repair subgraphs. The pattern
is per-node and the wiring is explicit.

A `concept:meta-agent` (or `concept:supervisor-handler`) primitive could
centralize this:

```yaml
meta_agent:
  executor: claude-supervisor
  triggers:
    - on_node_failed: any
    - on_node_parked_over: 1h
    - on_quality_rule_failed: any
  scope: full_instance_state
  tool_access: control_api
```

The meta-agent is dispatched whenever its triggers fire. It receives the
full instance state and has tool access to the control-api. It can
diagnose, take an action (invalidate with overrides, dispatch a repair
subgraph, escalate, terminate with summary), and the supervisor applies
the action.

What this gives that node-level error handling doesn't: **a unified agent
surface with visibility across the entire run**, correlating failures,
making decisions that span the work.

Structurally similar to Frame B but instance-scoped and primitive-level
rather than peer-shape. If Frames A + B + C are sufficient, this might
be unnecessary — consumers wire it themselves. Worth investigating
whether the pattern is verbose enough across real consumers to earn
primitive status.

### The killer-app combination

The four frames together enable **autonomously-managed pipelines** — a
platform where agents not only do the work but watch the work, fix the
work when it breaks, learn from prior runs, and propose improvements.

Most data platforms have agents *in* the work (LLM transforms, embedding
pipelines). Few have agents *in* the platform itself. Rimsky's existing
position — out-of-process peers that react to events via well-defined
protocols — is well-suited; the leap to "those peers can be agents" is
conceptual, not structural.

The work is in the surrounding tooling: MCP server, worked examples,
knowledge-producer shape, possibly the meta-agent primitive.

---

## Part 5: Candidate sketches that follow this roadmap

After walking through each gap and each frame, the expected sketch suite
expands to roughly:

### Data-processing extensions

- `2026-05-13-partitions.md` — biggest single ask. Foundation-level work.
- `2026-05-13-sensors-and-triggers.md` — modest; bundled-executor first.
- `2026-05-13-materialization-strategies.md` — sequenced after partitions.
- `2026-05-13-content-lineage.md` — small primitive, audit/debug payoff.
- `2026-05-13-asset-thinking-vocabulary.md` — presentation reframe; doc
  work primarily.
- `2026-05-13-backfills-as-operations.md` — falls out of partitions; may
  fold into the partitions sketch.

### Agentic integration

- `2026-05-13-control-api-mcp-server.md` — bundled MCP server design.
- `2026-05-13-agentic-supervisor-pattern.md` — lifecycle-subscriber-as-
  agent worked example.
- `2026-05-13-cross-instance-knowledge.md` — claim-producer shape for
  per-template memory.
- `2026-05-13-meta-agent-primitive.md` — speculative; investigation more
  than commitment.

### Sketches that may not survive into the final suite

Items where discussion may reveal that they fold into others, or that
they don't actually earn their place:

- `asset-thinking-vocabulary` — could fold into the existing blessed-types
  sketch as a vocabulary section.
- `backfills-as-operations` — falls out of partitions; might be a section
  in the partitions sketch rather than its own file.
- `meta-agent-primitive` — may turn out to be subsumed by Frames A + B + C.

---

## Discussion plan

We'll walk through each item in two passes:

1. **Data-processing gaps**, in priority order: partitions, materialization,
   sensors, content lineage, asset-thinking, backfills.
2. **Agentic frames**, in order: MCP server, lifecycle-subscriber-as-agent,
   cross-instance knowledge, meta-agent.

For each item, pressure-test:
- Whether it's actually a gap rimsky needs to fill (vs. better-left-elsewhere).
- Whether it's a primitive, a pattern, a worked example, or doc-only.
- Where it interacts with existing rimsky primitives.
- What sequence of work it implies (foundation, control, executor side).
- Open design questions.

After both passes, the surviving items become focused sketches in the
final suite. Items that fold into existing sketches or evaporate after
discussion don't get their own files.

This roadmap document itself stays as a sketch — workflow scratch.
Reference point for the discussion; not durable documentation.
