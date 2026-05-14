---
concept: x-as-executor
definition: |
  The pattern of wrapping an existing system (a CI pipeline, an ETL job, a custom worker) inside an Executor protocol implementation so rimsky can dispatch to it. Often the lowest-friction path to adopting rimsky in an existing project.
proto_symbol: Executor in protocols/proto/v1/executor.proto
config_field: rimsky.yml:executors
api_surface: (none)
related: [executor, claim, template]
deprecated_terms: []
---

# Pipelines as executors

A common pattern for adopting rimsky in an existing system is to wrap an
external pipeline, service, or workflow as an **executor**. Once the
external work is reachable through the executor protocol, every other
piece of rimsky machinery (claims, attributes, error policy,
cascade, frames, handlers, named events, parked-state) becomes available
to govern it.

This page outlines the design idiom and the wire-shape choices that make
it work for several recurring categories of integration: agent-driven
analyses, webhook-driven flows, and external-decision waits.

## What an executor is, in this lens

From rimsky's perspective an executor is anything that:

1. Implements the `Execute` RPC (gRPC stream or HTTP+JSON bridge).
2. Returns one of the protocol's terminal events
   (`Success`, `Error{error_class: "executor_blocked"}`, `Error{error_class}`, `AwaitAsyncCallback`, `Park`).
3. Optionally implements `ExecutorObservability.Capabilities()` to
   declare its `userdata_schema` and `declared_events`.

There is no requirement that an executor be implemented in any
particular language or runtime. The reference implementations
(`claude-agent` in TypeScript, `http-node` in Go) are illustrative,
not normative. The `stub` Go package is a test double (canned-outcome
scripting for tests and conformance), not a starting template. A
pipeline written in Python, a Lambda behind API Gateway, or a
long-running service in Kubernetes can all satisfy the protocol with
a thin wrapper.

## Categories of integration

### Agent-driven analyses

The wrapper hands user-facing prompts and tool catalogs to a Claude or
similar agent and consumes its outputs. `claude-agent` is the reference
shape: a dispatch produces a CLI invocation; `report_complete` (the
internal MCP tool) marshals the output back into an
`attributes_delta`; the wrapper validates against the
`attributes_schema` from the dispatch and either commits or asks the
agent to correct.

Use the **userdata schema** to lock the wrapper's input shape;
`Capabilities.userdata_schema` is enforced at template registration and
again at dispatch (`@blessed-invariant 12` extended). Use the
**named-event** wire type to surface non-terminal signals such as
intermediate findings, telemetry, or progress markers — those are
addressable later via `nodes.<emitter>.event.<name>.<path>`
substitution.

### Webhook-driven flows

The wrapper accepts an incoming webhook, kicks off the external work,
and emits `AwaitAsyncCallback` immediately. When the webhook return-payload
arrives the wrapper POSTs the new-shape async-callback body
(`{events: [...], terminal: {...}}`) to the supervisor's
callback URL. Events from the body are persisted before the terminal,
so any receiver with a `subscribes: [{node: <emitter>, on: event, name: <name>}]`
entry fires mid-flight.

This idiom is the right shape whenever the external work has its own
durable state and can be re-entered from outside; the executor is a
pure dispatcher.

### External-decision waits (parked)

Use `Park` whenever the wrapper needs to pause the run for an
out-of-band signal — a human approval, a downstream system's
completion, a rate-limit reset, a quorum vote.

`Park.resume_at` schedules a time-based wake. Omit it for
indefinite parks waiting on a signal. Resume happens via the admin
endpoint `POST /admin/instances/{instance}/nodes/{node}/invalidate` or
via an in-graph invalidate produced by the cascade walk (when another
node's transition matches a `subscribes:` entry that targets the
parked node). The executor reads `ResumeContext.payload` and
`ResumeContext.session_token` on the resume dispatch — so the wrapper
can stash whatever resumption state it needs.

The watchdog `max_park_duration` per-node cap fails the run with
`error_class: "park_timeout"` if a park exceeds the configured budget;
empty `max_park_duration` means unbounded.

## Worked example: a single-document analysis with human review

Suppose we want a small graph that runs an analysis, gates on human
review, then triggers a downstream summary.

```yaml
nodes:
  analysis:
    executor: project-alpha-agent
    userdata:
      cli:
        system_prompt: "Analyze the document at {{params.path}}"
    on_executor_complete:
      resolve: by_changed

  review_gate:
    executor: project-alpha-review
    subscribes:
      - { node: analysis, on: state, when: fresh, outcome: fresh_changed }

  summary:
    executor: project-alpha-agent
    attributes:
      schema:
        properties:
          source:
            source: nodes.analysis.attribute.findings
          decision:
            source: nodes.review_gate.event.approved.payload
```

`review_gate` subscribes to `analysis` completing and emits `Park` on
dispatch, waiting. A human approves through a project-built UI that
calls `POST /admin/instances/.../nodes/review_gate/invalidate`; the
executor resumes, emits `approved` (a named event with the reviewer
payload), then completes. `summary` auto-subscribes to both `analysis`
(via the attribute substitution) and `review_gate`'s `approved` event
(via the event substitution); it runs once both upstream waits drain.

## Antipattern: blocking a frame on review

Frame-blocking review (the analysis-and-review pattern above) does work
correctly — held frames stay held, parked nodes stay parked,
auto-terminal fires once the review completes. But it serializes
parallel work in the same frame and creates long-lived held frames in
operational dashboards.

When a review can be moved out of the producing frame's critical path,
prefer the **post-frame review** idiom: the producing graph runs to
completion; review happens externally; a follow-on graph or instance
kicks off post-review work. Reserve frame-blocking review for cases
where downstream genuinely cannot proceed without the approval.

See `docs/concepts/parked.md` for the parked-state mechanics this
section depends on.
