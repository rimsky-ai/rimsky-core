# Rimsky Operator Guide

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
their names in `stores.yml` per §3.4.3 and create them out-of-band). Rimsky
never creates those tables.

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

### 3.3 `sql_connections` (deprecated)

The `external-sql` resource implementation is gone (replaced by the
`postgres` store kind, configured per §3.4). The `sql_connections:` block
is no longer consulted; remove it from supervisor configs on adoption.

### 3.4 Stores and named-locks configuration (`stores.yml`)

In addition to the supervisor config above, **all three runtime processes
(`rimsky-scheduler`, `rimsky-supervisor`, `rimsky-control-api`) load a shared
operator config bundle** at startup. The bundle has two top-level keys:
`stores:` (one entry per configured store) and `named_locks:` (one entry per
declared named lock — spec §15.2). Templates reference stores by name and
named locks by name; resolution mirrors executor name resolution.

**Env var:** `RIMSKY_STORES_CONFIG` — path to the YAML file. Default
`/etc/rimsky/stores.yml`. The file must be readable by every process that
loads it; supervisor pool specialization is achieved by giving each pool a
different file. The canonical example is `deploy/stores.yml` — read it
alongside this section.

#### 3.4.1 Schema

```yaml
stores:
  <name>:
    kind: filesystem | postgres
    write_semantics: direct | staged_blocking | staged_async   # optional override
    # kind-specific config follows. For kind: postgres, `connection:`
    # (a Postgres DSN) is REQUIRED — see §3.4.1.1 below. For kind:
    # filesystem, `root:` (a directory path) is REQUIRED.
    ...
    pick_policies:                                              # optional, substrate-defined
      "@<policy-name>": { ... }                                 # schema is per-substrate

named_locks:
  <name>: { limit: <int> }
```

`write_semantics` per store declares how writes coordinate with readers
(spec §8). `direct` and `staged_blocking` block reads against in-flight
writes on overlapping regions; `staged_async` does not (substrate provides
stable views via snapshot delegation or native MVCC). Operators may
**downgrade** the substrate's max capability (e.g. force `direct` on a
`staged_blocking`-capable kind) but not upgrade — config-load validation
rejects upgrades against the factory's `MaxWriteSemantics` ceiling with a
clear error.

`pick_policies` is a substrate-defined block: each entry is keyed by the
substrate-recognized selector form (recommended convention: `@policy-name`,
e.g. `@review-queue`, `@docs-ring`) and carries substrate-specific
configuration. Rimsky does not introspect the contents — read each store
implementation's documentation for its supported pick-policy types and
config keys.

`named_locks` is non-substrate state. Each entry declares a `limit` (the
maximum simultaneous holders; `limit: 1` is conventionally a "mutex",
`limit: N>1` a counting semaphore). There is no `mode` field — the
supervisor's conflict predicate is uniformly `count(holders) >= limit`.
Templates reference named locks by name only (`locks: [{name: <name>}]`);
deploy-time validation rejects template references to undeclared names.

**Filesystem direct-mode example:**

```yaml
stores:
  content:
    kind: filesystem
    write_semantics: direct
    root: /workspace/content
```

`root` must exist and be readable/writable by the supervisor and by every
executor that mounts it. In docker-compose this is a shared named volume; in
Kubernetes it is a `PersistentVolumeClaim` mounted into all participating
pods.

**Postgres store with one configured pick policy:**

```yaml
stores:
  topics:
    kind: postgres
    connection: "postgres://app:pw@workload-pg:5432/workload?sslmode=require"
    write_semantics: direct
    pick_policies:
      "@review-queue":
        type: queue
        items_table: topics_items
        on_commit_default: delete
        on_give_up_default: release_to_head
        visibility_timeout_seconds: 300
```

