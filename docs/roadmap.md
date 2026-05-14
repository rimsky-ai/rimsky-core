# Rimsky roadmap

This document describes where rimsky is headed. It tells someone
evaluating the platform what's actively being designed, what's on the
horizon, and what rimsky has deliberately chosen not to be.

Rimsky is pre-v1. The platform's wire protocols, YAML config shapes,
and persistence schemas are not stable until v1 ships. The roadmap is
a direction statement, not a contract. Items can be reshaped, deferred,
or dropped if discussion exposes a better path.

## What rimsky is today

A project-agnostic reactive node-graph orchestration platform. The
load-bearing primitives:

- **Templates and instances.** Templates are content-addressed
  specifications of a graph of nodes. Instances bind a template to
  parameters and runtime state.
- **Cascade.** Node-state propagation: when a node's value changes,
  dependents become stale and recompute. Five node states; a sibling
  `last_outcome` column carries the resolution flavor.
- **Frames.** The unit of cascade resolution. At most one frame runs
  per instance at a time.
- **Claims and locks.** Producer-mediated concurrency gating. A node
  declares the scope-shaped state it intends to read or write; rimsky
  serializes conflicting acquisitions through a producer's conflict
  matrix.
- **Held subgraphs.** Multiple nodes share a held claim that resolves
  at subgraph completion: aggregate-success commits, any-failure
  abandons. This is the "stage-then-promote-or-discard" pattern as a
  first-class machinery.
- **Three out-of-process service protocols.** Claim producers,
  executors, lifecycle subscribers. Each speaks gRPC; reference
  implementations ship in-tree.
- **Three runtime processes plus migration and conformance tools.**
  Scheduler, supervisor, control-api communicate only through Postgres.

Reference implementations of bundled claim producers (filesystem,
postgres, stub) and executors (`http-node`, `claude-agent`, stub)
ship in the same repository.

## Active design

Three brainstorm-spec-implement cycles are in scope for the near term.
None have begun implementation; the order below reflects intended
sequencing.

### Quality-of-life cycle

Three independent improvements that sharpen observability and the
pattern surface without changing the core conceptual model.

- **`ParkReason` enum.** Today's `Park` event carries opaque
  parked-node context. Adding a typed reason (time-wait, signal-wait,
  awaiting-human, barrier-wait, retry-backoff) lets dashboards and
  diagnostics distinguish "rate-limit retry — expected" from "operator
  approval missing — page someone."
- **`barrier` bundled executor.** A first-class fan-in pattern for
  conditional subgraphs. Today the readiness-node pattern (a node
  parks waiting for `on_event` signals from optional upstream
  subgraphs) is correct but verbose. A bundled executor with a clean
  userdata schema centralizes the state-machine design once.
- **Atomic-staging pattern doc and reference producer.** A worked
  example for custom claim producers with stage-then-swap-on-Commit
  semantics. Generic across stores (Postgres schema swap, S3 prefix
  rename, Iceberg branch fast-forward, filesystem directory move).
  The reference implementation is filesystem-based.

### Data-platform cycle

Expands rimsky from "an orchestration platform that happens to handle
data workloads with effort" into "an orchestration platform whose
data-engineering surface is first-class."

- **Blessed typed-attribute standard library.** A small, bounded,
  opinionated set of attribute types where rimsky picks the backing
  store, owns the implementation, and provides predetermined
  concurrency and lifetime semantics. First two types: `blob`
  (evolution of today's blob backend) and `table` (row-oriented
  dataset with copy-on-write versioning over operator-configured
  object storage).
- **Per-language executor SDKs.** Python and TypeScript SDKs over the
  existing executor protocol. Hide the gRPC ceremony; expose a
  decorator/builder API; resolve blessed typed-attribute handles into
  language-native types (pandas, polars, Arrow) via per-type adapters.
- **Verifier-executor convention.** Collapse today's `quality-rule`
  primitive into the executor model. Quality checks become verifier
  nodes; in-process Go evaluators get deprecated; bundled verifier
  executors (`verifier-shape-checks`, `verifier-http`) cover the
  common cases. Held-subgraph membership preserves the "bad data
  never reaches canonical state" guarantee.

### Geo cycle

Adds `geo` as a blessed typed-attribute, after `blob` and `table`
prove the pattern. Native geospatial features with CRS handling,
predicate pushdown to PostGIS when the operator selects that backing,
and SDK adapters that resolve to language-native spatial types
(GeoPandas, GeoArrow).

