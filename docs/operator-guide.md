# Rimsky Operator Guide

> v3 spec at `docs/specs/2026-04-27-stores-redesign-v3-design.md` is the
> authoritative contract; this guide is the operator-facing summary.

---

This guide is written for the person who runs rimsky in production: deploying
the processes, writing the supervisor config, authoring templates, creating
and operating instances, scraping metrics, and diagnosing failures.

If you are authoring an executor (new language, new integration), see
`executor-author-guide.md`. If you are authoring a Go store implementation,
see `store-author-guide.md`. For the concept model, see
`node-graph-design.md`. For wire-format details, see `protocol.md`. The
authoritative vocabulary (claim, named lock, region, selector, address,
payload, intent, alias, write_semantics, pick policy, etc.) lives in
`glossary.md`.

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
{"template": "sha256-<64-hex>", "name": "hello-world", "version": "1.0.0"}
```

### 1.3 Create an instance

```bash
TPL=<template content hash from above>
curl -s -X POST http://localhost:8080/instances \
  -H 'Content-Type: application/json' \
  -d "{\"template\": \"$TPL\", \"instance_key\": \"demo-1\"}" | jq .
```

Expected: `{"instance_id": "...", "instance_key": "demo-1", "node_count": 1}`.

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
`attributes_committed` kinds.

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
`rimsky_nodes`, `rimsky_dispatch`, `rimsky_events`, `rimsky_schedules`,
`rimsky_supervisors`, `rimsky_node_attributes`, `rimsky_lock_holders`,
`rimsky_claim_holders`, `rimsky_frames`, `rimsky_migrations`). Nothing else
in your database should be named `rimsky_*`.

Postgres-store pick-policy **items tables** are caller-owned (you declare
their names in `rimsky.yml` per §3.4.3 and create them out-of-band). Rimsky
never creates those tables.

---

## 3. Supervisor configuration

The supervisor reads two YAML files:

- `RIMSKY_SUPERVISOR_CONFIG` (required; per-process tuning) — Postgres URL,
  concurrency, heartbeat, callback bind/advertise. Example
  `/etc/rimsky/supervisor-config.yml`:

  ```yaml
  postgres_url: "postgres://rimsky:rimsky@db:5432/rimsky?sslmode=disable"
  supervisor_id: "supervisor-1"      # optional; defaults to <hostname>-<pid>
  concurrency: 8
  heartbeat_interval_ms: 5000
  claim_poll_interval_ms: 1000
  callback:
    host: "0.0.0.0"                  # bind host for the callback HTTP listener
    port: 9100
    advertise_host: "supervisor"     # peer-reachable hostname executors dial back
    advertise_port: 9100
  ```

- `RIMSKY_CONFIG` (optional; defaults to `/etc/rimsky/rimsky.yml`) — shared
  deployment-shape config: `executors:`, `stores:`, `named_locks:`. The
  supervisor, control-api, and scheduler all read this file. The
  `executors:` block declared here is the source of truth for executor
  endpoints; templates that reference an executor not declared here fail at
  registration with `unknown executor`. The `stores:` and `named_locks:`
  blocks are documented in §3.4.

### 3.1 Supervisor-tuning fields

| field | purpose |
| --- | --- |
| `postgres_url` | DSN for the rimsky Postgres database (required) |
| `supervisor_id` | unique ID across running supervisors; defaults to `<hostname>-<pid>` |
| `concurrency` | hard cap on in-flight dispatch claims per supervisor |
| `heartbeat_interval_ms` | how often the supervisor updates `rimsky_supervisors.last_heartbeat_at` |
| `claim_poll_interval_ms` | how often the supervisor polls for new claims |
| `callback.host` / `callback.port` | bind address for the HTTP+JSON callback endpoint async executors POST to |
| `callback.advertise_host` / `callback.advertise_port` | peer-reachable host:port embedded in the `callback_url` handed to executors (override via `RIMSKY_SUPERVISOR_CALLBACK_ADVERTISE_{HOST,PORT}`) |

### 3.2 Executors in `rimsky.yml`

Every executor the supervisor is willing to dispatch must appear in the
`executors:` block of `rimsky.yml`. A template that references an executor
not declared there fails registration with `unknown executor`. The schema:

```yaml
executors:
  <name>:
    transport: grpc | http     # see protocol.md
    endpoint:  "<host>:<port>" # gRPC target, or full URL for http
    tls:       off | on        # optional; default off
