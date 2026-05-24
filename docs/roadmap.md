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
  dependents become stale and recompute. Five node states; the run
  row carries the `settling_signal_type` (the canonical signal
  type-path that settled the run) and that drives downstream cascade.
- **Subscriptions.** Per-template `subscribes:` declarations route
  upstream signals to downstream nodes; each entry declares a
  `type:` (canonical signal type-path or trailing-`*` prefix) and an
  optional `when:` CEL predicate over the signal payload. Five
  top-level signal kinds (`terminal/*`, `transient/*`, `attribute/*`,
  `event/*`, `message/*`) cover all cascade-bearing transitions.
- **Frames.** The unit of cascade resolution. At most one frame runs
  per instance at a time.
- **Claims and locks.** Producer-mediated concurrency gating. A node
  declares the scope-shaped state it intends to read or write; rimsky
  serializes conflicting acquisitions through a producer's conflict
  matrix.
- **Held subgraphs.** Multiple nodes share a held claim that resolves
  at subgraph completion: aggregate-success commits, any-failure
  abandons. The "stage-then-promote-or-discard" pattern as first-class
  machinery.
- **Fan-out and run-tree.** Nodes that partition a claim into
  sub-claims at runtime; each work unit gets its own run, tracked
  with parent and child-key links through the per-run record.
- **Assets and content lineage.** Durable-lifetime claims surface as
  assets via `/instances/{id}/assets/*`; lineage records
  (`leaf_run`, `claim_terminal`) track computational and
  data-promotion provenance, queryable via `/lineage/*`.
- **Service protocols.** Out-of-process gRPC services — claim
  producers, executors, lifecycle subscribers, publishers (the
  sensor / external-trigger surface), and an opt-in `Validation`
  mix-in any service may advertise for template-registration-time
  checks. Reference implementations ship in-tree.
- **Control-plane API with MCP skin.** HTTP+JSON operator interface
  at the control-api; the same surface available as MCP tools at
  `POST /mcp` for LLM-accessible operation.
- **API-key auth.** Bearer tokens with per-key JSONB permission
  grants, verb-noun action grammar, implicit anonymous bootstrap,
  rotation with grace, structured audit on the existing events log,
  per-handler dry-run mode.
- **Three runtime processes plus migration and conformance tools.**
  Scheduler, supervisor, control-api communicate only through
  Postgres.

Reference implementations of bundled claim producers (filesystem,
postgres, stub), executors (`http-node`, `claude-agent`,
`verifier-http`, `verifier-shape-checks`, stubs), and sensors
(`sensor-{cron,http,object-store,webhook}`) ship in the same
repository.

## Recently shipped

Major work landed since the previous roadmap pass:

- **Layer crystallization, public docs, tri-licensing.**
  Three-Go-module workspace (`protocols/`, `foundation/`, root) with
  depguard-enforced import boundaries; the `docs/{concepts,protocols,agents,humans}/`
  surface and lint suite; Apache/AGPL per-file headers with the
  `rimsky-license-check` tool.
- **Data-platform cycle.** Blessed `blob` and `table` typed-attributes,
  fan-out with run-tree partitioning, `verifier-{http,shape-checks}`
  executors (collapsing the old quality-rule primitive), assets,
  content lineage, backfills, the `Validation` mix-in service.
- **Sensor and publisher protocol.** Four bundled sensors (cron, http,
  object-store, webhook); unified publisher messaging endpoint with
  idempotency.
- **Subscription cascade refactor.** Cascade resolution model rewritten
  around `node-subscription` topics; Park typed with a 4-reason
  taxonomy plus freeform label; atomic-staging pattern documented with
  scenario coverage.
- **Nomenclature resolution.** Project-wide vocabulary alignment
  (`store`→`claim-producer`, `subscription`→`node-subscription`, etc.)
  to prevent drift between code, docs, and design.
- **Control-plane MCP + API-key auth + dry-run.** Per-key JSONB grants
  with wildcard action grammar, MCP folded into control-api as a
  protocol skin at `POST /mcp`, per-handler dry-run with
  synthetic-envelope responses, structured audit, CLI rename
  `rimsky-cli` → `rimsky` with new `auth` subcommand group.

Each cycle has its archived spec and plan under `.ok-planner/history/`;
the published `CHANGELOG.md` tracks what shipped.

## Active design

The cycles below are in scope for the near term. Each will get (or
already has) its own design spec and implementation plan before code
lands. The order reflects intended sequencing, not strict
dependencies — items can be reshaped if discussion exposes a better
path.