## On the horizon — data-engineering extensions

Beyond the three active cycles, the following directions have been
sketched but not yet committed. Each will need its own
brainstorm cycle before becoming a spec.

### Partitions

The biggest single extension. Every mature data framework treats
"sliceable datasets and parallel sub-units of work" as first-class. A
node is rimsky's unit of work; partitions would be the unit of data
within a unit of work.

What changes if rimsky adopts partitions:

- Blessed `table` (and `geo`) attributes carry partition specs
  (daily, hourly, by-dimension, hash-based).
- Cascade walks at partition granularity — only affected partitions
  invalidate; only affected partitions recompute. Today's
  "invalidate-the-whole-node" is too coarse for analytics workloads.
- Nodes declare per-partition or aggregate execution.
- Dashboards present state per partition; verifiers run per partition;
  barriers aggregate across partitions.
- Backfills become tractable as parametrized operations over partition
  ranges.

Pairs tightly with the blessed typed-attribute work. Touches cascade,
node-state, claim-handle, scheduling, and the persistence schema.
Worth its own multi-spec design effort.

### Sensors and external triggers

Today's trigger surface: cron, admin `force_fire`, in-graph
`invalidate`, held-claim resolution. The watch-external-state shape
(new file in S3, topic offset advanced, row in an audit table, webhook
arrived) is achievable via a polling executor that parks and resumes,
but it's not a first-class concept and the pattern is verbose.

A sensor primitive would declare a watch condition and emit
`invalidate(target)` against named template nodes when the condition
fires. Lower-commitment first move: a bundled executor with
conventional userdata, lifted to primitive status if the pattern earns
its place.

### Materialization strategies and incremental computation

Today every blessed-type write produces a new version of the whole
attribute. dbt's `incremental` / `append_only` / `merge` /
`partition_overwrite` materializations let a node produce only the
delta. For petabyte-scale tables, full re-materialization is
operationally infeasible.

Sequenced after partitions — a `partition_overwrite` materialization
is "produce this partition's contents; merge it into the table."
Without partitions, incremental modes are much harder to model
honestly.

### Content lineage

Rimsky's cascade graph is the structural lineage graph: "node X
depends on node Y." What it doesn't track is value lineage: "this
writeback value v3 was derived from these specific source values,
input A version v2, input B version v1, by executor E version 1.0."

Small primitive. Most of what it records is already captured in the
events log. The value is making it queryable: "what produced this
bad value" debugging, "what would be affected if I change this input"
impact analysis, compliance audit trails.

### Asset-thinking as a presentation layer

Dagster's conceptual move: reframe the unit from task or op to asset.
The graph IS the asset graph IS the lineage graph. Rimsky's node is
task-shaped. The blessed typed-attribute work nudges toward
asset-thinking — a `table` attribute is asset-shaped.

Possibly a documentation reframe rather than a primitive change: an
"asset" concept defined as "the named output of a node, viewed in the
context of the lineage graph," without touching the protocol or
adding new primitives. Cheap to add; significant onboarding-narrative
value.

### Backfills as a parametrized operation

Once partitions exist, "rerun this template over this partition range"
becomes a natural operation. Today the closest thing is "create N
instances with N parameter values," which doesn't track as a unified
backfill.

A control-api operation that, given a template plus a partition range,
schedules per-partition runs and surfaces the rolled-up status. Falls
out of partitions; may fold into the partitions design rather than
standalone work.

## On the horizon — agentic integration

Today rimsky's executor protocol supports agent-shaped executors
naturally — `claude-agent` is a TypeScript reference executor that
wraps the Claude CLI. This works well for "agent as worker" patterns.

The next direction is **agent as control-plane participant**: agents
that watch runs, understand what's happening, and act on the graph
itself. Four sketched approaches:

### A bundled MCP server for the control-api

The control-api already exposes HTTP endpoints for reading instance
state, invalidating nodes, force-firing schedules, and creating
instances. A bundled MCP server translates those endpoints into MCP
tools that an LLM can invoke natively.

Concrete consumer wins:

- LLMs can investigate runs interactively. "Why did instance X fail
  yesterday?" becomes a sequence of MCP tool calls against events,
  attributes, and claim state.
- Operator dialogue becomes natural. "Show parked nodes older than an
  hour" → table → "wake the third one" → another tool call.
- Agents become operators in the literal sense, with appropriate
  access controls.

Packaging, not protocol. The control-api already exists; the MCP
server is a translation layer.