```

The reference example is `deploy/rimsky.yml`. Control-api validates
`executors[<n>]` references at template registration; supervisors dispatch
through `core/executor`'s static resolver wired against the same map.

### 3.4 Stores and named-locks configuration (`rimsky.yml`)

In v3 each standard store implementation runs as a **separate process**
(a "store-service") and rimsky talks to it over the 4-verb (plus
`Capabilities` handshake) gRPC protocol defined in
`proto/v1/store_service.proto`. Rimsky's per-process config
collapses to a thin "name → endpoint + declared capabilities" form;
store-service-specific configuration (DSNs, pick policies, items
tables, filesystem roots) lives in the store-service's own config and
rimsky never sees it (v3 spec §6.1, §6.3).

All three rimsky processes (`rimsky-scheduler`, `rimsky-supervisor`,
`rimsky-control-api`) load this same operator config bundle at startup.
The bundle has two top-level keys: `stores:` (one entry per configured
remote store-service) and `named_locks:` (one entry per declared named
lock). Templates reference stores by name and named locks by name;
resolution mirrors executor name resolution.

**Env var:** `RIMSKY_CONFIG` — path to the YAML file. Default
`/etc/rimsky/rimsky.yml`. The file must be readable by every rimsky
process that loads it; supervisor pool specialization is achieved by
giving each pool a different file. The canonical example is
`deploy/rimsky.yml` — read it alongside this section.

#### 3.4.1 Schema

```yaml
stores:
  <name>:
    endpoint: "grpc://<host>:<port>"        # required
    capabilities:                           # required
      write_semantics: direct | staged_blocking | staged_async

named_locks:
  <name>: { limit: <int> }                  # limit ≥ 1
```

The schema explicitly does **NOT** contain `kind`, `connection`,
`pick_policies`, or any other store-service-specific keys. Each store-service
runs at exactly one `write_semantics` (the value baked into its own
config); rimsky's job is to verify the operator's declared expectation
matches what the store-service advertises via the `Capabilities()`
startup-handshake RPC (v3 spec §4.8). Mismatch fails the rimsky process
at startup with a clear error naming the store, the declared
capabilities, and the actual capabilities.

`endpoint:` is the gRPC target (the `grpc://` scheme prefix is mandatory
— `http://` / `https://` / `tcp://` / `unix://` are rejected). Auth
between rimsky and the store-service is deployment-layer concern (mTLS,
service mesh, IAM); rimsky uses insecure credentials by default and is
auth-blind by design (see §3.4.4 below).

`named_locks:` is unchanged from v2. Each entry declares a `limit` (the
maximum simultaneous holders; `limit: 1` is conventionally a "mutex",
`limit: N>1` a counting semaphore). The supervisor's conflict predicate
is uniformly `count(holders) >= limit`. Templates reference named locks
by name only (`locks: [{name: <name>}]`); deploy-time validation rejects
template references to undeclared names.

**Reference example** (`deploy/rimsky.yml`):

```yaml
stores:
  content:
    endpoint: "grpc://store-filesystem:9100"
    capabilities:
      write_semantics: direct
  topics-ring:
    endpoint: "grpc://store-postgres:9101"
    capabilities:
      write_semantics: direct

named_locks:
  "topics-ring:concurrent-claims": { limit: 5 }
  model-budget:                    { limit: 50 }
```

Each store-service is a sibling process to the rimsky processes (running
as its own container in the docker-compose stack, its own Pod in
Kubernetes). The compose file at `deploy/docker-compose.yml` brings up
`store-filesystem` and `store-postgres` as services that the rimsky
processes dial via service-name DNS.