### Dashboard and observability

A first-class web operator interface plus the observability
service-protocol surfaces (`ExecutorObservability`,
`ClaimProducerObservability`) that feed it. The dashboard SPA exists
at `dashboards/rimsky-dashboard/` and the observability protos have
shipped; the remaining work covers the wire-up between them and the
control-api's `/v1/observability/*` surface. Spec at
`.ok-planner/specs/2026-05-02-dashboard-and-observability-design.md`.

### `barrier` bundled executor

A first-class fan-in pattern for conditional subgraphs. Today the
readiness-node pattern (a node parks waiting for `on_event` signals
from optional upstream subgraphs) is correct but verbose. A bundled
executor with a clean expected-attributes schema centralizes the
state-machine design once.

### Per-language executor SDKs

Python and TypeScript SDKs over the existing executor protocol. Hide
the gRPC ceremony; expose a decorator/builder API; resolve blessed
typed-attribute handles into language-native types (pandas, polars,
Arrow). Sketched at
`.ok-planner/sketches/2026-05-14-rimsky-development-kit.md`.

### Geo cycle

`geo` as the third blessed typed-attribute, after `blob` and `table`
proved the pattern. Native geospatial features with CRS handling,
predicate pushdown to PostGIS when the operator selects that backing,
and SDK adapters that resolve to language-native spatial types
(GeoPandas, GeoArrow). Sketched at
`.ok-planner/sketches/2026-05-13-geo-cycle.md`.

## On the horizon

Sketched but not yet brainstormed into specs. Each will need its own
brainstorm cycle before becoming a spec.

### Package manager

A surface for distributing rimsky templates, bundled service
binaries, and SDK packages across organizations. Pairs naturally
with the SDK work but stands independently. Sketched at
`.ok-planner/sketches/2026-04-26-package-manager.md`.

### Agentic telemetry

Structured telemetry surface for agentic executors specifically —
model inputs, tool-call decisions, costs, retry behavior — exposed
as queryable events alongside the regular event log. Sketched at
`.ok-planner/sketches/2026-05-07-agentic-telemetry.md`.

### Full traceability

Cross-cutting trace correlation across rimsky's processes and the
out-of-process services it orchestrates, so a single user-facing
request can be followed through control-api, supervisor, executors,
producers, sensors, and external systems. Sketched at
`.ok-planner/sketches/2026-05-16-full-traceability-sketch.md`.

## Declined directions

Considered and explicitly declined, with the reasoning preserved so
future readers know these were thought through.

### Bundled agentic patterns beyond the MCP skin

The control-plane MCP server landed. Three further agentic patterns
were sketched (`.ok-planner/history/sketches/2026-05-14-agentic-platform.md`)
and declined:

- **Bundled knowledge store** (cross-instance LLM memory as a
  claim-producer pattern). Pre-v1 bundling would lock in opinions
  about entry shape, scope conventions, supersession semantics, and
  store-backend choices before any real consumer has stressed them. The
  architecture supports custom claim producers; consumers should
  develop their own approach.
- **Lifecycle-subscriber-as-agent worked example** (autonomous agent
  supervising rimsky workloads from inside a rimsky template). Better
  discovered through real consumer use than designed up-front; the
  lifecycle-subscriber, MCP, and claim-producer primitives are
  already in place for any consumer who wants to build it.
- **Meta-agent primitive** (declarative trigger-to-agent mapping for
  failure repair). Speculative; on hold pending evidence the
  consumer-side wiring is actually verbose enough to earn primitive
  status.

The producer-attribute-validation work originally proposed inside that
sketch landed separately as the `Validation` mix-in service — at a
narrower surface than the sketch proposed (per-claim `data:` bytes
only, not the full node attribute bag) but with the same
registration-time-validation intent.

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
- **Bundled agent-pattern libraries.** Knowledge stores, supervisor
  templates, meta-agent primitives — see "Declined directions."
  Consumer-domain patterns rimsky deliberately doesn't bundle.

If a future direction crosses one of these lines, it gets pushed back
into the consumer's domain or to a more appropriate adjacent system.

## How this roadmap evolves

The active design cycles get their own design specs and implementation
plans before code lands. Specifications, plans, and per-cycle
implementation notes are workflow material — they don't appear on the
public surface but are visible under `.ok-planner/` for those who want
to follow the working detail.

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
