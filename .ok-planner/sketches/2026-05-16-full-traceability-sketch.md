# Full Traceability — Design Sketch

**Date:** 2026-05-16
**Status:** Sketch (not a spec; not authorization to build)

## Idea

After the 2026-05-15 data platform extensions delivery, rimsky's
forensics surface is three independent layers: `log/slog` (ephemeral
process logs), `rimsky_events` (persisted audit log with free-form
JSONB), and `rimsky_lineage` (content-data identity across time, the
new table). The follow-up A+B work added Abandon + force-cancelled
records on lineage and event-emits at the new orchestration sites.
What's still missing is the connective tissue across all of that: a
single causal thread that follows one operation from "operator
created instance X" → "frame Y started" → "node N dispatched" → "claim
Z acquired" → "executor Q ran" → "Commit fired" → "subscriber R got
stale-marked" → "frame Y+1 started" → … through every component, every
process, every network hop.

Distributed tracing — OpenTelemetry-shaped — gives that thread. Spans
mirror the run-tree (which already gives natural parent/child
causality); cross-frame cascades become trace links; cross-process
calls (rimsky → executor, rimsky → claim-producer, sensor → rimsky)
propagate W3C traceparent so the picture stays whole across the wire.

The pitch isn't "replace events or lineage." Events stay as the
audit log, lineage stays as the data identity projection, and tracing
becomes the operational observability layer that makes the other two
queryable by causality rather than just by time + natural keys.

## Shape

### Trace topology mirrors run-tree topology

Rimsky already has the perfect skeleton for spans: `rimsky_node_runs`
with `parent_run_id` and `child_key` form a tree per frame. Sub-graph
dispatch and fan-out add nesting (caller → sub-graph entry; fan-out
parent → N children). Hold subgraphs add co-holdership. The trace tree
mirrors this 1:1.

```
trace (per root operation; see "Roots" below)
  span: frame.run (per frame)
    span: node-run.dispatch (per rimsky_node_runs row)
      span: acquisition.tx
        span: claim.open (per held claim; child of acquisition.tx)
          # producer-side spans appear here via traceparent
        span: subclaim.acquire (when fan_out; one span; child spans per
              SubScopeDescriptor)
          span: dataprocessing.begin_candidate (per sub-claim)
      span: executor.execute
        # executor-side spans appear here via traceparent
      span: terminal.decision (ResolveClaimHandleTerminal)
        span: dataprocessing.commit_candidate / .abandon_candidate
        span: producer.commit / .abandon
        span: lineage.write (claim_terminal)
        span: cancel.descendants (when AggregateAbandon + has descendants)
          # recursive ResolveClaimHandleTerminal spans nest here
        span: cancel.siblings (when AggregateAbandon + strict.cancel_siblings)
          # recursive ResolveClaimHandleTerminal spans nest here
        span: parent.recurse (resolveParentClaimChain)
      span: cascade.walk (when node terminal stale-marks subscribers)
    span: node-run.dispatch (sibling; second node-run in this frame)
      ...
```

Sub-graph dispatch:

```
span: node-run.dispatch (caller of delegate:)
  span: subgraph.dispatch
    span: frame.run (sub-graph's frame; nested trace under the caller)
      span: node-run.dispatch (sub-graph entry)
      ...
      span: node-run.dispatch (sub-graph exit)
    span: subgraph.exit_carry (writeback)
```

Fan-out:

```
span: node-run.dispatch (fan-out parent)
  span: acquisition.tx
    span: subclaim.acquire (returns N descriptors)
  span: fanout.children_created (N children INSERTed)
  span: node-run.dispatch (child[0]; same span hierarchy as any leaf)
  span: node-run.dispatch (child[1]; ...)
  ...
  span: parent.aggregate (when last child resolves; counter-driven)
  span: terminal.decision (parent's own resolution)
```

### Roots — where traces start

A root span is opened at each "operation initiator":

