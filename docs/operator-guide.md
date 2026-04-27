# Rimsky Operator Guide

This guide is written for the person who runs rimsky in production: deploying
the processes, writing the supervisor config, authoring templates, creating
and operating instances, scraping metrics, and diagnosing failures.

If you are authoring an executor (new language, new integration), see
`executor-author-guide.md`. If you are authoring a Go resource implementation,
see `resource-author-guide.md`. For the concept model, see
`node-graph-design.md`. For wire-format details, see `protocol.md`.

---

## 1. First 15 minutes — quickstart

This quickstart assumes only Docker and `curl` are installed. It brings up a
complete rimsky stack, deploys a template, creates an instance, and inspects
the event log — all against the repository-provided Docker Compose file.

### 1.1 Bring up the stack

```bash
cd rimsky-go/deploy
docker compose up -d
```

The stack includes:

| service | port | purpose |
| --- | --- | --- |
| `postgres` | 5544 | Rimsky's state database |
| `migrate` | — | One-shot; applies SQL migrations then exits |
| `scheduler` | — | Pure-cascade + schedule tick loop |
| `supervisor` | — | Dispatch loop; owns executor claims |
| `control-api` | 8080 | HTTP+JSON operator surface |
| `http-node` | 9091/9092 | Reference HTTP executor (gRPC + HTTP bridge) |
| `claude-agent` | 9090/9190 | Agentic executor (stub mode by default) |

Verify everything is up:

```bash
curl -s http://localhost:8080/health | jq .
```

Expected: `{"status":"ok","supervisors":[{...}],"node_counts":{...}}`.
If `supervisors` is empty, wait another 10 seconds — the supervisor writes its
first heartbeat row on its first tick.

### 1.2 Deploy your first template

```bash
cat > /tmp/hello.yaml <<'YAML'
name: hello-world
version: "1.0.0"
description: One node that pings httpbin.
nodes:
  - type: ping
    executor: http-node
    userdata:
      url: "https://httpbin.org/get"
      method: GET
YAML
```

Templates are submitted as JSON to the control API. Convert the YAML and
POST:

```bash
python3 -c 'import yaml, json, sys; print(json.dumps(yaml.safe_load(open("/tmp/hello.yaml"))))' \
  | curl -s -X POST http://localhost:8080/templates \
       -H 'Content-Type: application/json' -d @- | jq .
```

Expected response:
```json
{"template_id": "...", "name": "hello-world", "version": "1.0.0"}
```

### 1.3 Create an instance

```bash
TPL=<template_id from above>
curl -s -X POST http://localhost:8080/instances \
  -H 'Content-Type: application/json' \
  -d "{\"template_id\": \"$TPL\", \"consumer_key\": \"demo-1\"}" | jq .
```

Expected: `{"instance_id": "...", "consumer_key": "demo-1", "node_count": 1}`.

### 1.4 Watch the node run

```bash
INST=<instance_id>
curl -s "http://localhost:8080/instances/$INST/nodes" | jq '.nodes[].state'
```

Within a few seconds the node transitions `stale -> running -> fresh`. Tail
the event log:

```bash
curl -s "http://localhost:8080/events?instance_id=$INST" | jq '.events[].kind'
```

You should see `node_state_change`, `executor_complete`, and
`resource_commit` kinds.

### 1.5 Tear down

```bash
docker compose down -v
```

---

## 2. Deployment

### 2.1 Docker Compose (development, staging)

`deploy/docker-compose.yml` is the canonical reference. Copy it into your
environment and customize:

1. `postgres.image` — pin to your preferred Postgres tag; rimsky supports 13+.
2. `postgres.volumes` — replace the named volume with a bind-mount or managed
   block device for durability.
3. `supervisor.volumes` — point at your own `supervisor-config.yml` (see §3).
4. `claude-agent.environment.CLAUDE_STUB_MODE` — set to `0` and provide
   `ANTHROPIC_API_KEY` to enable real agent execution.