#### 3.4.2 Store-service configuration (store-internal)

Each store-service binary owns its own config schema and loads it from
its own env var. The schema is store-specific and out of rimsky's view.
For the standard stores shipped under `stores/`:

- **`stores/filesystem`** loads `STORE_FILESYSTEM_CONFIG` (or whatever
  env var the operator wires; the binary's `cmd/main.go` documents
  the default). Reference example at `deploy/store-filesystem.yml`:
  ```yaml
  root: /workspace/content
  host: 0.0.0.0
  grpc_port: 9100
  http_port: 9110
  ```
  `root:` must exist and be readable/writable by the store-service and
  by every executor that mounts it on the data path. In docker-compose
  this is a shared named volume; in Kubernetes it is a
  `PersistentVolumeClaim` mounted into both the store-service Pod and
  the executor Pods that read/write the files directly.

- **`stores/postgres`** loads `STORE_POSTGRES_CONFIG`. Reference
  example at `deploy/store-postgres.yml`:
  ```yaml
  connection: "postgres://app:pw@workload-pg:5432/workload?sslmode=require"
  write_semantics: direct
  pick_policies:
    "@review-queue":
      type: queue
      items_table: topics_items
      on_commit_default: release_to_back
      on_give_up_default: release_to_back
      visibility_timeout_seconds: 300
  host: 0.0.0.0
  grpc_port: 9101
  http_port: 9111
  admin_port: 9121
  sweep_interval_seconds: 30
  ```
  `connection:` is the store-service's own Postgres pool, opened by
  the store-service. Operators may collocate it with rimsky's
  control-plane database (same DSN as `RIMSKY_DB_URL`) or point at a
  separate database — the store-service is opaque to rimsky, so rimsky
  doesn't care. `pick_policies` is a store-defined block keyed by the
  store-recognised selector form (convention: `@policy-name`); each
  entry configures the items-table backing, default actions, and the
  visibility-timeout sweep period. The store-service's own admin
  endpoint at `:admin_port` is documented in §5.5 below.

Read each store-author's documentation for its supported config schema.
Rimsky neither defines nor validates these schemas.

#### 3.4.3 Pick-policy timing constraint (postgres reference store-service)

This subsection is guidance for operators of *the postgres reference
store-service* specifically, not a rimsky-level constraint. The
constraint exists because the postgres store implements an
items-table queue with a store-internal visibility timeout that
runs independently of rimsky's lock-holder bookkeeping. Other
store-services may have their own analogous timing constraints (or
none) — consult each store-service's own documentation.

When the postgres store-service is configured with a pick policy
that has a `visibility_timeout`, set that timeout **strictly greater
than `5 × heartbeat_interval`** (the rimsky-side orphan-reap window).
If `visibility_timeout` is shorter, the store's internal sweep
can strip a healthy claim out from under a live supervisor —
rimsky's heartbeat keeps the lock-holder row alive, but the
store's TTL doesn't observe the lock-holder row. The reference
postgres store-service ships `visibility_timeout_seconds: 300` and
`sweep_interval_seconds: 30` against the rimsky default
`heartbeat_interval_ms: 5000` (so `5 × 5s = 25s`); see
`stores/postgres/store/sweep.go` for the store-side sweep shape.

If you tune `heartbeat_interval_ms` upward in the supervisor config, you
must tune `visibility_timeout_seconds` upward in the store-service
config to match. Rimsky has no way to validate this constraint at
startup — the two configs live in two different processes.

#### 3.4.4 Auth-blind philosophy

Rimsky has **no protocol surface for credentials**. No verbs, fields, or
types in the executor or store interfaces mention auth. Substrate
connection details (DSNs in store-service configs, S3 bucket names with
embedded keys, etc.) often carry credentials; rimsky never sees them in
v3 because the store-service parses them internally. Service-to-service
auth between rimsky processes (operator → control-api, supervisor ↔
executor, supervisor ↔ store-service) is configured at the deployment
layer (mTLS, service mesh, IAM) — outside any YAML rimsky reads.