### Lifecycle-subscriber-as-agent worked example

The lifecycle-subscriber protocol is already opt-in for peers that
react to template and instance state transitions. A lifecycle
subscriber that hosts an LLM and reacts to events with autonomous
action is the unlock:

- `OnInstanceTerminated` with failure → agent investigates and decides
  retry, escalate, or file ticket.
- `OnInstanceCreated` for a new run → agent observes early progress;
  intervenes if discovery is unusually slow.
- Periodic walks across all running instances → agent surfaces
  patterns, drift, and anomalies that wouldn't trip any individual
  quality rule.

No new rimsky primitive — just a worked example pairing existing
machinery (lifecycle subscriber, control-api, MCP server) into the
"agent as platform supervisor" pattern.

### Cross-instance knowledge as a claim-producer pattern

Today's agents start fresh per instance. A cross-instance-knowledge
claim producer carries scope `knowledge/{template-name}`:

- Read claims from agent executors in new instances let them consult
  prior lessons.
- Read-write claims from agent executors in terminating instances let
  them distill new lessons.
- Updates are mutable; the store is the producer's choice (filesystem,
  S3, vector DB for embeddings-shaped knowledge).

Not a new primitive — a claim-producer shape. High leverage when
paired with the lifecycle-subscriber-as-agent pattern (the supervising
agent reads the knowledge claim; distills lessons after each instance
terminates).

### Meta-agent primitive (speculative)

The more ambitious move. Today node-level repair happens via
`on_executor_errored` and consumer-built repair subgraphs. The pattern
is per-node; the wiring is explicit.

A meta-agent primitive could centralize this: a declarative
configuration of triggers (node failed, node parked over threshold,
verifier failed) that dispatches an agent with tool access to the
control-api whenever any trigger fires. The agent receives the full
instance state, diagnoses, takes an action (invalidate with overrides,
dispatch a repair subgraph, escalate, terminate with summary), and
the supervisor applies the action.

If the three patterns above are sufficient, this might be unnecessary
— consumers wire it themselves. Worth investigating whether the
pattern is verbose enough across real consumers to earn primitive
status.

## Explicit non-goals

Rimsky deliberately chooses not to be the following things, even
though they're orchestration-adjacent.

- **Stream processing.** Event-time windowing, watermarks, late-data
  handling, exactly-once stream semantics. These are streaming
  data-plane concerns. Flink and Beam live here. Adding them to rimsky
  would pull the orchestrator into data-plane responsibilities and
  erode the store-agnostic position.
- **Per-key state stores.** Flink's keyed state, Spark Structured
  Streaming state. Same reason.
- **Streaming-batch unification.** Rimsky's invocation model is
  discrete dispatch. A node either runs or doesn't; it doesn't
  represent a continuously-running stream operator.
- **CPU and memory-aware scheduling, fair-share queueing, cluster
  resource management.** These are cluster scheduler concerns —
  Kubernetes, YARN, Nomad. Rimsky's named-lock primitive gives basic
  capacity gating; deeper scheduling lives downstream.
- **Semantic layer and metric definitions.** Application-level
  concerns. dbt's domain. Not rimsky's.
- **In-flight workflow versioning and migration.** Temporal has this
  as first-class. Rimsky's content-addressed templates plus movable
  tags give roughly 80% of this for free — old instances continue on
  their template hash; new instances pick up the moved tag. The
  remaining 20% (mid-flight migration to a new template version) is
  not a planned primitive.

If a future direction crosses one of these lines, it gets pushed back
into the consumer's domain or to a more appropriate adjacent system.

## How this roadmap evolves

The active design cycles get their own design specs and implementation
plans before code lands. Specifications, plans, and per-cycle
implementation notes are workflow material — they don't appear on the
public surface but are visible in the repository's design-log
directories for those who want to follow the working detail.

The on-the-horizon items will each be brainstormed individually as
the active cycles complete. Each will need to pressure-test:

- Whether it's actually a gap rimsky needs to fill, or one that's
  better left to an adjacent system.
- Whether it's a primitive, a pattern, a worked example, or
  documentation-only.
- Where it interacts with existing rimsky primitives.
- What sequence of work it implies (foundation, control, executor
  side).
- What open design questions need resolving before commitment.

Items can be merged with other items, deferred indefinitely, or
dropped if pressure-testing exposes a better path. The roadmap reflects
direction; the published changelog tracks what actually shipped.