Bring up only infrastructure for local development:

```bash
docker compose up -d postgres migrate
```

Bring up the full stack:

```bash
docker compose up -d
```

Logs:

```bash
docker compose logs -f scheduler supervisor control-api
```

### 2.2 Helm / Kubernetes (production)

`deploy/kubernetes/rimsky-chart/` is the canonical Helm chart. Install:

```bash
helm install rimsky deploy/kubernetes/rimsky-chart \
  --set postgres.dsn="postgres://rimsky:pw@pg.example:5432/rimsky?sslmode=require" \
  --set claudeAgent.anthropicApiKey="$ANTHROPIC_API_KEY" \
  --set claudeAgent.stubMode="0"
```

Chart values of interest (see `values.yaml` for the full list):

| key | default | notes |
| --- | --- | --- |
| `postgres.dsn` | *(required)* | DSN must include the database the migrations run into |
| `scheduler.replicas` | 1 | Leader election is cooperative; running >1 is safe but unnecessary |
| `supervisor.replicas` | 2 | Scale out for throughput or isolation |
| `supervisor.concurrency` | 8 | Total concurrent nodes per supervisor |
| `controlapi.replicas` | 2 | Stateless; scale for HA |
| `controlapi.ingress.enabled` | false | Enable + supply host to expose externally |
| `httpNode.replicas` | 2 | Reference HTTP executor |
| `claudeAgent.replicas` | 1 | Non-idempotent work; scale with caution |
| `claudeAgent.stubMode` | `"1"` | `"0"` for real Anthropic calls; `"1"` for conformance/CI |

Post-install smoke test:

```bash
kubectl port-forward svc/rimsky-control-api 8080:8080 &
curl -s http://localhost:8080/health | jq .
```

### 2.3 What lives where

Rimsky writes to exactly one schema: the migrations in `core/migrations/`
create tables prefixed `rimsky_*` (`rimsky_templates`, `rimsky_instances`,
`rimsky_nodes`, `rimsky_resources`, `rimsky_resource_versions`,
`rimsky_dispatch`, `rimsky_events`, `rimsky_schedules`, `rimsky_supervisors`,
`rimsky_migrations`). Nothing else in your database should be named
`rimsky_*`.

External-SQL resources write to caller-owned tables you declare in the
template; rimsky never creates those tables — you do, via your own migration
tooling.

---

## 3. Supervisor configuration

The supervisor reads one YAML file at `RIMSKY_CONFIG_PATH` (default in the
Docker image: `/etc/rimsky/supervisor-config.yml`). Example:

```yaml
supervisor:
  heartbeat_interval_ms: 5000
  callback_addr: ":9100"
  concurrency:
    total: 8
    per_executor:
      claude-agent: 4
      http-node: 8

executors:
  - name: claude-agent
    transport: grpc
    endpoint: "claude-agent:9090"
    concurrency: 4
  - name: http-node
    transport: grpc
    endpoint: "http-node:9091"
    concurrency: 8

sql_connections:
  analytics_production:
    dsn: "postgres://app:pw@pg-production:5432/analytics?sslmode=require"
    max_conns: 20
```

### 3.1 `supervisor` block

| field | purpose |
| --- | --- |
| `heartbeat_interval_ms` | how often the supervisor updates `rimsky_supervisors.last_heartbeat_at` |
| `callback_addr` | bind address for the HTTP+JSON callback endpoint async executors POST to |
| `concurrency.total` | hard cap on in-flight dispatch claims per supervisor |
| `concurrency.per_executor` | per-name soft cap; tighter than total |

### 3.2 `executors` list

Every executor the supervisor is willing to dispatch must appear here. A
template that references an executor not in this list will fail at instance
creation with `unknown executor`.

| field | purpose |
| --- | --- |
| `name` | matches `node.executor` in templates |
| `transport` | `grpc` or `http` (see `protocol.md`) |
| `endpoint` | host:port for gRPC, full URL for HTTP |
| `concurrency` | executor-specific claim cap |