`connection:` is **required** on every `kind: postgres` store. The factory
opens a dedicated `*pgxpool.Pool` against this DSN; the store uses it for
its lock-holder, claim-holder, and items-table reads/writes. There is no
implicit fallback to "rimsky's control-plane pool" — the conceptual
distinction matters: the control plane (`RIMSKY_DB_URL`) is rimsky's own
state machine; a `kind: postgres` store is workload data your DAGs claim.
They may live in the same database for cheap deployments, but the
operator must say so explicitly.

`on_commit_default` and `on_give_up_default` are the substrate's defaults
for the configured pick policy; nodes can override per-claim via
`claim_resolutions:` in the template.
`visibility_timeout_seconds` is a backstop — rimsky's heartbeat-driven
release runs first (the items-table sweep's `NOT EXISTS` clause confirms);
set it to at least `2 × 5 × heartbeat_interval`.

A single postgres store may declare multiple pick policies side by side —
e.g. `@review-queue` (FIFO) alongside `@audit-ring` (ring buffer). All are
addressed via the same 5-verb interface (`Open` / `Commit` / `Abandon` /
`Delete` / `Release`); the substrate dispatches internally based on the
resolved selector.

**Collocating a workload store with the rimsky control DB:**

```yaml
stores:
  ledger:
    kind: postgres
    connection: "postgres://rimsky:rimsky@postgres:5432/rimsky?sslmode=disable"
    write_semantics: direct
    # … pick_policies, etc.
```

This is the cheap path for dev / single-cluster deployments — one Postgres
hosts both rimsky's bookkeeping tables (`rimsky_*`) and the operator-owned
items tables. The DSN is duplicated with `RIMSKY_DB_URL` by design: the
operator config bundle is one place to read what each store talks to,
without indirection through env-var fallbacks. Templating tools (helm,
kustomize, env substitution) handle the duplication out-of-band.

**Pointing a workload store at a separate database:**

Just supply the separate DSN as `connection:`. The factory opens a fresh
pool against that DSN; rimsky's control-plane pool is untouched. Works
across hosts, clusters, credentials — anything `pgx` can dial. Each
`kind: postgres` store gets one independent pool; pools are released at
process shutdown via `Registry.Close`.

#### 3.4.2 Per-region overrides not supported

`write_semantics` is a per-store property. v1 does not support per-region
overrides (spec §8.3). If you need different semantics for sub-regions of
the same underlying storage, the cleaner expression is **two distinct
stores pointing at the same underlying storage**, each with its own
`write_semantics`. Operators that try to express this with one store and
intra-store branching are encouraged to revisit the two-store pattern; it
is the substrate-honesty boundary the spec is built around.

#### 3.4.3 Operator-owned items tables for postgres pick policies

Pick-policy items tables are **operator-owned**: rimsky never creates,
migrates, or drops them. Create them out-of-band before any process loads
`stores.yml` — otherwise factory `Build` fails fast at startup with a
"table missing or malformed" error.

Required schema per spec §12.12 (one table per items-backed pick policy;
substitute your `<items_table>` name):

```sql
CREATE TABLE <items_table> (
    item_id     TEXT PRIMARY KEY,
    payload     JSONB NOT NULL,
    state       TEXT NOT NULL CHECK (state IN ('available', 'in_progress', 'completed')),
    claim_token TEXT,
    claimed_at  TIMESTAMPTZ,
    enqueued_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    priority    INTEGER NOT NULL DEFAULT 0,
    sequence    BIGSERIAL
);
CREATE INDEX idx_<items_table>_available
    ON <items_table>(priority DESC, sequence ASC)
    WHERE state = 'available';
```

Populate items via direct SQL or via the admin API endpoint
`POST /admin/stores/:name/pick-policies/:selector/items` (see §5.5).

The compose stack ships a one-shot `init-items` service that creates the
default `topics_items` table; the Helm chart does not yet run an equivalent
pre-install Job (known gap; see CHANGELOG).

#### 3.4.4 Auth-blind philosophy

Rimsky has **no protocol surface for credentials** (spec §17.1). No verbs,
fields, or types in the executor or store interfaces mention auth. Substrate
connection details (postgres URLs, S3 bucket names with embedded keys, etc.)
often carry credentials; Rimsky transports them as opaque substrate-specific
config — no introspection, no validation, no logging of credential shape.
Service-to-service auth between Rimsky processes (operator → control-api,
supervisor ↔ executor) is configured at the deployment layer (mTLS, IAM,
service mesh) — outside this YAML.

**Inertness boundary (spec §17.5):** store-config bytes (this YAML, loaded
at process start) are operator-managed and under your discretion to log,
redact, or audit. Routine startup logging like "loaded store `X` (kind:
postgres)" is fine. **Claim content** — the runtime substrate-supplied
`payload` / `address` / `region` bytes returned by `Store.Open` — is
governed by blessed invariant 20 and is **never** under operator-side
logging discretion. The boundary is verb-based: anything Rimsky receives
via the 5-verb interface is claim content (inert); anything Rimsky reads
from `RIMSKY_STORES_CONFIG` is store config (operator's call).

#### 3.4.5 Encrypt-before-pass (operator practice)

Sensitive fields inside claim content (any of payload / address / region)
should be encrypted at any producer-side boundary before they enter
Rimsky's address space (spec §17.6). Rimsky transports ciphertext as
opaque bytes; the consuming executor decrypts at point of use.

- Asymmetric is the recommended default (executor holds private key;
  producer holds public).
- Field-level, not whole-content — Rimsky needs to see structure to
  substitute by name; sensitive values are individually encrypted.
- Rimsky-side awareness: zero. The protocol is unaware of encryption.

This is a documented operator practice, not a Rimsky feature. Rimsky ships
no helper library; implementers handle the crypto end-to-end.

#### 3.4.6 Deploy-time validation surface

When a template is uploaded to control-api, the validator (using the
loaded operator config) checks:

- Every `stores[*].name` resolves to a configured store kind in the local
  registry.
- Every `stores[*].intent` is `"r"` or `"rw"`.
- Every `stores[*].alias` (defaulting to the store name) is unique within
  the node.
- Every `locks[*].name` resolves to a declared `named_locks:` entry.
- Every `inherits[*].claim` resolves to an upstream claim alias the
  inheriting node depends on (transitively).
- Holding-subgraph computation succeeds for each held claim (acquirer +
  inheritors, all reachable via deps).
- Every claim served by a configured pick policy (selector matches a
  `pick_policies` key on the store) declares `intent: rw` (spec §14.5 —
  pick-policy claims are inherently mutating).
- `frame_resolution` is `coalesce` or `serial_queue`.

What deploy-time validation does **not** check:

- **Selector text against substrate grammar.** Selectors may contain
  `{{...}}` substitution directives resolved at dispatch; their post-
  substitution shape is unknown until then. The authoritative validity
  check is the substrate's response to `Open` at dispatch (spec §7.5).
- Substrate-specific config inside `pick_policies` blocks (per-substrate
  factories validate at `Build`).
- Substrate connection-string contents.

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

nodes:                              # the graph. order is irrelevant; deps drive ordering.
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
    deps: [discover]                # runs after `discover` reaches fresh
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
    schedule: "0 2 * * *"           # cron expression; invalidates deps chain at fire time
    userdata:
      url: "http://refresh:8080/go"
    deps: [transform]
```

### 4.2 Common patterns

**Pure-cascade fan-out.** A node with no `executor` and no `schedule` is a
pure-cascade node: it contributes to dependency wiring but never dispatches.
Use it to fan out one producer into multiple reader chains without duplicating
execution. Declare it with `deps: [...]` only.

**Schedule-driven ingestion.** A top-of-graph node with a `schedule:` cron
invalidates itself (and cascades staleness down) at each fire time. Children
with their own `executor` re-run because their dep's attributes changed. This
is the idiomatic shape for periodic ingestion.

**Claim-based work assignment.** A node may declare a `stores:` entry with
a selector and intent — e.g. `{name: topics, selector: "@review-queue",
intent: rw, alias: queue}`. The supervisor calls `Store.Open` to acquire
the claim; the executor receives the substrate-native address opaquely and
the picked item's payload is available via `{{claim.queue.payload.<f>}}`
substitution paths. Declare `claim_resolutions:` on the acquiring node to
choose the substrate's terminal action (`delete`, `release_to_back`,
`release_to_head`, etc.).

**Held claims (multi-node access to the same picked item).** A downstream
node declares `inherits: [{claim: <alias>}]` to extend the claim's
lifetime to cover its own run. The supervisor's auto-terminal mechanism
(spec §14.4) fires exactly one resolution at holding-subgraph completion
based on aggregate outcome (all-success → `on_commit`; any-failure →
`on_give_up`). See `node-graph-design.md` for the full inheritance model.

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
- every `deps[]` entry resolves to another node in the same template
- every `inherits[*].claim` resolves to an upstream claim alias acquired
  by a node the inheritor depends on (transitively)
- holding-subgraph computation succeeds for each held claim (acquirer +
  inheritors all reachable via deps)
- placeholder strings: `{params.<key>}` (single brace, instantiation-time)
  references keys in `params_schema`; `{{...}}` (double brace,
  dispatch-time) references resolve to known dep / claim / params paths
- `attributes.schema` is a valid JSON Schema (draft-07)

What is **not** validated at deploy time: selector text against substrate
grammar (substrate parses; resolved selector unknown until dispatch — spec
§7.5).

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

**Insert items into a postgres-store pick-policy items table:**

```bash
curl -s -X POST 'http://localhost:8080/admin/stores/topics/pick-policies/@review-queue/items' \
  -H 'Content-Type: application/json' \
  -H 'X-Rimsky-Admin-Token: <token>' \
  -d '{
    "items": [
      {"payload": {"area": "A_1", "subtopic": "S_1"}},
      {"payload": {"area": "A_2", "subtopic": "S_2"}}
    ]
  }'
