# Rimsky Operator Guide

> v3 spec at `docs/history/2026-04-27-stores-redesign-v3-design.md` is the
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
3. `supervisor.volumes` — point at your own `supervisor-config.yml` (see §8).
4. `claude-agent.environment.RIMSKY_EXECUTOR_STUB_MODE` — set to `0` and
   provide `ANTHROPIC_API_KEY` (preferred) or `CLAUDE_CODE_OAUTH_TOKEN`
   (dev fallback) to enable real agent execution. The executor exits at
   startup if neither is set in non-stub mode. See
   `executors/claude-agent/README.md` for the precedence and env-stripping
   hygiene.
5. **Filesystem-store volume mounts.** If any template node sets
   `userdata.cwd_from_store: <fs-store-name>`, the claude-agent
   container must mount the same volume the filesystem store-service
   mounts, **at the same absolute path**. The address bytes the
   filesystem store returns from `Open` are a path on its own
   filesystem; the executor `chdir`s the spawned `claude` subprocess to
   that path verbatim. A mount-path mismatch surfaces as
   `invalid_cwd_from_store` errored outcomes on every dispatch.

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

Rimsky writes to exactly one schema: the migrations under
`core/persistence/<driver>/migrations/` create tables prefixed `rimsky_*`
(`rimsky_templates`, `rimsky_instances`, `rimsky_nodes`, `rimsky_dispatch`,
`rimsky_events`, `rimsky_schedules`, `rimsky_supervisors`,
`rimsky_node_attributes`, `rimsky_lock_holders`, `rimsky_claim_holders`,
`rimsky_frames`, `rimsky_migrations`). Nothing else in your database should
be named `rimsky_*`.

Postgres-store pick-policy **items tables** are caller-owned (you declare
their names in `rimsky.yml` per §8.4.3 and create them out-of-band). Rimsky
never creates those tables.

### 2.4 Persistence drivers

Rimsky supports two persistence drivers, configured via the `persistence:`
block in `rimsky.yml`:

- **postgres** — production-grade. Multi-replica, multi-host, real
  advisory locks, real `FOR UPDATE SKIP LOCKED`. Configured with a
  `dsn:` field (e.g., `postgres://user:pass@host:5432/db?sslmode=...`).
- **sqlite** — development-only. Single-process, single-writer, no
  cross-host coordination. The driver logs a loud startup banner —
  do not silence it. Multi-host or multi-replica deployments must use
  the postgres driver. Configured with a `path:` field (must be an
  absolute path).

Example:

```yaml
persistence:
  driver: postgres
  postgres:
    dsn: postgres://rimsky:rimsky@postgres:5432/rimsky?sslmode=disable
```

```yaml
persistence:
  driver: sqlite
  sqlite:
    path: /var/lib/rimsky/state.db
```

The `RIMSKY_DB_URL` environment variable is gone. All persistence config
lives under `persistence.postgres.dsn` (or `persistence.sqlite.path`) in
`RIMSKY_CONFIG`.

> **SQLite is dev-only.** It runs the runtime stack end-to-end, but the
> driver is single-process (cannot be replicated), single-writer
> concurrency (`SetMaxOpenConns(1)` plus `_txlock=immediate` per spec
> §6.2), and the cross-process advisory-lock methods on `Coordinator`
> are no-ops (the BEGIN IMMEDIATE writer slot subsumes them). Production
> / multi-replica deployments must declare `driver: postgres`.

### 2.5 Unified Docker image (`rimsky/all`)

For local development the `rimsky/all` image bundles the four runtime
binaries (`rimsky-scheduler`, `rimsky-supervisor`, `rimsky-control-api`,
`rimsky-migrate`) plus a small PID-1 process supervisor
(`rimsky-entrypoint`) under one container. The bundled `rimsky-all.yml`
declares `driver: sqlite` with state at `/var/lib/rimsky/state.db`.