### 3.3 `sql_connections` (optional)

Named pgx pools made available to `external-sql` resources. Template configs
reference a pool by name via `connection_ref`. If a template references a
`connection_ref` not defined here, instance creation fails at
`external-sql: connection_ref "X" not configured`.

### 3.4 Stores configuration (`stores.yml`)

In addition to the supervisor config above, **all three runtime processes
(`rimsky-scheduler`, `rimsky-supervisor`, `rimsky-control-api`) load a shared
stores config** at startup. Stores are pure runtime objects built from this
YAML — there is no `rimsky_stores` database table. Templates reference stores
by name; resolution mirrors executor name resolution.

**Env var:** `RIMSKY_STORES_CONFIG` — path to the YAML file. Default
`/etc/rimsky/stores.yml`. The file must be readable by every process that
loads it; supervisor pool specialization is achieved by giving each pool a
different file (the supervisor writes its `accepted_stores` into
`rimsky_supervisors` at registration; dispatch eligibility filters out nodes
whose `required_stores` are not in the local pool).

#### 3.4.1 Schema

```yaml
stores:
  <name>:
    kind: filesystem | claim_store
    # kind-specific fields follow:
    ...
```

**Filesystem direct-mode** (used for content the executor reads/writes
directly off disk):

```yaml
stores:
  content:
    kind: filesystem
    mode: direct
    root: /workspace/content
```

`root` must exist and be readable/writable by the supervisor and by every
executor that mounts it. In docker-compose this is a shared named volume; in
Kubernetes it is a `PersistentVolumeClaim` mounted into all participating
pods.

**Claim store (postgres backend)** (queues, ring buffers, work tables):

```yaml
stores:
  inbound:
    kind: claim_store
    backend: postgres
    items_table: inbound_items
    on_commit_default:  delete             # or release_to_back / release_to_head
    on_give_up_default: release_to_head    # or release_to_back / delete
    visibility_timeout_seconds: 300
```

`on_commit_default` and `on_give_up_default` choose queue (`delete` /
`release_to_head`) vs. ring-buffer (`release_to_back`) semantics; nodes can
override per-claim via `claim_resolutions` in the template.
`visibility_timeout_seconds` is a backstop only — rimsky's heartbeat
release runs first (see §7.3 in the spec for the full ordering); set it to at
least `2 × 5 × heartbeat_interval`.

#### 3.4.2 Operator-owned items table for `claim-store-postgres`

The `items_table` is **operator-owned**: rimsky never creates, migrates, or
drops it. Create it out-of-band before any process loads `stores.yml` —
otherwise factory `Build` fails fast at startup with a "table missing or
malformed" error.

Required schema (one table per claim store; substitute your `<items_table>`
name):

```sql
CREATE TABLE <items_table> (
    item_id     UUID PRIMARY KEY,
    payload     JSONB NOT NULL,
    enqueued_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    state       TEXT NOT NULL DEFAULT 'available',  -- 'available' | 'in_progress' | 'dead_letter'
    claim_token UUID,                               -- non-null when state='in_progress'
    claimed_at  TIMESTAMPTZ                         -- non-null when state='in_progress'
);
CREATE INDEX <items_table>_available_idx
    ON <items_table> (enqueued_at) WHERE state = 'available';
CREATE INDEX <items_table>_in_progress_idx
    ON <items_table> (claim_token) WHERE state = 'in_progress';
```

`dead_letter` rows are produced by the supervisor when a node hits `give_up`
and the claim's `on_give_up = delete`; inspect with
`SELECT … WHERE state='dead_letter'`. Manual SQL flips the row back to
`available` when you're ready to retry. There is no automated re-enqueue.

Populate items either via direct SQL or via the admin API endpoint
`POST /admin/claim-stores/:name/items` (see §5.5).

The compose stack ships a one-shot `init-items` service that creates the
default `topics_items` table; the Helm chart does not yet run an equivalent
pre-install Job (known gap; see CHANGELOG).