```

URL shape: `POST /admin/stores/<store-name>/pick-policies/<selector>/items`.
The `<selector>` is the substrate-recognized form configured under the
store's `pick_policies:` block.

**Selector encoding.** Both forms work and resolve to the same route:

```bash
# Raw '@' (what curl produces by default):
.../pick-policies/@review-queue/items

# Percent-encoded (what URL-builder libraries typically produce):
.../pick-policies/%40review-queue/items
```

The chi router decodes the path segment once before dispatch, so the
handler always sees the leading `@`. The one footgun is **double-
encoding** — `.../pick-policies/%2540review-queue/items` decodes to
the literal string `%40review-queue`, which won't match the selector
configured in `stores.yml`. This usually shows up when a client URL-
escapes a value that's already escaped (e.g. running the encoded form
through a second `urlencode` pass). The endpoint will return `400`
with a "no pick policy at selector" error when this happens.

Response: `201 Created` with `{"inserted": <n>}`. Bulk-inserts each
`items[*].payload` into the items table backing the named pick policy on
the named postgres store. Errors:

- `400` — empty `items` array, missing/invalid JSON in any payload, or the
  store is not a postgres store, or the named selector is not a configured
  pick policy on that store
- `404` — no store registered under that name in the loaded `stores.yml`
- `503` — control-api was started without a store registry (mis-wired)

Rimsky itself never enqueues into pick-policy items tables; this endpoint
exists for operators and external producers who prefer HTTP over direct SQL.

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

**Diagnosis:** the `path` points to the offending node or claim. Common
causes:

- `unknown executor "foo"` — name not in supervisor config
- `unknown store "foo"` — name not in `stores.yml`'s `stores:` block
- `unknown named lock "foo"` — name not in `stores.yml`'s `named_locks:`
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
2. Verify `RIMSKY_STORES_CONFIG` resolves to a readable `stores.yml` on
   scheduler / supervisor / control-api (default `/etc/rimsky/stores.yml`)
   — the bundle now carries both `stores:` and `named_locks:` top-level
   blocks per §3.4.1.
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