- `instance.create` — operator POST /instances; flows into the first frame
- `instance.terminate` — operator DELETE /instances/{id}; flows into ReleaseHeldDurableClaims
- `sensor.observation` — POST /sensors/{watch_id}/observations; flows into message enqueue → frame
- `sensor-cron.fire` — internal cron tick in sensors/sensor-cron/; flows into observation push
- `backfill.create` — POST /instances/{id}/backfills
- `message.enqueue` — POST /instances/{id}/messages from operator
- `template.register` — POST /templates (synchronous; flows into Validation pipeline)
- `admin.invalidate` — POST /admin/instances/.../nodes/.../invalidate
- `lifecycle.fanout` — control-api firing LifecycleSubscriber events

Each root carries metadata identifying who initiated (operator API
key, sensor watch id, internal scheduler tick, etc.).

### Cross-process propagation

W3C traceparent (header `traceparent` + `tracestate`) crosses every
wire:

- **rimsky → executor** (gRPC `Executor.Execute`): inject traceparent
  into gRPC metadata; executor-side spans become children of
  `executor.execute`. The reference executors (`http-node`,
  `claude-agent`, `stub`, `verifier-shape-checks`, `verifier-http`)
  each need a thin OTel SDK integration.
- **rimsky → claim-producer** (gRPC `ClaimProducer.Open` /
  `.Commit` / `.Abandon` / `.Release` / `.SplitScope` /
  `.ScopesConflict`): same shape. Bundled producers
  (`stores/filesystem`, `stores/postgres`, `stores/stub`) also need
  the integration.
- **rimsky → data-processing** (gRPC `DataProcessing.BeginCandidate` /
  `.CommitCandidate` / `.AbandonCandidate` / `.ListVersions` etc.):
  same shape.
- **rimsky → validation** (gRPC `Validation.Validate`): same shape.
- **rimsky → sensor** (gRPC `Sensor.StartWatch` / `.StopWatch` /
  `.ListWatches`): same shape. Bundled sensors (`sensor-cron`,
  `sensor-http`, `sensor-object-store`, `sensor-webhook`) integrate.
- **sensor → control-api** (HTTP POST /sensors/{watch_id}/observations):
  HTTP header traceparent.
- **subscriber → control-api** (lifecycle event fire-outs are sync
  gRPC from control-api side): traceparent on the request.
- **openlineage subscriber → openlineage collector** (HTTP POST):
  pass-through traceparent so the collector can link rimsky traces
  to upstream/downstream data systems' traces.

### Cross-frame and cross-process causality (the hard part)

Two crossings are tricky:

**1. Scheduler → supervisor coordinate-via-DB.** The three rimsky
processes (`rimsky-scheduler`, `rimsky-supervisor`, `rimsky-control-api`)
don't make direct calls to each other — they coordinate through
Postgres. The scheduler tick stale-marks; the supervisor's claim
loop picks up the marked rows. There's no traceparent header to
propagate. Two options:

- **Trace context columns on `rimsky_node_runs` + `rimsky_frames`.**
  Add `trace_id`, `span_id`, `trace_flags` columns. The scheduler
  writes the current trace context when it creates the row; the
  supervisor reads it when it picks the row up. The supervisor's
  span is then a child of the scheduler's span. Schema impact:
  +3 columns × 2 tables.
- **One trace per frame, link to cascade-source frame.** The
  scheduler's stale-mark span ends; the supervisor starts a new
  trace per frame with a `link` attribute pointing to the
  scheduler's span. Easier to implement (no schema change). Less
  visually intuitive (each frame is its own trace; cascade flows
  visible only via links).

The first option is cleaner for operators; the second is faster
to land. Recommend (1) for the long-term shape.