---

## 4. Template authoring

A template is a YAML (or JSON) document describing the node graph. It is
submitted once via `POST /templates`; each `POST /instances` against that
template creates a fresh copy of the graph with concrete IDs and
consumer-specific params resolved into placeholders.

### 4.1 Schema walkthrough

```yaml
name: ingest-source                 # stable identifier, unique per version
version: "1.2.0"                    # freeform; operators pin by ID, not name+version
description: |                      # free text, surfaced in GET /templates/:id
  Ingest source data, normalize, and commit into resource tables.

params_schema:                      # JSON Schema for POST /instances {params}
  type: object
  properties:
    name:   { type: string }
    region: { type: string }
  required: [name, region]

params_redact:                      # field names to redact in GET responses
  - api_key

nodes:                              # the graph. order is irrelevant; deps drive ordering.
  - type: discover                  # node_type, unique within template
    executor: http-node             # name from supervisor-config.executors
    userdata:                       # opaque to rimsky; passed through to executor
      url: "https://example.com/{params.name}/layers"
      method: GET
    owns_resources:                 # resources this node produces
      - path: ["discovery", "{consumer_key}"]
        implementation: inline-jsonb
        config:
          keep_versions: 3
    error_types:                    # optional per-error-class policy chain
      http_unexpected_status:
        policy:
          - { action: retry, count: 2, backoff: exponential, base_delay_ms: 30000 }
          - { action: give_up }

  - type: transform
    executor: http-node
    dependencies: [discover]        # runs after `discover` reaches fresh
    userdata:
      url: "http://transformer:8080/run"
      method: POST
      body: { "discovery": "{{deps.discover}}" }
    owns_resources:
      - path: ["transformed", "{consumer_key}"]
        implementation: inline-jsonb

  - type: nightly-refresh
    executor: http-node
    schedule: "0 2 * * *"           # cron expression; invalidates deps chain at fire time
    userdata:
      url: "http://refresh:8080/go"
    dependencies: [transform]
```

### 4.2 Common patterns

**Pure-cascade fan-out.** A node with no `executor` and no `schedule` is a
pure-cascade node: it contributes to dependency wiring but never dispatches.
Use it to fan out one producer into multiple reader chains without duplicating
execution. Declare it with `dependencies: [...]` only.

**Schedule-driven ingestion.** A top-of-graph node with a `schedule:` cron
invalidates itself (and cascades staleness down) at each fire time. Children
with their own `executor` re-run because their dep's version changed. This is
the idiomatic shape for periodic ingestion.

**Rollback policies.** Declare `error_types.<class>.policy` as an ordered
list of actions the scheduler walks on repeated failures. Actions:

| action | semantics |
| --- | --- |
| `retry` | re-dispatch after `base_delay_ms * backoff` (exponential or fixed) up to `count` times |
| `rollback` | restore named resources' previous version; re-dispatch once |
| `give_up` | mark node `failed`; human intervention required |

Policies that end without `give_up` implicitly fail open: the scheduler stops
acting on the error but the node state remains `running` until something
terminates it. Always end chains with `give_up`.

**Resource reads.** `reads_resources:` declares explicit read dependencies.
The executor receives the current version of each read resource at dispatch
time via `ExecuteRequest.reads_data`.

### 4.3 Validation

Before a template is accepted, the control API validates:

- `name`, `version` non-empty
- every `executor` name is registered (checked by the template validator via
  the resource/executor factories)
- every `implementation` name is registered as a resource factory
- every `dependencies[]` entry resolves to another node in the same template
- every `owns_resources[].config` validates against that implementation's
  `ConfigSchema()`
- placeholder strings reference known keys (`{consumer_key}`, `{instance_id}`,
  `{params.<key>}` where `<key>` appears in `params_schema`)

Validation errors come back as HTTP 400 with a `validation_errors` array.

---

## 5. Control API operations

All endpoints accept and return `application/json`. Paginated reads support
`?limit=N&cursor=<opaque>`.