**Inertness boundary (v3 spec §13.3):** *store-config bytes* (the
`rimsky.yml` rimsky reads, plus each store-service's own config) are
operator-managed; what to log, redact, or audit is your call. Routine
startup logging like "loaded remote store `content` at
`grpc://store-filesystem:9100`" is fine. **Claim content** — the runtime
store-supplied `payload` / `address` / `region` bytes returned by
`Store.Open` over the wire — is governed by blessed invariant 20 and is
**never** under operator-side logging discretion. The boundary is
verb-based: anything rimsky receives over the 4-verb wire is claim
content (inert); anything rimsky reads from `RIMSKY_CONFIG` is
store config (operator's call).

If you need to pass sensitive bytes through claim content (payload,
address, or region), encrypt them before they enter the producer-side
store. Rimsky transports ciphertext as opaque bytes; the consuming
executor decrypts at point of use. Rimsky ships no helper library;
implementers handle the crypto end-to-end.

#### 3.4.5 Deploy-time validation surface

When a template is uploaded to control-api, the validator (using the
loaded operator config) checks:

- Every `stores[*].name` resolves to a configured remote store in the
  local `rimsky.yml`.
- Every `stores[*].intent` is `"r"` or `"rw"`.
- Every `stores[*].alias` (defaulting to the store name) is unique
  within the node.
- Every `locks[*].name` resolves to a declared `named_locks:` entry.
- Every `inherits[*].claim` resolves to an upstream claim alias the
  inheriting node depends on (transitively).
- Holding-subgraph computation succeeds for each held claim (acquirer +
  inheritors, all reachable via dependencies).
- `frame_resolution` is `coalesce` or `serial_queue`.

What deploy-time validation does **not** check:

- **Selector text against store grammar.** Selectors are opaque
  strings rimsky carries verbatim from template DSL (after `{{...}}`
  substitution at dispatch) through to the store-service. Rimsky does
  not parse, classify, or look up selectors against any store-side
  state. The authoritative validity check is the store's response
  to `Open` at dispatch (v3 spec §7.3).
- **Pick-policy intent.** Pick-policy classification is store-side; the
  control-api has no way to recognize a `@policy-name` selector as
  pick-policy because pick-policy registration lives in the
  store-service's own config. Operators are responsible for ensuring
  pick-policy claims declare `intent: rw` (the store will reject
  read-only `Open` against a pick-policy selector at dispatch).
- **Store connection details / store-service configs.** These live
  in the store-service binary's own config and rimsky has no view of
  them.

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
  Ingest source data, normalize, and commit attributes downstream.

frame_resolution: serial_queue      # required; coalesce | serial_queue (see frame-resolution spec)

params_schema:                      # JSON Schema for POST /instances {params}
  type: object
  properties:
    name:   { type: string }
    region: { type: string }
  required: [name, region]

params_redact:                      # field names to redact in GET responses
  - api_key

nodes:                              # the graph. order is irrelevant; dependencies drive ordering.
  - type: discover                  # node_type, unique within template
    executor: http-node             # name from supervisor-config.executors
    userdata:                       # opaque to rimsky; passed through to executor
      url: "https://example.com/{params.name}/layers"
      method: GET
    attributes:
      schema:
        layers:                     # executor-populated; written via Complete.attributes_delta
          type: array
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
    attributes:
      schema:
        layers:
          source: "{{deps.discover.layers}}"   # source-directive: pre-populated at dispatch
          type: array
        rows:                                  # executor-populated
          type: integer

  - type: nightly-refresh
    executor: http-node
    schedule: "0 2 * * *"           # cron expression; invalidates dependency chain at fire time
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
with their own `executor` re-run because their dep's attributes changed. This
is the idiomatic shape for periodic ingestion.

**Claim-based work assignment.** A node may declare a `stores:` entry with
a selector and intent — e.g. `{name: topics, selector: "@review-queue",
intent: rw, alias: queue}`. The supervisor calls `Store.Open` to acquire
the claim; the executor receives the store-supplied address opaquely and
the picked item's payload is available via `{{claim.queue.payload.<f>}}`
substitution paths. Store disposition at terminal — what `Commit` /
`Abandon` mean for the store's own state — is governed entirely by
the store's own configuration (e.g. the postgres reference store-
service exposes per-pick-policy `on_commit_default` / `on_give_up_default`
in its own `config.yml`). Templates carry no store-internal vocabulary.

**Held claims (multi-node access to the same picked item).** A downstream
node declares `inherits: [{claim: <alias>}]` to extend the claim's
lifetime to cover its own run. The supervisor's auto-terminal mechanism
(v3 spec §4.10 invariant 13, as amended by the 2026-04-30 stores cleanup)
fires exactly one store verb at holding-subgraph completion based
on aggregate outcome (all-success → `Commit`; any-failure → `Abandon`).
See `node-graph-design.md` for the full inheritance model.

**Error-policy chains.** Declare `error_types.<class>.policy` as an
ordered list of actions the scheduler walks on repeated failures.
Actions: `retry`, `invalidate(targets)`, `give_up`. Always end chains with
`give_up` so failures eventually reach a terminal state.

### 4.3 Validation

Before a template is accepted, the control API validates:

- `name`, `version` non-empty
- `frame_resolution` is `coalesce` or `serial_queue`
- every `executor` name is registered with the supervisor
- every `stores[*].name` resolves to a configured store kind in the local
  registry
- every `stores[*].intent` is `"r"` or `"rw"`; pick-policy claims (selector
  matches a configured `pick_policies` key on the store) require
  `intent: rw`
- every `stores[*].alias` is unique within the node
- every `locks[*].name` resolves to a declared `named_locks:` entry
- every `dependencies[]` entry resolves to another node in the same template
- every `inherits[*].claim` resolves to an upstream claim alias acquired
  by a node the inheritor depends on (transitively)
- holding-subgraph computation succeeds for each held claim (acquirer +
  inheritors all reachable via dependencies)
- placeholder strings: `{params.<key>}` (single brace, instantiation-time)
  references keys in `params_schema`; `{{...}}` (double brace,
  dispatch-time) references resolve to known dep / claim / params paths
- `attributes.schema` is a valid JSON Schema (draft-07)

What is **not** validated at deploy time: selector text against store
grammar (the store parses; resolved selector unknown until dispatch — v3
spec §7.3).

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
    "template": "'"$TPL"'",
    "instance_key": "alpha",
    "params": {"name": "alpha", "region": "r1"}
  }'