**2. Cascade across frames.** When sender's terminal stale-marks
receivers in instance X, the receivers' next node-run happens in
frame Y+1 (or later, if the receiver wasn't ready). The receiver's
trace SHOULD know "I'm running because sender's terminal in frame
Y caused me to be stale-marked." OTel spans support `links` — the
receiver's `node-run.dispatch` span gets a link to the sender's
`terminal.decision` span. The link is informational, not
parent-child. Storage of the link requires the sender's span_id to
be persistable + readable — easiest if it's stamped on
`rimsky_wait_set` rows (the cascade walker already inserts these).
Schema impact: +1 column on `rimsky_wait_set`.

### Persistence integration

The existing surfaces gain trace context awareness:

- **`rimsky_events`** gets `trace_id` + `span_id` columns. Every
  `Events().Append(...)` call captures the current span context.
  Operator dashboard queries can pivot from "show me all events for
  this trace" to a span-tree view.
- **`rimsky_lineage`** gets the same columns. Lineage rows become
  joinable to the trace that produced them, so an operator looking
  at a `claim_terminal` row can pivot to the full causal trace of
  why that commit happened.
- **`rimsky_node_runs`** + **`rimsky_frames`** gain trace context
  (per the scheduler→supervisor crossing above).

### Backend + transport

- **OTLP exporter** (HTTP and gRPC variants). Reference deployment
  ships against the OpenTelemetry Collector; the collector handles
  the fan-out to Jaeger / Tempo / Datadog / Honeycomb / etc.
- **Configuration** in `rimsky.yml`:
  ```yaml
  observability:
    tracing:
      enabled: true
      backend: otlp
      otlp:
        endpoint: http://otel-collector:4318/v1/traces
        protocol: http
        headers:
          # auth, etc.
      sampling:
        kind: parent_or_ratio
        ratio: 0.01           # head-based
        always_sample_failures: true
  ```
- **Sampling** is critical. A loaded rimsky deployment with
  sensor-cron firing every minute generates frames at a high rate;
  unsampled tracing would swamp the backend. Head-based ratio
  sampling for the common case; force-sample errors and parked
  rows so post-mortems always have data.

### Privacy / invariants

Spans carry attributes. Attributes MUST NOT contain:

- userdata (`@blessed-invariant 11`)
- claim content — addresses, payloads, scopes (`@blessed-invariant 20`)
- claim candidate handles — opaque producer bytes (covered by 20)
- message payloads (`@blessed-invariant 21`)
- blob bytes (`@blessed-invariant 21`)

Allowed attribute keys: rimsky-side identifiers (run_id, frame_id,
claim_handle_id, instance_id, node_id, template_hash, producer_name,
node_alias), counts (sub-claim count, child count), sizes (in bytes)
of opaque content, scope hash (already used by lineage), version_id,
parked-reason enum, claim-lifetime enum, outcome enum, error_class
strings.

The same redaction discipline already enforced by Change B of the
A+B follow-up applies — extend it from events to spans.

### Stages of rollout (sketch, not a plan)

If this becomes a spec, natural staging:

1. **Trace context columns + transport.** Add the schema columns
   (`rimsky_events`, `rimsky_lineage`, `rimsky_node_runs`,
   `rimsky_frames`, `rimsky_wait_set`). Wire OTel SDK into the
   three rimsky binaries. No external backend yet — just capture
   the context locally.

2. **Span emission at orchestration sites.** Mirror the run-tree
   in spans (per the topology above). Start with the root spans
   (instance.create, sensor.observation, backfill, etc.) and the
   per-frame + per-run-node spans. Add cross-process traceparent
   propagation on the gRPC + HTTP wires.

3. **Bundled service integration.** Each bundled executor /
   claim-producer / sensor gets the OTel SDK. Reference impls
   ship trace-context-aware.

4. **OTLP exporter + sampling.** Land the export path; ship
   reference deployment against the OpenTelemetry Collector.
   Sampling config in `rimsky.yml`.

5. **Dashboard integration.** Operator dashboard pivots from
   `rimsky_events` view to "span tree for this trace" view;
   `rimsky_lineage` rows surface "open trace" buttons.

6. **Conformance.** A new conformance binary
   (`rimsky-tracing-conformance` or fold into existing ones)
   verifies that third-party executors / producers / sensors
   correctly propagate traceparent.

## Open questions

- **Trace context column placement.** Cleaner to put trace_id on
  every relevant table (run_runs, frames, events, lineage, wait_set,
  claim_handles)? Or one canonical place (rimsky_traces table with
  FKs everywhere)? The former is denormalized but cheap; the latter
  is normalized but adds a JOIN to every observability query.
- **One trace per instance lifetime, or one trace per frame?**
  Per-frame is the natural unit (frame is the cascade resolution
  unit) but means cross-frame causality has to be link-based. Per-
  instance is more intuitive for "the lifecycle of one tenant
  operation" but spans can be very long-lived (held-durable claims
  can survive across many frames).