> **Unified image limitations.** The unified image runs the full runtime
> stack end-to-end against the bundled SQLite default. It is dev-only:
> single-process (running with `replicas > 1` creates independent SQLite
> databases — broken); single-writer concurrency
> (`SetMaxOpenConns(1)` plus `_txlock=immediate` per spec §6.2); cannot
> be replicated. For production / multi-replica deployments, run the
> per-process images plus the postgres driver.

```sh
# Default: bundled SQLite.
docker run --rm -p 8080:8080 -v rimsky-state:/var/lib/rimsky rimsky/all

# Production: override the bundled config to point at Postgres.
docker run --rm -p 8080:8080 \
  -v ./my-rimsky.yml:/etc/rimsky/rimsky.yml:ro \
  rimsky/all
```

Override the bundled config to declare stores / executors as well:

```sh
docker run --rm -p 8080:8080 \
  -v ./my-rimsky.yml:/etc/rimsky/rimsky.yml:ro \
  rimsky/all
```

The unified image is **not** for production. Replicas > 1 each create
their own SQLite database — broken. Use the per-process images
(`rimsky/scheduler`, `rimsky/supervisor`, `rimsky/control-api`,
`rimsky/migrate`) plus the postgres driver for deployed instances.

---

## 3. Installing the CLI

Rimsky ships an operator-facing CLI, `rimsky-cli`. It is a thin client over
the control-api: every verb either maps to one endpoint or composes several
into a higher-level workflow (`compose up`, `run`, `init`).

Distribution channels (per spec §6.1):

- **GitHub Releases.** Per-platform tarballs at
  `https://github.com/.../releases` (linux/amd64, linux/arm64,
  darwin/amd64, darwin/arm64, windows/amd64). Source of truth for all
  channels.
- **Install script.** `curl -sSL https://rimsky.io/install.sh | sh`
  detects OS/arch, downloads the matching artifact, verifies SHA-256,
  installs to `/usr/local/bin/rimsky-cli`. (URL is a placeholder —
  publication is operator-responsibility outside the spec.)
- **Homebrew tap.** `brew install fallguy/rimsky/rimsky` once the tap is
  published.
- **`go install`.** `go install github.com/fallguy/rimsky/core/cmd/rimsky-cli@latest`
  works because the CLI is part of this module.
- **Docker image.** `rimsky/cli:<version>` and `rimsky/cli:latest`,
  distroless-based; for CI use.

`rimsky-cli version` prints the build-stamped version.

## 4. Using the CLI (dev loop)

Walkthrough on a fresh workstation:

```sh
mkdir myproject && cd myproject
rimsky-cli init .
rimsky-cli dev up                 # docker compose up + compose reconcile
rimsky-cli ls                     # list instances
rimsky-cli logs <instance-id>     # poll-stream events
rimsky-cli compose down --infra --yes
```

