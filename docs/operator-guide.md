# Operator guide

This guide collects operator-visible knobs that span multiple concepts.
For per-concept references, see `docs/concepts/`. For protocol
implementation, see `docs/protocols/`.

## Configuration root: `RIMSKY_CONFIG`

Each rimsky binary reads its deployment-shape config from
`RIMSKY_CONFIG` (default `/etc/rimsky/rimsky.yml`). The unified file
declares persistence, named-locks, claim-producers, and executors.
A reference config ships at `deploy/rimsky.yml`.

Per-process tuning (concurrency, callback host, heartbeat) lives in
each process's own YAML or env vars and is read separately
(e.g. `RIMSKY_SUPERVISOR_CONFIG` for `rimsky-supervisor`).

## Persistence: blob backend

The `persistence.blob` block selects how attribute values, parked-state
payloads, and named-event payloads are stored when they exceed the
inline-spill threshold:

```yaml
persistence:
  driver: postgres
  postgres:
    dsn: ...
  blob:
    backend: pg-largeobject  # inline | pg-largeobject | filesystem | memory
    spill_threshold_bytes: 65536
    filesystem:
      root: /var/lib/rimsky/blobs
    pg_largeobject:
      schema: public
    retention:
      orphan_sweep_interval: 1h
      retention_after_unreferenced: 24h
```

Backend choices:

- `inline` — the default. No spill; large attribute values stay in the
  attribute table inline. Suitable for small-attribute workloads.
- `pg-largeobject` — postgres-large-object backend. Suitable for
  multi-host deployments. Uses the same DSN as the persistence driver.
- `filesystem` — files written under `filesystem.root`. Requires a
  shared volume in multi-host deployments.
- `memory` — in-process map. **Dev-only**: rejected at startup unless
  `RIMSKY_PROCESS_ROLE=unified` (set by `rimsky-entrypoint`). The
  per-process binaries (`rimsky-scheduler`, `rimsky-supervisor`,
  `rimsky-control-api`) cannot share state through an in-process map,
  so the gate prevents accidental misconfiguration.

`SweepOrphanedBlobs` runs in the foundation tick loop and reaps blob
handles whose retention window has elapsed. The blob backend itself
sees only `Delete(handle)`.

## claude-agent: MCP catalogs

The `claude-agent` reference executor reads its startup config from
`CLAUDE_AGENT_CONFIG` (default `/etc/claude-agent/config.yaml`):

```yaml
mcp_catalog:
  project-tracker:
    transport: http
    url: ${PROJECT_TRACKER_URL}
    headers:
      Authorization: ${PROJECT_TRACKER_TOKEN}
  workspace-files:
    transport: stdio
    command: project-fs-server
    args: ["--root", "/workspace"]
    lifetime: persistent

policy:
  allow_inline: false
  allow_modules_from: ["@project-alpha/*"]
```

Templates reference catalog entries by `ref`. `policy.allow_inline:
false` (the strict default) blocks templates from injecting
unconfigured MCP servers at dispatch time. `policy.allow_modules_from`
gates the `module` and `http-loopback` transports against an
allow-list of glob patterns. See `docs/executors/claude-agent/expected-attributes.md`
for the full expected-attributes schema.

## Observability: Prometheus metrics

Each rimsky binary can expose a `/metrics` endpoint:

- `rimsky-control-api` — `RIMSKY_METRICS_PORT` (0 = disabled, default).
  Bound to the same host as the control API.
- `rimsky-scheduler` — `RIMSKY_METRICS_PORT` and `RIMSKY_METRICS_HOST`
  (default `127.0.0.1`).
- `rimsky-supervisor` — same as scheduler.

The metric set is documented in `control/observability/metrics.go`.
Counters cover dispatches, terminal verdicts, invalidates, claim
acquisitions. Gauges cover nodes-by-state, parked-by-reason, held
frames, dispatch queue depth. Histograms cover dispatch latency,
claim acquisition latency, frame duration, and parked-duration-on-
resume.

## Diagnostic endpoints

The control API exposes:

- `GET /admin/diagnostics/held-frames` — frames currently held.
- `GET /admin/diagnostics/parked-nodes` — parked nodes; optional
  `?reason=<name>` filter.
- `POST /admin/instances/{instance}/nodes/{node_id}/invalidate` — admin
  invalidate. Dispatches by node state: `parked` resumes,
  `fresh` invalidates, `running`/`failed` returns 409.

`force-fire` (admin: `POST /admin/scheduled-nodes/{node_id}/force-fire`)
remains scheduled-node-specific. It bypasses cron-next-fire calculation
to fire a scheduled node immediately. It is a separate surface from
`/admin/instances/{instance}/nodes/{node_id}/invalidate`, which is the
general-purpose admin invalidation surface for any node state.

## Conformance binaries

Three conformance binaries ship under `cmd/`:

- `rimsky-executor-conformance` — exercises an executor against the protocol.
  Stub mode is mandatory for LLM-calling executors
  (`--require-stub-mode`).
- `rimsky-claim-producer-conformance` — exercises a claim-producer.
- `rimsky-blob-backend-conformance` — exercises a blob backend
  against the `BlobBackend` interface (in-process; pass
  `--backend <name>` plus the backend's required config).

Each exits 0 on all checks passing.

## Pre-v1 caveats

- The Helm chart at `deploy/kubernetes/rimsky-chart/` may lag behind
  binary env-var renames; verify before deploying.
- The unified image (`rimsky/all`) defaults to SQLite at
  `/var/lib/rimsky/state.db`. Replicas > 1 break (independent SQLite
  databases). Use the per-process images plus the postgres driver for
  multi-replica deployments.
- Pre-v1 has no backwards-compat guarantees on schema or wire shapes.
  Migrations may drop and recreate tables.