```

**List instances:**
```bash
curl -s 'http://localhost:8080/instances?template='"$TPL"
```

**Get an instance (by UUID or instance_key):**
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
| `attributes_committed` | typed attributes persisted at terminal commit |
| `claim_acquired` | `Store.Open` succeeded for a claim |
| `claim_resolved` | auto-terminal fired `Commit` / `Abandon` / `Delete` |
| `lock_orphan_reaped` | supervisor heartbeat sweep released a lock-holder row |
| `operator_override` | invalidate / reset |
| `heartbeat_timeout` | in-flight node exceeded heartbeat cutoff |

### 5.5 Admin endpoints

All routes under `/admin/...` are gated by the global authenticator wired
into the control-api process. Operators that want admin-only access wire an
`Authenticator` that checks the `X-Rimsky-Admin-Token` request header against
their configured token; processes started without an authenticator leave
these routes anonymous (consistent with the rest of the API in pre-v1).

**There is no rimsky-side admin endpoint for items insertion.** v3 removed
the `POST /admin/stores/{name}/pick-policies/{selector}/items` route
entirely (v3 spec §11.1). Items insertion is store-internal and out of
scope for the rimsky 4+1 protocol — each store-service that exposes
items-table queue semantics owns its own admin surface on a separate
listener port. Operators configure their item-seeding tooling to talk
to the store-service directly, never through rimsky.

**Insert items into a postgres store-service's pick-policy items table:**

The reference postgres store-service (under `stores/postgres/`) ships
with a documented admin endpoint at `POST /admin/items/{selector}` on
its configured `admin_port` (separate from the `grpc_port` and
`http_port` that carry the 4-verb store protocol). The reference
docker-compose stack maps this to host port `9121`:

```bash
curl -s -X POST 'http://localhost:9121/admin/items/@review-queue' \
  -H 'Content-Type: application/json' \
  -d '{
    "items": [
      {"payload": {"area": "A_1", "subtopic": "S_1"}},
      {"payload": {"area": "A_2", "subtopic": "S_2"}}
    ]
  }'