`rimsky-cli init [<dir>]` scaffolds a starter project: a
`rimsky-compose.yml` with the project name pre-filled, a
`deploy/docker-compose.yml` (copied from the rimsky module's reference at
the CLI version's build time), `deploy/store-filesystem.yml`,
`deploy/supervisor-config.yml`, `graphs/example.yml`, and an empty
`.rimsky/` (which `dev up` populates with the rendered `rimsky.yml`).
`.gitignore` is created or appended with `/.rimsky/`.

`rimsky-cli dev up` materializes inline `rimsky_config:` from the
manifest to `./.rimsky/rimsky.yml`, runs `infra.up.command`, polls
`infra.up.wait_for` until 2xx (default timeout 60s), then runs the same
plan-and-apply that `compose up` would.

## 5. Compose manifests

`rimsky-compose.yml` is application-layer: it describes templates,
tags, and persistent instances — what should exist inside an
already-running rimsky deployment. Compose owns project-prefixed names
(`compose:<project>:<...>`) and reconciles only against those.

Example:

```yaml
project: ingest-pipeline
context: dev

infra:
  up:
    command: ["docker", "compose", "-f", "deploy/docker-compose.yml", "up", "-d"]
    wait_for: "http://localhost:8080/health"
    timeout: 60s
  down:
    command: ["docker", "compose", "-f", "deploy/docker-compose.yml", "down", "-v"]

rimsky_config:
  inline:
    stores:
      content:
        endpoint: "grpc://store-filesystem:9100"
        capabilities: { write_semantics: direct }
    named_locks:
      model-budget: { limit: 50 }
    executors:
      claude-agent:
        transport: grpc
        endpoint: "claude-agent:9090"
        tls: off

templates:
  - path: ./graphs/ingest.yml
    tag: ingest@1.0
    state: deployed

instances:
  - template: ingest@1.0
    name: daily-ingest
    params:
      window: "24h"
    restart: on_failure
```

`compose up` is apply-once-and-exit: parse manifest → query control-api
state for resources prefixed with `compose:<project>:` → diff → execute
serially in dependency order → exit. Steps are sequenced as registers
→ tag creates/moves → deploys → instance deletes → undeploys → tag
deletes → instance creates → best-effort template deletes.

`compose plan` prints the diff and exits. Exit codes: `0` (no drift),
`3` (drift detected), `1` (control-api error), `2` (manifest validation).
Mirrors `terraform plan -detailed-exitcode`.

`compose down` reverses the application-state side of the manifest.
Adding `--infra` runs `infra.down.command` last (e.g. `docker compose
down -v`). Compose refuses to abort running instances — wait for
terminal state.

Restart policies (`never` (default), `on_failure`, `always`) apply on
the next `compose up` after an instance reaches terminal state. The
control-api has no in-flight kill: terminal-then-recreate is the only
self-healing path.

## 6. Contexts

The CLI's endpoint is resolved by precedence (highest first):

For non-compose verbs (`template`, `instance`, `tag`, `node`, `health`,
`run`, `register`, `deploy`, `logs`, etc.):

1. `--endpoint <url>` flag.
2. `RIMSKY_CONTROL_API` environment variable.
3. The current context's endpoint from `~/.rimsky/config.yml`
   (`rimsky-cli ctx use <name>` sets it; `RIMSKY_CONTEXT` env var
   overrides for one invocation).

For compose verbs (`compose up/down/plan/status`, `dev up/down/status`),
the manifest's `context:` field, when set, pins the deployment and
overrides flag and env. This is intentional: the manifest pin protects
against cross-environment misfires (e.g. running `compose up` against
prod when your shell is configured for dev). The remaining tiers
fall through in the same order as above when the manifest does not
declare a `context:`.

`~/.rimsky/config.yml`:

```yaml
current_context: dev

contexts:
  dev:
    endpoint: http://localhost:8080
  staging:
    endpoint: https://rimsky.staging.example.com
```

Verbs: `ctx list`, `ctx use <name>`, `ctx add <name> --endpoint <url>`,
`ctx rm <name>`, `ctx current`. The file is per-user (not per-project)
and is created on first `ctx add` if missing.

## 7. Cloud deployment workflows

Use your own IaC (Terraform, Helm, ECS, Pulumi, …) to deploy rimsky to
a managed environment, then run the CLI from an operator workstation
or CI runner with a context pointing at the deployed control-api:

```sh
rimsky-cli ctx add prod --endpoint https://rimsky.prod.example.com
rimsky-cli ctx use prod
rimsky-cli compose up -f rimsky-compose.yml --yes
```

Cloud manifests typically omit `infra:` (the operator's IaC is what
brings rimsky up; compose only manages templates / tags / instances) or
have `infra.up.command` invoke `terraform apply` / `kubectl apply` /
`helm upgrade --install`. `dev up` is a convenience for local
development; production reconciliation runs `compose up` directly.

---

## 8. Supervisor configuration

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
  blocks are documented in §8.4.

### 8.1 Supervisor-tuning fields

| field | purpose |
| --- | --- |
| `postgres_url` | DSN for the rimsky Postgres database (required) |
| `supervisor_id` | unique ID across running supervisors; defaults to `<hostname>-<pid>` |
| `concurrency` | hard cap on in-flight dispatch claims per supervisor |
| `heartbeat_interval_ms` | how often the supervisor updates `rimsky_supervisors.last_heartbeat_at` |
| `claim_poll_interval_ms` | how often the supervisor polls for new claims |
| `callback.host` / `callback.port` | bind address for the HTTP+JSON callback endpoint async executors POST to |
| `callback.advertise_host` / `callback.advertise_port` | peer-reachable host:port embedded in the `callback_url` handed to executors (override via `RIMSKY_SUPERVISOR_CALLBACK_ADVERTISE_{HOST,PORT}`) |

### 8.2 Executors in `rimsky.yml`

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

### 8.4 Stores and named-locks configuration (`rimsky.yml`)

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

#### 8.4.1 Schema

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
auth-blind by design (see §8.4.4 below).

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

#### 8.4.2 Store-service configuration (store-internal)

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
  control-plane database (same DSN as `persistence.postgres.dsn`) or
  point at a separate database — the store-service is opaque to rimsky,
  so rimsky doesn't care. `pick_policies` is a store-defined block keyed by the
  store-recognised selector form (convention: `@policy-name`); each
  entry configures the items-table backing, default actions, and the
  visibility-timeout sweep period. The store-service's own admin
  endpoint at `:admin_port` is documented in §10.5 below.

- **`stores/filesystem` with pick policies** loads the same `STORE_FILESYSTEM_CONFIG`
  and grows a `pick_policies:` block when configured for queue/ring workloads:
  ```yaml
  root: /workspace
  pick_policies:
    "@docs-ring":
      root: documents
      folder_pattern: "^[a-z][a-z0-9-]*$"
      on_commit_default: release_to_back
      on_give_up_default: release_to_back
      visibility_timeout_seconds: 1800
      sync_strategy: on_open
  host: 0.0.0.0
  grpc_port: 9100
  http_port: 9110
  admin_port: 9120
  sweep_interval_seconds: 60
  ```
  Folders under `<root>/<pick_policies.<sel>.root>/` are auto-discovered as
  queue items. Adding/removing a folder is `mkdir`/`rm -rf` under the sub-root;
  the next `Open` (or sweep tick under `sync_strategy: on_sweep`) reconciles.
  Actions: `release_to_back` cycles to the tail; `release_to_head` (mtime
  epoch) sorts strictly to the head — note this is *stronger* than pg's
  priority-bump `release_to_head`; `delete` runs `os.RemoveAll` on the
  underlying folder. Per `docs/history/2026-05-03-fs-store-pick-policies-design.md`.

Read each store-author's documentation for its supported config schema.
Rimsky neither defines nor validates these schemas.

#### 8.4.3 Pick-policy timing constraint (postgres reference store-service)

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

#### 8.4.4 Auth-blind philosophy

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

#### 8.4.5 Deploy-time validation surface

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

## 9. Template authoring

A template is a YAML (or JSON) document describing the node graph. It is
submitted once via `POST /templates`; each `POST /instances` against that
template creates a fresh copy of the graph with concrete IDs and
consumer-specific params resolved into placeholders.

### 9.1 Schema walkthrough

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

### 9.2 Common patterns

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

### 9.3 Validation

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

## 10. Control API operations

All endpoints accept and return `application/json`. Paginated reads support
`?limit=N&cursor=<opaque>`.

### 10.1 Templates

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

### 10.2 Instances

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

### 10.3 Operator overrides

Overrides are emergency levers. Every override emits an `operator_override`
event so audits can reconstruct who moved the state machine.

**Invalidate a node** (mark stale, re-dispatch):
```bash
curl -s -X POST http://localhost:8080/nodes/$NODE/invalidate \
  -H 'Content-Type: application/json' \
  -d '{"reason": "upstream data corrected"}'
```

Under frame resolution (see `docs/history/2026-04-26-frame-resolution-design.md`)
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

### 10.3.1 Frame resolution and templates

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

### 10.4 Event log

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

### 10.5 Admin endpoints

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
store-service's own `pick_policies:` block (see §8.4.2). Percent-encoded
selectors are accepted (`%40review-queue` decodes to `@review-queue`);
double-encoding is a footgun the store-service surfaces as a `400` with
a "no pick policy at selector" error.

Response: `201 Created` with `{"inserted": <n>}`. Bulk-inserts each
`items[*].payload` into the items table backing the named pick policy.
Authentication, TLS, and access control on this endpoint are operator-
deployment concern (mTLS, service mesh, IAM); the reference store-service
runs unauthenticated by default and is intended to live on an internal
network.

**Bump a folder to the head of the queue (filesystem reference store-service):**

The reference filesystem store-service (under `stores/filesystem/`) ships
a single admin endpoint at `POST /admin/bump-to-head/{selector}` on its
configured `admin_port` (separate from the `grpc_port` and `http_port`
that carry the 4-verb store protocol). Example config in §8.4.2 binds
`admin_port: 9120`; expose that port in your deployment topology
alongside the grpc/http ports:

```bash
curl -s -X POST 'http://store-filesystem:9120/admin/bump-to-head/%40docs-ring' \
  -H 'Content-Type: application/json' \
  -d '{"folder":"area-a"}'
```

URL shape: `POST <admin_endpoint>/admin/bump-to-head/<selector>`. The
`<selector>` is the store-recognised form configured under the
store-service's `pick_policies:` block (see §8.4.2). Percent-encoded
selectors are accepted (`%40docs-ring` decodes to `@docs-ring`).

Body: `{"folder":"<folder-name>"}`. The folder name must be a single
path component — embedded `/` or `\`, leading `.`, and `..` segments
are rejected with `400`.

Responses:

- `204 No Content` — sentinel mtime set to epoch; folder will sort to
  the head of the queue on the next `Open`.
- `400 Bad Request` — unknown selector, malformed body, folder name
  rejected (separators / leading-dot / pattern mismatch / not a single
  component).
- `404 Not Found` — folder does not exist on disk under the policy
  root, or the folder exists but is not currently enqueued (sync may
  not have observed it yet — operators can wait for the next sweep).
- `409 Conflict` — folder is currently held in `in_progress` by a live
  claim; bumping it would race the visibility timeout. Wait for the
  claim to complete (or reach the visibility cutoff) and retry.
- `500 Internal Server Error` — unexpected I/O failure.

Authentication, TLS, and access control on this endpoint are operator-
deployment concerns (mTLS, service mesh, IAM); the reference store-service
runs unauthenticated by default and is intended to live on an internal
network (mirrors the postgres store-service stance above).

Operator constraint — store root must not contain symlinks pointing
outside itself: the filesystem store-service follows symlinks at
runtime (per `os.Stat` / `os.Rename` semantics), so a symlink in the
store root that escapes it would let runtime operations escape too.
The lexical containment check at startup catches direct `..` escapes
in the YAML config but not symlink-mediated ones. Per the spec's
"POSIX local filesystems" assumption this is an operator-fault concern,
not enforced by the store-service. Mount the policy root from a
trusted source and avoid placing operator-controlled symlinks under it.

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

## 11. Monitoring

### 11.1 Health

Every long-running process exposes `/health`:

- `control-api`: `GET :8080/health` → supervisor list + node-state counts
- `scheduler`: `GET :9097/health` (Prometheus port doubles as liveness)
- `supervisor`: liveness probe via `/health` on the metrics port
- Executors: depend on image; `http-node` and `claude-agent` each expose
  `/health` on their metrics port

A healthy cluster has at least one supervisor row with
`last_heartbeat_at` within 2× `heartbeat_interval_ms` of now.

### 11.2 Postgres queries

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

## 12. Common failure modes

### 12.1 Node stuck `running`

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

### 12.2 Supervisor unreachable

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

### 12.3 Orphaned claims

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

### 12.4 Template validation rejects a field that looks right

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

### 12.5 Postgres pick-policy items table missing

**Symptom:** scheduler / supervisor / control-api fails to start with
`postgres store "X": pick policy "@Y": items_table "Z" missing or malformed`.

**Diagnosis:** the postgres store's factory verifies every items-table
referenced by a configured pick policy at `Build` time. Create the table
out-of-band per §8.4.3 before bringing up the processes; the compose stack
ships a `init-items` one-shot for the reference `topics_items` table.

### 12.6 `claude-agent` appears to hang on every dispatch

**Symptom:** nodes using `claude-agent` sit `running` for a long time.

**Diagnosis:** check `RIMSKY_EXECUTOR_STUB_MODE`. In stub mode (`"1"`),
claude-agent short-circuits and always returns instantly. If stub mode is
off and you see hangs, verify `ANTHROPIC_API_KEY` (preferred) or
`CLAUDE_CODE_OAUTH_TOKEN` (dev fallback) is set — the executor exits at
startup if neither is present in non-stub mode — and check the executor's
metrics endpoint for rate-limit errors.

---

## 13. Upgrade procedure

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

### 13.1 Pre-v1 schema reset

Rimsky is pre-v1: there is no production data preservation guarantee.
When a refactor changes the schema in place (rewriting the existing
migration files rather than appending a new one), `rimsky_migrations`
will record the file as already applied and the migration runner will
skip the rewrite, leaving the schema wrong.

**Required step: nuke the dev database before running the new code.**
This applies only to dev / staging environments — there is no
production-data migration path, by design.

Compose:

```bash
docker compose -f deploy/docker-compose.yml down -v   # -v drops the postgres volume
docker compose -f deploy/docker-compose.yml up -d     # fresh DB; migrate runs cleanly
```

Kubernetes / external Postgres: drop and recreate the rimsky database (or
drop the `rimsky_*` tables plus `rimsky_migrations`) before bringing up
the new images. The migration runner then re-applies the embedded SQL
(`core/persistence/<driver>/migrations/`) as the new end-state schema.

After bringing the stack up:

1. Create any operator-owned items tables per §8.4.3 (the compose
   `init-items` service handles `topics_items` automatically).
2. Verify `RIMSKY_CONFIG` resolves to a readable `rimsky.yml` on
   scheduler / supervisor / control-api (default `/etc/rimsky/rimsky.yml`)
   — the bundle carries `stores:`, `named_locks:`, and `executors:`
   top-level blocks per §8.4.1.
3. `curl -s http://localhost:8080/health` to confirm at least one supervisor
   has registered.

## 14. Appendix — useful one-liners

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

Per `docs/history/2026-05-01-control-plane-and-store-lifecycle-design.md`.

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


## Observability & dashboard

Rimsky exposes three optional public observability protocols, one per
existing collection. Per `docs/specs/2026-05-02-dashboard-and-observability-design.md`:

- **Rimsky observability API** — read-only HTTP/JSON on
  `rimsky-control-api`, mounted under `/v1/observability/*`. Backed by
  the `rimsky_*` tables; no new schema. Resource-oriented browse +
  detail endpoints for templates, instances, frames, nodes,
  dispatches, lock-holders, schedules, events, plus per-peer topology
  (declared executors and stores with their observability
  capabilities).
- **Executor observability protocol** — gRPC service + HTTP+JSON
  bridge per executor (`proto/v1/executor_observability.proto`).
  `GetCapabilities`, `GetTrace(dispatch_id)`, `StreamTrace`. Optional;
  executors that don't implement it return `Unimplemented` for the
  RPCs and false `supports_*` flags from `GetCapabilities`.
- **Store observability protocol** — gRPC service + HTTP+JSON bridge
  per store (`proto/v1/store_observability.proto`). `GetCapabilities`,
  `GetClaim`, `StreamClaim`, `ListClaims`, `GetAdminView`. Optional in
  the same way.

### `observability_endpoint:` in `rimsky.yml`

Each `executors:` and `stores:` block in `rimsky.yml` accepts an
optional `observability_endpoint:` field. When omitted, control-api
uses the dispatch `endpoint` for the observability handshake. Override
when the observability surface lives on a different port or host than
the dispatch surface (e.g., when a sidecar serves the observability
protocol on its own listener).

```yaml
executors:
  claude-agent:
    transport: grpc
    endpoint: claude-agent:9090
    observability_endpoint: claude-agent:9091   # optional
    tls: off

stores:
  topics-ring:
    endpoint: grpc://store-postgres:9101
    observability_endpoint: grpc://store-postgres:9103
    capabilities:
      write_semantics: direct
```

Per spec §4 the observability handshake is best-effort: unreachable
peers or absent endpoints are recorded as `reachability_status:
unreachable`; control-api startup is not aborted. A background
re-prober (`RIMSKY_OBSERVABILITY_REFRESH_INTERVAL`, default `60s`)
re-probes peers so transient unreachability heals.

### `http_bridge_url:` per peer

Separate from `observability_endpoint:` (which is the gRPC handshake
target read by control-api at startup), each peer declares its own
HTTP+JSON bridge URL via `http_bridge_url`. The bridge URL is
returned in the peer's capabilities response and exposed on
`/v1/observability/{stores,executors}/{name}` as `http_bridge_url`;
the dashboard's reverse proxy uses it to dial the peer from the
browser. When a peer's gRPC and HTTP-bridge ports differ (the case
for every shipped executor and store), set this explicitly:

- **Stores** declare it in their own YAML (`http_bridge_url:` at the
  top level of `deploy/store-{filesystem,postgres}.yml`).
- **Executors** declare it via env var
  (`RIMSKY_EXECUTOR_HTTP_NODE_HTTP_BRIDGE_URL` for `http-node`,
  `RIMSKY_EXECUTOR_OBSERVABILITY_HTTP_BRIDGE_URL` for `claude-agent`).

When empty, the peer exposes only the gRPC observability surface and
the dashboard cannot proxy to it from the browser; the SystemPage
will show the peer as reachable (gRPC handshake succeeded) but
deep-links to its detail pages will surface a "no HTTP bridge"
notice.

### Reference dashboard

`dashboards/rimsky-dashboard/` ships as a single Node + SPA process.
Bundled with the dev `docker-compose.yml` and started by default:

```sh
docker compose -f deploy/docker-compose.yml up -d
# open http://localhost:8090
```

The dashboard reads `RIMSKY_CONTROL_API_URL` (default
`http://control-api:8080`) and `PORT` (default `8090`). It listens on
a single port and exposes `/healthz` for compose / k8s liveness. The
SPA composes the three observability protocols via reverse-proxy
endpoints (`/api/control/*`, `/api/exec/{name}/*`,
`/api/store/{name}/*`); CORS is collapsed to the dashboard's own
origin so operators don't have to configure CORS on every executor or
store.

### Auth posture

V1 inherits the per-project deployment / network-perimeter model. The
observability API is unauthenticated; the dashboard is unauthenticated.
Run them behind the same perimeter as control-api. Tenant scoping and
front-end auth are forward-scope (spec §11).

### Iframe security

The dashboard renders peer-declared custom UIs in sandboxed iframes
(`sandbox="allow-scripts allow-forms"`, `referrerpolicy="no-referrer"`,
optional `allow-same-origin` only when the iframe origin matches the
dashboard's own). Operators are responsible for the executors and
stores they declare in `rimsky.yml`; the iframe sandbox is
defense-in-depth, not the primary trust boundary. See spec §5.6.