### 5.1 Templates

**Deploy a template:**
```bash
curl -s -X POST http://localhost:8080/templates \
  -H 'Content-Type: application/json' \
  -d @template.json
```

**List templates:**
```bash
curl -s 'http://localhost:8080/templates?name=ingest-source&limit=20'
```

**Get a template (full spec):**
```bash
curl -s "http://localhost:8080/templates/$TPL"
```

**Delete a template (only if unreferenced):**
```bash
curl -s -X DELETE "http://localhost:8080/templates/$TPL"
```

Returns 409 `ErrTemplateInUse` if any instance still references it.

### 5.2 Instances

**Create an instance:**
```bash
curl -s -X POST http://localhost:8080/instances \
  -H 'Content-Type: application/json' \
  -d '{
    "template_id": "'"$TPL"'",
    "consumer_key": "alpha",
    "params": {"name": "alpha", "region": "r1"}
  }'
```

**List instances:**
```bash
curl -s 'http://localhost:8080/instances?template_id='"$TPL"
```

**Get an instance (by UUID or consumer_key):**
```bash
curl -s "http://localhost:8080/instances/alpha"
```

**List an instance's nodes:**
```bash
curl -s "http://localhost:8080/instances/alpha/nodes" | jq '.nodes[] | {node_type, state}'
```

**Delete an instance:**
```bash
curl -s -X DELETE "http://localhost:8080/instances/alpha"
```

### 5.3 Operator overrides

Overrides are emergency levers. Every override emits an `operator_override`
event so audits can reconstruct who moved the state machine.

**Invalidate a node** (mark stale, re-dispatch):
```bash
curl -s -X POST http://localhost:8080/nodes/$NODE/invalidate \
  -H 'Content-Type: application/json' \
  -d '{"reason": "upstream data corrected"}'
```

Under frame resolution (see `docs/specs/2026-04-26-frame-resolution-design.md`)
operator invalidates do **not** preempt running work. Each invalidate goes
through `frame.EnqueueOrCoalesce`: `serial_queue` templates queue a new frame
that runs after the in-flight one completes; `coalesce` templates fold the
invalidate into the pending coalesce row that runs as a single trailing frame
at frame-end. There is no kill mechanism — the `kill_requested` column was
removed and the `POST /nodes/{id}/kill` route was deleted. To force a re-run
"now," wait for the in-flight frame to complete; the queued / coalesced frame
runs next.

**Reset a failed node** (only legal from `state=failed`):
```bash
curl -s -X POST http://localhost:8080/nodes/$NODE/reset
```

Clears the error counters and moves the node to `stale` so it becomes eligible
for dispatch again.

### 5.3.1 Frame resolution and templates

Templates declare a required `frame_resolution` field (`coalesce` or
`serial_queue`) and an optional `frame_timeout_ms` (default 600000 = 10 min;
floor 60000). Control-api rejects template uploads missing or with invalid
values for these fields.

Inspect frame state per instance:
```bash
psql -c "SELECT frame_id, mode, state, source_node_ids, queued_at, started_at, ended_at
         FROM rimsky_frames WHERE instance_id = $INSTANCE
         ORDER BY queued_at;"
```
At most one frame is in `running` state per instance at any time. Frames in
`queued` state advance on the next scheduler tick once the running frame
ends. Frames that exceed `frame_timeout_ms` with no live executor work are
reaped to `failed` by the scheduler.

### 5.4 Event log

The event log is append-only and the authoritative audit trail. Filter by any
combination of `instance_id`, `node_id`, `kind`, `since`, `until` (RFC3339).

```bash
curl -s "http://localhost:8080/events?instance_id=$INST&since=2026-04-22T00:00:00Z&limit=200"
```

Common kinds:

| kind | when |
| --- | --- |
| `node_state_change` | every stale↔running↔fresh↔failed transition |
| `executor_complete` | successful terminal from executor |
| `executor_errored` | application-level error |
| `executor_blocked` | executor returned Blocked |
| `resource_commit` | new version persisted |
| `resource_rollback` | restored previous (or named) version |
| `operator_override` | invalidate / reset / kill |
| `orphaned_claim_released` | supervisor heartbeat sweep reclaimed a claim |
| `heartbeat_timeout` | in-flight node exceeded heartbeat cutoff |

### 5.5 Admin endpoints

All routes under `/admin/...` are gated by the global authenticator wired
into the control-api process. Operators that want admin-only access wire an
`Authenticator` that checks the `X-Rimsky-Admin-Token` request header against
their configured token; processes started without an authenticator leave
these routes anonymous (consistent with the rest of the API in pre-v1).

**Insert items into a postgres claim store:**

```bash
curl -s -X POST http://localhost:8080/admin/claim-stores/inbound/items \
  -H 'Content-Type: application/json' \
  -H 'X-Rimsky-Admin-Token: <token>' \
  -d '{
    "items": [
      {"payload": {"area": "A_1", "subtopic": "S_1"}},
      {"payload": {"area": "A_2", "subtopic": "S_2"}}
    ]
  }'
```

Response: `201 Created` with `{"inserted": <n>}`. Bulk-inserts each
`items[*].payload` into the operator-owned items table backing the named
claim store. Errors:

- `400` — empty `items` array, missing/invalid JSON in any payload, or store
  is not a postgres claim store (`kind != claim_store / backend != postgres`)
- `404` — no store registered under that name in the loaded `stores.yml`
- `503` — control-api was started without a store registry (mis-wired)

Rimsky itself never enqueues into a claim store; this endpoint exists for
operators and external producers who prefer HTTP over direct SQL.

**Force-fire a scheduled node (set `next_fire_at = now()`):**

```bash
curl -s -X POST http://localhost:8080/admin/scheduled-nodes/$NODE/force-fire \
  -H 'X-Rimsky-Admin-Token: <token>'
```

Response: `204 No Content` the moment the row is updated. The handler does
**not** wait for the cascade — the next scheduler tick picks up the row and
fires the schedule. Callers that need to observe the fire (e.g. acceptance
tests) poll `rimsky_nodes.state` or the events table separately.

This is the same primitive the §19.2 smoke fixture uses to drive the source
node 100 times in a row: with `RIMSKY_SCHEDULER_TICK_MS` set low (50ms in the
fixture), each force-fire round-trips sub-second under stub executors. In
production, prefer `POST /nodes/:id/invalidate` for one-off re-runs (it
participates in the operator-override audit log); use `force-fire` only when
you specifically need the schedule to advance via the regular scheduler tick
path (e.g. you want `last_fire_at` to update).

---

## 6. Monitoring

### 6.1 Health

Every long-running process exposes `/health`:

- `control-api`: `GET :8080/health` → supervisor list + node-state counts
- `scheduler`: `GET :9097/health` (Prometheus port doubles as liveness)
- `supervisor`: liveness probe via `/health` on the metrics port
- Executors: depend on image; `http-node` and `claude-agent` each expose
  `/health` on their metrics port

A healthy cluster has at least one supervisor row with
`last_heartbeat_at` within 2× `heartbeat_interval_ms` of now.

### 6.2 Postgres queries

When the API is unavailable or diagnosis needs raw data, these queries cut
straight to Postgres:

**Currently running nodes:**
```sql
SELECT id, node_type, instance_id, assigned_supervisor_id,
       last_heartbeat_at, now() - last_heartbeat_at AS age
  FROM rimsky_nodes
 WHERE state = 'running'
 ORDER BY last_heartbeat_at ASC;
```

**Stuck dispatch rows (claimed > 2 minutes, no heartbeat):**
```sql
SELECT d.node_id, d.claimed_by, d.claimed_at,
       n.state, n.last_heartbeat_at
  FROM rimsky_dispatch d
  JOIN rimsky_nodes n ON n.id = d.node_id
 WHERE d.claimed_by IS NOT NULL
   AND d.claimed_at < now() - interval '2 minutes'
   AND (n.last_heartbeat_at IS NULL
        OR n.last_heartbeat_at < now() - interval '2 minutes');
```