```

URL shape: `POST <admin_endpoint>/admin/items/<selector>`. The
`<selector>` is the store-recognized form configured under the
store-service's own `pick_policies:` block (see §3.4.2). Percent-encoded
selectors are accepted (`%40review-queue` decodes to `@review-queue`);
double-encoding is a footgun the store-service surfaces as a `400` with
a "no pick policy at selector" error.

Response: `201 Created` with `{"inserted": <n>}`. Bulk-inserts each
`items[*].payload` into the items table backing the named pick policy.
Authentication, TLS, and access control on this endpoint are operator-
deployment concern (mTLS, service mesh, IAM); the reference store-service
runs unauthenticated by default and is intended to live on an internal
network.

Custom store-services that support pick policies expose their own admin
surface (HTTP, gRPC, direct SQL — the store-author's choice). Read the
store-author guide and each store-service's own documentation for the
shape.

**Force-fire a scheduled node (set `next_fire_at = now()`):**

```bash
curl -s -X POST http://localhost:8080/admin/scheduled-nodes/$NODE/force-fire \
  -H 'X-Rimsky-Admin-Token: <token>'
```

Response: `204 No Content` the moment the row is updated. The handler does
**not** wait for the cascade — the next scheduler tick picks up the row and
fires the schedule. Callers that need to observe the fire (e.g. acceptance
tests) poll `rimsky_nodes.state` or the events table separately.

This is the same primitive the v3 §10 smoke fixture uses to drive the source
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
SELECT i.instance_key,
       count(*) FILTER (WHERE n.state='fresh')   AS fresh,
       count(*) FILTER (WHERE n.state='running') AS running,
       count(*) FILTER (WHERE n.state='stale')   AS stale,
       count(*) FILTER (WHERE n.state='failed')  AS failed
  FROM rimsky_instances i
  JOIN rimsky_nodes n ON n.instance_id = i.id
 GROUP BY i.instance_key
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

**Diagnosis:** the `path` points to the offending node or claim. Common
causes:

- `unknown executor "foo"` — name not in supervisor config
- `unknown store "foo"` — name not in `rimsky.yml`'s `stores:` block
- `unknown named lock "foo"` — name not in `rimsky.yml`'s `named_locks:`
  block
- `pick policy claim must be intent: rw` — the selector matches a
  configured `pick_policies` key on the store; pick-policy claims are
  inherently mutating
- `inherits.claim "X" not acquired by any dep` — `inherits:` reference
  doesn't resolve to a real upstream claim alias

### 7.5 Postgres pick-policy items table missing

**Symptom:** scheduler / supervisor / control-api fails to start with
`postgres store "X": pick policy "@Y": items_table "Z" missing or malformed`.

**Diagnosis:** the postgres store's factory verifies every items-table
referenced by a configured pick policy at `Build` time. Create the table
out-of-band per §3.4.3 before bringing up the processes; the compose stack
ships a `init-items` one-shot for the reference `topics_items` table.

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
stores-redesign-v2 rewrite changes `rimsky_lock_holders` (drops `claim_id`,
adds `address` and `intent`) and `rimsky_claim_holders` (adds
`lock_holder_id` FK with cascade, drops `actual_action` / `delete_won`) in
place against `core/migrations/001-initial.sql`. Because `rimsky_migrations`
already records `001-initial.sql` as applied on existing dev databases, the
migration runner will skip the rewritten file and the schema will be wrong.

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

1. Create any operator-owned items tables per §3.4.3 (the compose
   `init-items` service handles `topics_items` automatically).
2. Verify `RIMSKY_CONFIG` resolves to a readable `rimsky.yml` on
   scheduler / supervisor / control-api (default `/etc/rimsky/rimsky.yml`)
   — the bundle carries `stores:`, `named_locks:`, and `executors:`
   top-level blocks per §3.4.1.
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
for k in $(curl -s "http://localhost:8080/instances?template_hash=$TPL" | jq -r '.instances[].instance_key'); do
  curl -s -X DELETE "http://localhost:8080/instances/$k"
done
```

