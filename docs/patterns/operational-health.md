---
concept: operational-health
definition: |
  How operators observe and maintain a running rimsky deployment: lifecycle subscribers for audit and per-tenant rollups, retry-loop detection, parked-node watchdogs, frame-stuck warnings.
proto_symbol: (none)
config_field: (none)
api_surface: GET /v1/observability/summary
related: [lifecycle-subscriber, parked, frame, retry-no-progress]
deprecated_terms: []
---

# Operational health

Rimsky is a long-running platform; operators who run it need
visibility into what is healthy, what is degraded, and what is wedged.
This page covers the surfaces rimsky exposes for that and the
operational patterns that compose them.

## Surfaces

### Lifecycle-subscriber services

Any service registered with `protocols: [claim_producer, lifecycle_subscriber]`
in `rimsky.yml` receives the six lifecycle events at the points they
fire (template registered/deployed/undeployed/deregistered, instance
created/terminated). Wire a domain-shaped audit log to a
LifecycleSubscriber and the audit trail composes for free. The events
are persistently idempotent (rimsky records each `(producer,
scope_kind, scope_id)` triple it has already fired); a
re-fire under the same `(producer, scope_kind, scope_id)` is a no-op
on the consumer side.

### Watchdog graphs

A graph instance can run forever and check itself: a scheduled node
that polls a metric, queries the control API, and either sleeps until
the next tick or invalidates a downstream remediation node when an
alarm condition fires. This pattern lets operators express incident
response as a graph rather than as ad hoc scripts.

The scheduling primitives:
- `cron:` on a node fires it at the configured cadence.
- `force-fire` on a scheduled node bypasses the cadence (admin-only).
- `on_executor_complete` with `invalidate` chains the watchdog into a
  remediation graph.

### Control-API polling

The control API exposes JSON endpoints suitable for polling from
dashboards or external monitors:

- `GET /admin/diagnostics/held-frames` — frames currently held
  pending node completion. Held frames are normal during agent-driven
  work; persistently held frames may indicate stuck reviews.
- `GET /admin/diagnostics/parked-nodes` — parked nodes with their
  reasons and resume timestamps. Optional `?reason=<name>` filter.
- `GET /metrics` — Prometheus text format on the per-process
  `RIMSKY_METRICS_PORT` (default disabled). The metric set covers
  dispatches by terminal class, claim acquisitions by producer, node
  state gauges, parked-by-reason gauges, dispatch latency
  histograms, and held-frame counts.

### Admin invalidate

`POST /admin/instances/{instance}/nodes/{node_id}/invalidate` is the
operator escape hatch. It dispatches by node state:

- `parked` → resume with `resume_reason: "external_invalidate"`.
- `fresh` → standard invalidate (state → stale; cascade picks up next
  scheduler tick).
- `running` or `failed` → 409 Conflict.

Use it when something has wedged on a signal that didn't arrive (a
review never came back, a webhook never fired). For all other states,
use the normal cascade or template-driven mechanisms.

## Patterns

### Single dashboard for held + parked + retries

Compose `held-frames`, `parked-nodes`, and the
`rimsky_dispatches_total` metric (filtered by terminal class) into one
operator dashboard. Persistent growth in any of the three signals
something to investigate.

### Per-tenant SLA observability

Templates can be tagged with the consumer they belong to. The
lifecycle-subscriber service receives that tag at template-deployed
time and can surface per-tenant rollups in the operator's monitoring
stack.

### Detect retry loops with no progress

The `max_retries_without_progress` cap (default 100; 0 disables;
configurable per-node) forces `error_class: "retry_loop_no_progress"`
when the same `settling_signal_type` is observed N times in a row.
Combined with the
`rimsky_terminal_verdicts_total{error_class="retry_loop_no_progress"}`
metric, retry loops surface in alerts before they exhaust budget.

### Graceful degradation

A graph that depends on an external service can wrap the dependency in
a node whose `error_types:` chain resolves the relevant error class
to `pass` — the run settles fresh and downstream subscribers wired to
`type: terminal/error/*` for that node can react (e.g., dispatch a
"degraded mode" sibling). The graph keeps moving; the operator sees
degraded-mode telemetry and can intervene.

## What rimsky does not provide

Rimsky's surfaces are JSON over HTTP plus Prometheus metrics. Any
higher-level dashboarding (alerting rules, log aggregation, paging)
lives in the operator's existing observability stack — Grafana,
Datadog, PagerDuty, or whatever the project standardizes on.
Rimsky's role is to expose the underlying signals; the operator's
role is to compose them.