**Recent errors across the cluster:**
```sql
SELECT e.occurred_at, e.kind, e.node_id, e.payload
  FROM rimsky_events e
 WHERE e.kind IN ('executor_errored', 'executor_blocked', 'heartbeat_timeout')
   AND e.occurred_at > now() - interval '1 hour'
 ORDER BY e.occurred_at DESC
 LIMIT 100;
```

**Supervisors and their last heartbeats:**
```sql
SELECT id, accepted_executors, active_node_count, last_heartbeat_at,
       now() - last_heartbeat_at AS since_heartbeat
  FROM rimsky_supervisors
 ORDER BY last_heartbeat_at DESC;
```

**Instances ranked by node-state progress:**
```sql
SELECT i.consumer_key,
       count(*) FILTER (WHERE n.state='fresh')   AS fresh,
       count(*) FILTER (WHERE n.state='running') AS running,
       count(*) FILTER (WHERE n.state='stale')   AS stale,
       count(*) FILTER (WHERE n.state='failed')  AS failed
  FROM rimsky_instances i
  JOIN rimsky_nodes n ON n.instance_id = i.id
 GROUP BY i.consumer_key
 ORDER BY failed DESC, stale DESC;
```

---

## 7. Common failure modes

### 7.1 Node stuck `running`

**Symptom:** `state='running'` but `last_heartbeat_at` is older than a few
minutes; event log shows no terminal event.

**Diagnosis:**
1. Is the assigned supervisor still alive? Check
   `rimsky_supervisors.last_heartbeat_at` for
   `n.assigned_supervisor_id`.
2. Is the executor process alive? `kubectl logs` or
   `docker compose logs <executor>`.
3. Did the executor emit an async-accepted? `GET /events?node_id=<id>` —
   look for `executor_async_accepted`. If yes, the work is outside rimsky
   and rimsky is waiting for the callback.

**Remediation:** if the supervisor is dead, the heartbeat-loss sweep will
release the orphan claim within `2 × heartbeat_interval_ms`. Watch for the
`orphaned_claim_released` event. If the sweep does not fire (e.g., scheduler
is also down), restart the scheduler process; it runs the sweep on startup.

If the sweep runs but the node immediately re-claims and re-stalls, the
root cause is in the executor — stop dispatch and diagnose the executor
directly.

### 7.2 Supervisor unreachable

**Symptom:** `GET /health` lists no supervisors, or the supervisor row is
stale (`last_heartbeat_at` > 30s old).

**Diagnosis:**
```sql
SELECT id, last_heartbeat_at, now() - last_heartbeat_at AS age
  FROM rimsky_supervisors
 ORDER BY last_heartbeat_at DESC
 LIMIT 5;
```

**Remediation:** restart the supervisor; orphan sweep will release any
claims the dead supervisor was holding within the sweep interval. No
operator action is required to recover in-flight work beyond bringing a
supervisor back up.

### 7.3 Orphaned claims

**Symptom:** event log shows `orphaned_claim_released` entries; nodes
that had been running for a while are back in `stale`.

**Diagnosis:** this is normal recovery behavior. Expected after a
supervisor crash, OOM, or scale-down. Payload carries the prior
`claimed_by` and claim age.

**Remediation:** none. The scheduler will re-enqueue the affected
dispatch rows; another supervisor (or the restarted one) will pick
them up.

If you see *continuous* orphaned-claim-released events against the same
supervisor, that supervisor is failing its heartbeat writes — check its
Postgres connectivity and logs.

### 7.4 Template validation rejects a field that looks right

**Symptom:** `POST /templates` returns 400 with
`validation_errors: [{path, msg}]`.

**Diagnosis:** the `path` points to the offending node or resource.
Common causes:

- `unknown executor "foo"` — name not in supervisor config
- `unknown resource implementation "foo"` — no factory registered; check the
  process logs for factory registration at startup
- `config: required property "connection_ref" missing` — the resource impl's
  config schema was not satisfied

### 7.5 External-SQL commits fail during instance creation

**Symptom:** `POST /instances` returns 500 with
`externalsql: probe schema.table: <pg error>`.

**Diagnosis:** `external-sql` probes the target table at Factory.Create via
`SELECT 1 FROM schema.table LIMIT 0`. The table must exist. Create it
(and its `__staging` / `__previous` tables) out-of-band before creating the
instance.

### 7.6 `claude-agent` appears to hang on every dispatch

**Symptom:** nodes using `claude-agent` sit `running` for a long time.

**Diagnosis:** check `CLAUDE_STUB_MODE`. In stub mode (`"1"`), claude-agent
short-circuits and always returns instantly. If stub mode is off and you
see hangs, verify `ANTHROPIC_API_KEY` is set and check the executor's
metrics endpoint for rate-limit errors.

---

## 8. Upgrade procedure

1. Back up Postgres.
2. Apply new migrations in a maintenance window:
   ```bash
   docker compose run --rm migrate
   ```
   Migrations are idempotent; re-running an applied migration is a no-op.
3. Rolling-restart supervisors, then scheduler, then control-api. Executors
   last. Order preserves dispatch claim semantics: supervisors drain
   their claims gracefully when SIGTERM'd; scheduler pauses tick;
   control-api is stateless.
4. Verify:
   ```bash
   curl -s http://localhost:8080/health | jq .
   ```

### 8.1 Adopting the stores redesign (pre-v1, dev-only)

Rimsky is pre-v1: there is no production data preservation guarantee. The
stores redesign rewrites `core/migrations/001-initial.sql` in place and drops
the legacy `rimsky_resources` / `rimsky_resource_versions` tables. Because
`rimsky_migrations` already records `001-initial.sql` as applied on existing
dev databases, the migration runner will skip the rewritten file and the
schema will be wrong.

**Required step: nuke the dev database before running the new code.** This
applies only to dev / staging environments adopting the redesign — there is
no production-data migration path, by design.

Compose:

```bash
docker compose -f deploy/docker-compose.yml down -v   # -v drops the postgres volume
docker compose -f deploy/docker-compose.yml up -d     # fresh DB; migrate runs cleanly
```

Kubernetes / external Postgres: drop and recreate the rimsky database (or
drop the `rimsky_*` tables plus `rimsky_migrations`) before bringing up the
new images. The migration runner then re-applies `001-initial.sql` as the
new end-state schema.

After bringing the stack up:

1. Create any operator-owned claim-store items tables per §3.4.2 (the
   compose `init-items` service handles `topics_items` automatically).
2. Verify `RIMSKY_STORES_CONFIG` resolves to a readable `stores.yml` on
   scheduler / supervisor / control-api (default `/etc/rimsky/stores.yml`).
3. `curl -s http://localhost:8080/health` to confirm at least one supervisor
   has registered.

## 9. Appendix — useful one-liners

**Watch node state changes for an instance in near-real-time:**
```bash
watch -n 2 "curl -s http://localhost:8080/instances/$INST/nodes | \
            jq -r '.nodes[] | [.node_type, .state] | @tsv'"
```

**Tail events of a specific kind:**
```bash
LAST=""
while true; do
  curl -s "http://localhost:8080/events?kind=executor_errored&limit=50" | \
    jq -c '.events[]' | grep -vF "$LAST" || true
  sleep 5
done
```

**Dump a template's full spec:**
```bash
curl -s "http://localhost:8080/templates/$TPL" | jq '.spec'
```

**Force-delete every instance of a template (emergency only):**
```bash
for k in $(curl -s "http://localhost:8080/instances?template_id=$TPL" | jq -r '.instances[].consumer_key'); do
  curl -s -X DELETE "http://localhost:8080/instances/$k"
done
```