- **How does this interact with the run-tree retention sweep
  (`SweepRunTreeRetention`)?** When a `rimsky_node_runs` row is
  garbage-collected, does the corresponding span become orphaned in
  the trace backend? Or should retention also fire a span-end at
  GC time?
- **Multi-tenant rimsky.** If one rimsky deployment serves multiple
  consumers, each instance should get its own trace boundary —
  cross-tenant traces would leak observability. Probably enforced
  by setting `instance_id` as the trace's tenant key in the OTLP
  exporter config.
- **Backwards compatibility with existing `cascade-graph` dashboard
  routes.** The `/observability/*` routes serve rimsky-internal
  state JSON today. Do those endpoints add `trace_id` to their
  responses so the operator dashboard can deep-link from a
  rimsky_event to a Jaeger/Tempo trace, or stay rimsky-internal
  and let operators use their backend's own UI?
- **Sampling under fan-out.** When a fan-out parent decides "I'm
  sampled," should all N children inherit the same decision? OTel
  parent-based sampling handles this naturally, but rimsky's
  fan-out children are dispatched asynchronously across multiple
  supervisors — the sampling decision needs to ride on the run-
  tree (likely via the trace_id column on rimsky_node_runs).
- **Conformance for third-party services that DON'T propagate
  traceparent.** Their spans show up as orphans under
  `executor.execute` etc. Worth a conformance check? Or accept
  that third-party services may not be trace-aware and accept the
  partial picture?

## Risks / unknowns

- **Volume.** Production rimsky under load could generate spans at
  a rate that's expensive to export. Sampling helps but requires
  careful default. Backpressure from the exporter could affect
  rimsky's runtime if not bounded (OTel SDK's batch exporter has
  a queue; full queue blocks).
- **Schema churn.** Adding trace context columns to 5 tables is a
  meaningful schema commitment. Pre-v1 break-freely makes the
  migration safe but post-v1 this would be a Big Deal.
- **Operator complexity.** Today rimsky operators don't need to
  run an observability backend. Adding tracing makes the
  reference deployment heavier (now needs OTel Collector +
  Jaeger/Tempo). The `enabled: false` default keeps the bare path
  unchanged, but the "best experience" path now has a dependency.
- **Cross-trace causality is genuinely hard.** Cascade across
  frames + held-durable claims surviving across instances + sensor
  pushes from external systems all complicate the "what's one
  trace?" story. There's no single right answer; the choice has
  to fit operator mental models more than implementation
  convenience.
- **The link between `rimsky_lineage` (data identity) and traces
  (causal flow) is the most powerful but least obvious feature.**
  Operators may not immediately see why they'd want to pivot from
  a `claim_terminal` lineage row to a trace, OR vice versa. Worth
  thinking about the dashboard UX explicitly.
- **Bundled-service trace SDK integration.** Five reference
  binaries (the bundled sensors + the two verifier executors)
  each need OTel wiring. Plus the `stores/*` and the existing
  `executors/*`. That's ~12 binaries to update. Each is small but
  the breadth is real work.

## What this is not

- **Not a replacement for `rimsky_events`.** Events stay as the
  persistent audit log with free-form JSONB payloads. Traces are
  for operational observability over a finite retention window;
  events are for "what did the system claim about itself over
  arbitrary time."
- **Not a replacement for `rimsky_lineage`.** Lineage tracks
  content identity across time (which version of which dataset
  ran where); traces track causal flow within a single operation.
  Joinable, complementary, not interchangeable.
- **Not metrics.** This sketch is for distributed tracing. Metrics
  (counter / gauge / histogram aggregates) are a separate
  observability surface and would deserve their own sketch.
- **Not log shipping.** `log/slog` stays as the structured-log
  surface; if operators want centralized log shipping, that's a
  collector concern, not a rimsky concern.
- **Not OpenLineage replacement.** The openlineage subscriber
  emits data-lineage events to external OL collectors; that's a
  different graph (cross-organization data flow) than rimsky's
  internal traces.