---

## Control-plane v1: `rimsky.yml` and template lifecycle

Per `docs/specs/2026-05-01-control-plane-and-store-lifecycle-design.md`.

### `RIMSKY_CONFIG`

Replaces the prior `RIMSKY_STORES_CONFIG`. All three rimsky processes (control-
api, supervisor, scheduler) load a single deployment-shape config from
`$RIMSKY_CONFIG` (default `/etc/rimsky/rimsky.yml`). The file contains three
top-level blocks:

```yaml
stores:
  <name>:
    endpoint: "grpc://<host>:<port>"
    capabilities:
      write_semantics: direct | staged_blocking | staged_async

named_locks:
  <name>: { limit: <int> }

executors:
  <name>:
    transport: grpc | http
    endpoint: "<host>:<port>"
    tls: off | optional | required
```

The supervisor still reads its per-process tuning (concurrency, callback,
heartbeat) from `$RIMSKY_SUPERVISOR_CONFIG`. The legacy `executors:` block
inside that file is gone.

### Four-state template lifecycle

Templates are content-addressed: `rimsky_templates.id` is
`sha256-<64-hex>` over an RFC 8785 JCS-canonicalized spec. Tags
(`rimsky_template_tags`) are movable aliases. The four states are
`registered → deployed → undeployed → deregistered (absent)`.

- `POST /v1/templates`           — register; body `{spec: <TemplateSpec>, tag?,
                                   source?}` or the bare `<TemplateSpec>` legacy
                                   shape. Returns the content hash.
- `GET /v1/templates`            — paginated list; filter by `?state=`.
- `GET /v1/templates/{tag_or_hash}` — read.
- `POST /v1/templates/{tag_or_hash}/deploy`     — `registered`/`undeployed` → `deployed`.
- `POST /v1/templates/{tag_or_hash}/undeploy`   — `deployed` → `undeployed`.
                                                 409 if active instances exist.
- `DELETE /v1/templates/{tag_or_hash}`          — deregister. Refused (409) if
                                                 the template is `deployed` or
                                                 has active instances.

### Tag operations

- `POST /v1/tags`     body `{tag, template: <tag_or_hash>}` — create.
- `GET /v1/tags`      paginated list.
- `PUT /v1/tags/{tag}` body `{template: <tag_or_hash>}` — move.
- `DELETE /v1/tags/{tag}` — drop the tag (does not delete the template).

Tag identifiers must match `^[a-zA-Z][a-zA-Z0-9._:@/-]{0,254}$`. Hash-shape
tags are rejected so `tag_or_hash` resolution stays unambiguous.

### Instance content-pinning

Instances bind to a template's content hash at creation time. Moving a tag to
a different hash does **not** migrate live instances: the instance keeps
running against the spec it was created with. Templates remain in the registry
as long as any instance references them (the schema enforces `ON DELETE
RESTRICT`).

The instances request body shape is now:

```json
{
  "template": "<tag_or_hash>",
  "instance_key": "optional",
  "params": {...}
}
```

The legacy `template_id` and `consumer_key` field names are no longer
accepted; bodies using those names are rejected at the control-api boundary.

### Instance terminal-state detection

When an instance's frame engine evaluates terminal (no queued/running frames,
no stale/running nodes), the scheduler sets
`rimsky_instances.terminated_at = now()`. A control-api background worker
polls for terminated instances with outstanding lifecycle bookkeeping and
fires `OnInstanceTerminated` to the relevant stores.
