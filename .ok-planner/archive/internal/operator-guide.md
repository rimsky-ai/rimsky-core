# Rimsky Operator Guide

This guide is written for the person who runs Rimsky in production: deploying the processes, writing the YAML config, authoring templates, creating and operating instances, scraping metrics, and diagnosing failures.

The three contracts in `docs/specs/` are the authoritative sources for the underlying responsibilities:

- `docs/specs/2026-05-04-foundation-contract.md` — what the foundation owns.
- `docs/specs/2026-05-04-modeling-layer-contract.md` — what the modeling layer owns.
- `docs/specs/2026-05-04-service-protocol-contract.md` — the three peer-service protocols (`ClaimProducer`, `Executor`, `LifecycleSubscriber`).

If you are authoring an executor (new language, new integration), see `executor-author-guide.md`. If you are authoring a Go claim-producer, see `claim-producer-author-guide.md`. For the concept model, see `node-graph-design.md`. The authoritative vocabulary (claim, named lock, scope, selector, address, payload, intent, alias, write_semantics, pick policy, etc.) lives in `glossary.md`.

A note on terminology: the protocol-level term for a service that produces claim handles is **claim producer**. The colloquial term **store** survives at the bundled-services layer for the data-backed reference impls (filesystem store, postgres store, stub store). Use whichever term is clearer in context; this guide uses both.

---

## 1. First 15 minutes — quickstart

Brings up a complete Rimsky stack, deploys a template, creates an instance, and inspects the event log. Requires only Docker and `curl`.

### 1.1 Bring up the stack

```bash
cd rimsky/deploy
docker compose up -d
```

The stack includes:

| service | port | purpose |
| --- | --- | --- |
| `postgres` | 5544 | Rimsky's state database |
| `migrate` | — | One-shot; applies SQL migrations then exits |
| `scheduler` | — | Pure-cascade + schedule tick loop |
| `supervisor` | — | Dispatch loop; owns claim handles |
| `control-api` | 8080 | HTTP+JSON operator surface |
| `store-filesystem` | 9100/9110 | Reference filesystem claim-producer |
| `store-postgres` | 9101/9111 | Reference postgres claim-producer |
| `http-node` | 9091/9092 | Reference HTTP executor (gRPC + HTTP bridge) |
| `claude-agent` | 9090/9190 | Agentic executor (stub mode by default) |

Verify everything is up:

```bash
curl -s http://localhost:8080/health | jq .
```

Expected: `{"status":"ok","supervisors":[{...}],"node_counts":{...}}`. If `supervisors` is empty, wait 10 seconds — the supervisor writes its first heartbeat row on its first tick.

### 1.2 Deploy your first template

```bash
cat > /tmp/hello.yaml <<'YAML'
name: hello-world
version: "1.0.0"
description: One node that pings httpbin.
frame_resolution: serial_queue
nodes:
  - type: ping
    executor: http-node
    userdata:
      url: "https://httpbin.org/get"
      method: GET
YAML
```

Templates are submitted as JSON to the control API:

```bash
python3 -c 'import yaml, json, sys; print(json.dumps(yaml.safe_load(open("/tmp/hello.yaml"))))' \
  | curl -s -X POST http://localhost:8080/templates \
       -H 'Content-Type: application/json' -d @- | jq .
```

Expected response: `{"template": "sha256-<64-hex>", ...}`.

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

Within a few seconds the node transitions `stale -> running -> fresh`. Tail the event log:

```bash
curl -s "http://localhost:8080/events?instance_id=$INST" | jq '.events[].kind'
```

You should see `node_state_change`, `executor_complete`, and `attributes_committed` kinds.

### 1.5 Tear down

```bash
docker compose down -v
```

---

## 2. Deployment

### 2.1 Docker Compose (development, staging)

`deploy/docker-compose.yml` is the canonical reference. Copy it into your environment and customize:

1. `postgres.image` — pin to your preferred Postgres tag; rimsky supports 13+.
2. `postgres.volumes` — replace the named volume with a bind-mount or managed block device for durability.
3. `claude-agent.environment.RIMSKY_EXECUTOR_STUB_MODE` — set to `0` and provide `ANTHROPIC_API_KEY` (preferred) or `CLAUDE_CODE_OAUTH_TOKEN` (dev fallback) to enable real agent execution. The executor exits at startup if neither is set in non-stub mode. See `executors/claude-agent/README.md` for the precedence and env-stripping hygiene.
4. **Filesystem-store volume mounts.** If any template node sets `userdata.cwd_from_store: <fs-store-name>`, the claude-agent container must mount the same volume the filesystem store mounts, **at the same absolute path**. The address bytes the filesystem store returns from `Open` are a path on its own filesystem; the executor `chdir`s the spawned `claude` subprocess to that path verbatim. A mount-path mismatch surfaces as `invalid_cwd_from_store` errored outcomes on every dispatch.

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

`deploy/kubernetes/rimsky-chart/` is the canonical Helm chart. **Known stale**: env-var names lag behind the binaries; verify against the current binaries before deploying.

Chart values of interest (see `values.yaml` for the full list):

| key | default | notes |
| --- | --- | --- |
| `postgres.dsn` | *(required)* | DSN must include the database the migrations run into |
| `scheduler.replicas` | 1 | Cooperative leader election; running >1 is safe but unnecessary |
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

Rimsky writes to exactly one schema: the migrations under `foundation/persistence/<driver>/migrations/` create tables prefixed `rimsky_*`:

- Foundation-owned: `rimsky_worker_request`, `rimsky_claim_handle`, `rimsky_claim_holders`, `rimsky_nodes` (split-owned), `rimsky_node_attributes`, `rimsky_supervisors`, `rimsky_migrations`.
- Modeling-owned: `rimsky_templates`, `rimsky_template_tags`, `rimsky_instances`, `rimsky_schedules`, `rimsky_frames`, `rimsky_events`, `rimsky_lifecycle_idempotency`.

Nothing else in your database should be named `rimsky_*`.

Postgres-store pick-policy **items tables** are caller-owned (you declare their names in the postgres claim-producer's own YAML; you create them out-of-band). Rimsky never creates those tables.

### 2.4 Persistence drivers

Rimsky supports two persistence drivers, configured via the `persistence:` block in `rimsky.yml`:

- **postgres** — production-grade. Multi-replica, multi-host, real advisory locks, real `FOR UPDATE SKIP LOCKED`.
- **sqlite** — development-only. Single-process, single-writer, no cross-host coordination. The driver logs a loud startup banner — do not silence it.

Examples:

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

The legacy `RIMSKY_DB_URL` environment variable is gone. All persistence config lives under `persistence.postgres.dsn` (or `persistence.sqlite.path`) in `RIMSKY_CONFIG`.

> **SQLite is dev-only.** Multi-process / multi-host SQLite is not supported (single-writer concurrency, no cross-process advisory locks). Production / multi-replica deployments must declare `driver: postgres`.

### 2.5 Unified Docker image (`rimsky/all`)

For local development the `rimsky/all` image bundles the four runtime binaries (`rimsky-scheduler`, `rimsky-supervisor`, `rimsky-control-api`, `rimsky-migrate`) plus a small PID-1 process supervisor (`rimsky-entrypoint`) under one container. The bundled `rimsky-all.yml` declares `driver: sqlite` with state at `/var/lib/rimsky/state.db`.

> **Unified image limitations.** Dev-only. Single-process; running with `replicas > 1` creates independent SQLite databases — broken. Cannot be replicated.

```sh
# Default: bundled SQLite.
docker run --rm -p 8080:8080 -v rimsky-state:/var/lib/rimsky rimsky/all

# Override the bundled config (e.g., point at Postgres).
docker run --rm -p 8080:8080 \
  -v ./my-rimsky.yml:/etc/rimsky/rimsky.yml:ro \
  rimsky/all
```

For production / multi-replica deployments use the per-process images (`rimsky/scheduler`, `rimsky/supervisor`, `rimsky/control-api`, `rimsky/migrate`) plus the postgres driver.

---

## 3. Installing the CLI

Rimsky ships an operator-facing CLI, `rimsky-cli`. It is a thin client over the control-api: every verb either maps to one endpoint or composes several into a higher-level workflow (`compose up`, `run`, `init`).

Distribution channels:

- **GitHub Releases.** Per-platform tarballs (linux/amd64, linux/arm64, darwin/amd64, darwin/arm64, windows/amd64).
- **Install script.** `curl -sSL https://rimsky.io/install.sh | sh` (URL placeholder; publication is operator concern).
- **Homebrew tap.** `brew install fallguy/rimsky/rimsky` once the tap is published.
- **`go install`.** `go install github.com/fallguyconsulting/rimsky/cmd/rimsky-cli@latest`.
- **Docker image.** `rimsky/cli:<version>` and `rimsky/cli:latest`, distroless-based; for CI use.

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

`rimsky-cli init [<dir>]` scaffolds a starter project: a `rimsky-compose.yml` with the project name pre-filled, a `deploy/docker-compose.yml`, `deploy/store-filesystem.yml`, `graphs/example.yml`, and an empty `.rimsky/` (which `dev up` populates with the rendered `rimsky.yml`).

`rimsky-cli dev up` materializes inline `rimsky_config:` from the manifest to `./.rimsky/rimsky.yml`, runs `infra.up.command`, polls `infra.up.wait_for` until 2xx (default timeout 60s), then runs the same plan-and-apply that `compose up` would.

## 5. Compose manifests

`rimsky-compose.yml` is application-layer: it describes templates, tags, and persistent instances — what should exist inside an already-running Rimsky deployment. Compose owns project-prefixed names (`compose:<project>:<...>`) and reconciles only against those.

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
    claim_producers:
      - name: content
        endpoint: store-filesystem:9100
        write_semantics_envelope: [sync]
    named_locks:
      - name: model-budget
        mode: counting
        capacity: 50
    executors:
      - name: claude-agent
        endpoint: claude-agent:9090

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

`compose up` is apply-once-and-exit: parse manifest → query control-api for resources prefixed with `compose:<project>:` → diff → execute serially in dependency order → exit. Steps: registers → tag creates/moves → deploys → instance deletes → undeploys → tag deletes → instance creates → best-effort template deletes.

`compose plan` prints the diff and exits. Exit codes: `0` (no drift), `3` (drift detected), `1` (control-api error), `2` (manifest validation). Mirrors `terraform plan -detailed-exitcode`.

`compose down` reverses the application-state side of the manifest. Adding `--infra` runs `infra.down.command` last. Compose refuses to abort running instances — wait for terminal state.

Restart policies (`never` (default), `on_failure`, `always`) apply on the next `compose up` after an instance reaches terminal state. The control-api has no in-flight kill: terminal-then-recreate is the only self-healing path.

## 6. Contexts

The CLI's endpoint is resolved by precedence (highest first):

For non-compose verbs (`template`, `instance`, `tag`, `node`, `health`, `run`, `register`, `deploy`, `logs`, etc.):

1. `--endpoint <url>` flag.
2. `RIMSKY_CONTROL_API` environment variable.
3. The current context's endpoint from `~/.rimsky/config.yml` (`rimsky-cli ctx use <name>` sets it; `RIMSKY_CONTEXT` env var overrides for one invocation).

For compose verbs, the manifest's `context:` field, when set, pins the deployment and overrides flag and env. This is intentional: the manifest pin protects against cross-environment misfires.

`~/.rimsky/config.yml`:

```yaml
current_context: dev

contexts:
  dev:
    endpoint: http://localhost:8080
  staging:
    endpoint: https://rimsky.staging.example.com
```

Verbs: `ctx list`, `ctx use <name>`, `ctx add <name> --endpoint <url>`, `ctx rm <name>`, `ctx current`. The file is per-user (not per-project) and is created on first `ctx add` if missing.

## 7. Cloud deployment workflows

Use your own IaC (Terraform, Helm, ECS, Pulumi) to deploy Rimsky to a managed environment, then run the CLI from an operator workstation or CI runner with a context pointing at the deployed control-api:

```sh
rimsky-cli ctx add prod --endpoint https://rimsky.prod.example.com
rimsky-cli ctx use prod
rimsky-cli compose up -f rimsky-compose.yml --yes
```

Cloud manifests typically omit `infra:` (the operator's IaC is what brings rimsky up; compose only manages templates / tags / instances) or have `infra.up.command` invoke `terraform apply` / `kubectl apply` / `helm upgrade --install`.

---

## 8. Unified `rimsky.yml` configuration

All four Rimsky binaries (`rimsky-scheduler`, `rimsky-supervisor`, `rimsky-control-api`, `rimsky-migrate`) load a single deployment-shape config from `RIMSKY_CONFIG` (default `/etc/rimsky/rimsky.yml`). The file has four top-level blocks: `persistence:`, `claim_producers:`, `executors:`, `named_locks:`.

**Modeling contract reference:** the YAML schema is governed by `docs/specs/2026-05-04-modeling-layer-contract.md` §10.

### 8.1 Schema

```yaml
persistence:
  driver: postgres                            # or 'sqlite' (dev-only)
  postgres:
    dsn: "postgres://rimsky:rimsky@postgres:5432/rimsky?sslmode=disable"
  sqlite:
    path: /var/lib/rimsky/state.db

claim_producers:
  - name: <peer-name>
    endpoint: "<host>:<port>"                 # gRPC target
    protocols: [claim_producer, lifecycle_subscriber]   # default: [claim_producer]
    write_semantics_envelope: [sync]          # required; ⊆ producer-declared at handshake
    observability_endpoint: "<host>:<port>"   # optional
    http_bridge_url: "http://<host>:<port>"   # optional

executors:
  - name: <peer-name>
    endpoint: "<host>:<port>"                 # gRPC target, or full URL for HTTP+JSON
    transport: grpc                           # or 'http+json'; default 'grpc'
    protocols: [executor]                     # default: [executor]
    tls: off                                  # off | optional | required

named_locks:
  - name: <lock-name>
    mode: mutex                               # mutex | counting
    capacity: 1                               # required for counting
```

### 8.2 Capability handshake

At startup, every Rimsky process dials each peer endpoint and runs the `Capabilities()` handshake **per declared protocol** in the peer's `protocols:` list. Validations:

- Operator-declared `write_semantics_envelope` MUST be a subset of the producer-declared envelope returned by `ClaimProducer.Capabilities()`.
- For each peer, `Capabilities()` must succeed for every protocol named in `protocols:`.
- Peers referenced by templates but not declared in YAML cause template registration to fail with `unknown claim producer` / `unknown executor`.
- Failures fail-fast at process startup with a clear error naming the peer, the declared properties, and the actual properties.

The handshake is one-shot at startup; capabilities are cached for the process's lifetime. Capability changes require restart.

### 8.3 LifecycleSubscriber as a per-peer protocol

LifecycleSubscriber is an opt-in protocol on the same peer binaries. Declare it in the `protocols:` list of whichever block the peer primarily belongs to (typically `claim_producers:` for stores that bootstrap schema on `OnTemplateDeployed`):

```yaml
claim_producers:
  - name: items-pg
    endpoint: store-postgres:9101
    protocols: [claim_producer, lifecycle_subscriber]
    write_semantics_envelope: [staged_async]
```

Bundled producer binaries can ship a no-op LifecycleSubscriber via their own `enable_lifecycle: true` config without forking the binary. Peers referenced by a template but not subscribed silently skip lifecycle fan-out (no idempotency row inserted, no error returned).

There is no separate `lifecycle_subscribers:` block.

### 8.4 Reference example

`deploy/rimsky.yml`:

```yaml
persistence:
  driver: postgres
  postgres:
    dsn: "postgres://rimsky:rimsky@postgres:5432/rimsky?sslmode=disable"

claim_producers:
  - name: content
    endpoint: store-filesystem:9100
    write_semantics_envelope: [sync]
  - name: topics-ring
    endpoint: store-postgres:9101
    protocols: [claim_producer, lifecycle_subscriber]
    write_semantics_envelope: [staged_async]

executors:
  - name: http-node
    endpoint: http-node:9091
  - name: claude-agent
    endpoint: claude-agent:9090

named_locks:
  - name: topics-ring-concurrent-claims
    mode: counting
    capacity: 5
  - name: model-budget
    mode: counting
    capacity: 50
```

`endpoint:` values are gRPC targets unless the executor's `transport: http+json` is set, in which case the value is a full URL.

`named_locks:` mode is `mutex` (capacity 1) or `counting` (declared capacity). Templates reference named locks by name only; deploy-time validation rejects references to undeclared names.

### 8.5 Per-process tuning (supervisor)

The supervisor reads tuning fields from `RIMSKY_SUPERVISOR_CONFIG` (separate from `RIMSKY_CONFIG`):

```yaml
supervisor_id: "supervisor-1"               # optional; defaults to <hostname>-<pid>
concurrency: 8
heartbeat_interval_ms: 5000
claim_poll_interval_ms: 1000
callback:
  host: "0.0.0.0"                           # bind host for the callback HTTP listener
  port: 9100
  advertise_host: "supervisor"              # peer-reachable hostname executors dial back
  advertise_port: 9100
```

| field | purpose |
| --- | --- |
| `supervisor_id` | unique ID across running supervisors |
| `concurrency` | hard cap on in-flight worker requests per supervisor |
| `heartbeat_interval_ms` | how often the supervisor refreshes ownership timestamps |
| `claim_poll_interval_ms` | how often the supervisor polls for new worker requests |
| `callback.host` / `callback.port` | bind for the HTTP+JSON callback endpoint async executors POST to |
| `callback.advertise_host` / `callback.advertise_port` | peer-reachable host:port embedded in the `callback_url` handed to executors (override via `RIMSKY_SUPERVISOR_CALLBACK_ADVERTISE_{HOST,PORT}`) |

### 8.6 Claim-producer-internal configuration

Each claim-producer binary owns its own config schema. The bundled producers under `stores/` document their schemas in their own README; `rimsky.yml` does NOT see them. Examples follow.

#### `stores/filesystem` (`deploy/store-filesystem.yml`)

```yaml
root: /workspace/content
host: 0.0.0.0
grpc_port: 9100
http_port: 9110
```

`root:` must exist and be readable/writable by the producer and by every executor that mounts it on the data path. In docker-compose this is a shared named volume; in Kubernetes a `PersistentVolumeClaim` mounted into both Pods.

With pick policies (queue/ring workloads):

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

Folders under `<root>/<pick_policies.<sel>.root>/` are auto-discovered as queue items. `mkdir`/`rm -rf` is the insertion/removal mechanism.

#### `stores/postgres` (`deploy/store-postgres.yml`)

```yaml
connection: "postgres://app:pw@workload-pg:5432/workload?sslmode=require"
write_semantics: staged_async
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

`connection:` is the producer's own Postgres pool, opened by the producer. Operators may collocate it with Rimsky's control-plane database or point at a separate database. `pick_policies` is a producer-defined block keyed by the producer-recognised selector form (convention: `@policy-name`).

### 8.7 Pick-policy timing constraint (postgres reference producer)

When the postgres producer is configured with a pick policy that has a `visibility_timeout_seconds`, set that timeout **strictly greater than `5 × heartbeat_interval`** (the rimsky-side orphan-reap window, per blessed invariant 6). If the timeout is shorter, the producer's internal sweep can strip a healthy claim out from under a live supervisor.

The reference postgres producer ships `visibility_timeout_seconds: 300` against the Rimsky default `heartbeat_interval_ms: 5000` (so `5 × 5s = 25s`).

If you tune `heartbeat_interval_ms` upward in supervisor config, you must tune `visibility_timeout_seconds` upward in the producer config to match. Rimsky has no way to validate this at startup — the two configs live in two different processes.

### 8.8 Auth-blind philosophy

Rimsky has **no protocol surface for credentials**. No verbs, fields, or types in the Executor or ClaimProducer interfaces mention auth. Connection details (DSNs in producer configs, S3 bucket names with embedded keys, etc.) often carry credentials; Rimsky never sees them — the producer parses them internally. Service-to-service auth between Rimsky processes (operator → control-api, supervisor ↔ executor, supervisor ↔ producer) is configured at the deployment layer (mTLS, service mesh, IAM) — outside any YAML Rimsky reads.

**Inertness boundary:** *config bytes* (the `rimsky.yml` Rimsky reads, plus each producer's own config) are operator-managed; what to log, redact, or audit is your call. **Claim content** — the runtime producer-supplied `payload` / `address` / `scope` bytes returned by `ClaimProducer.Open` over the wire — is governed by blessed invariant 20 and is **never** under operator-side logging discretion. The boundary is verb-based: anything Rimsky receives over the four-verb wire is claim content (inert); anything Rimsky reads from `RIMSKY_CONFIG` is config (operator's call).

If you need to pass sensitive bytes through claim content, encrypt them before they enter the producer. Rimsky transports ciphertext as opaque bytes; the consuming executor decrypts at point of use. Rimsky ships no helper library; implementers handle the crypto end-to-end.

### 8.9 Deploy-time validation surface

When a template is uploaded to control-api, the validator (using the loaded operator config) checks:

- Every claim reference resolves to a configured `claim_producers:` entry.
- Every `intent` is `"r"` or `"rw"`.
- Every claim alias is unique within the node.
- Every `locks[*].name` resolves to a declared `named_locks:` entry.
- Every `inherits[*].claim` resolves to an upstream claim alias the inheriting node depends on (transitively).
- Holding-subgraph computation succeeds for each held claim.
- `frame_resolution` is `coalesce` or `serial_queue`.

What deploy-time validation does **not** check:

- **Selector text against producer grammar.** Selectors are opaque strings Rimsky carries verbatim from template DSL through to the producer. The authoritative validity check is the producer's response to `Open` at dispatch.
- **Pick-policy intent.** Pick-policy classification is producer-side; the control-api has no way to recognize a `@policy-name` selector as pick-policy because pick-policy registration lives in the producer's own config.
- **Producer connection details / producer configs.** Rimsky has no view of these.

---

## 9. Template authoring

A template is a YAML (or JSON) document describing the node graph. Submitted once via `POST /templates`; each `POST /instances` against that template creates a fresh instance bound to the template's content hash.

### 9.1 Schema walkthrough

```yaml
name: ingest-source
version: "1.2.0"
description: |
  Ingest source data, normalize, and commit attributes downstream.

frame_resolution: serial_queue      # required; coalesce | serial_queue

params_schema:                      # JSON Schema for POST /instances {params}
  type: object
  properties:
    name:   { type: string }
    region: { type: string }
  required: [name, region]

params_redact:
  - api_key

nodes:
  - type: discover
    executor: http-node
    userdata:
      url: "https://example.com/{params.name}/layers"
      method: GET
    attributes:
      schema:
        layers:
          type: array
    error_types:
      http_unexpected_status:
        policy:
          - { action: retry, count: 2, backoff: exponential, base_delay_ms: 30000 }
          - { action: give_up }

  - type: transform
    executor: http-node
    dependencies: [discover]
    userdata:
      url: "http://transformer:8080/run"
      method: POST
    attributes:
      schema:
        layers:
          source: "{{deps.discover.layers}}"
          type: array
        rows:
          type: integer

  - type: nightly-refresh
    executor: http-node
    schedule: "0 2 * * *"
    userdata:
      url: "http://refresh:8080/go"
    dependencies: [transform]
```

### 9.2 Common patterns

**Pure-cascade fan-out.** A node with no `executor` and no `schedule` is a pure-cascade node: it contributes to dependency wiring but never dispatches. Use it to fan out one producer into multiple reader chains without duplicating execution.

**Schedule-driven ingestion.** A top-of-graph node with a `schedule:` cron invalidates itself (and cascades staleness down) at each fire time. Children with their own `executor` re-run because their dep's attributes changed.

**Claim-based work assignment.** A node may declare a claim — e.g. `{name: topics, selector: "@review-queue", intent: rw, alias: queue}`. The supervisor calls `ClaimProducer.Open` to acquire the claim; the executor receives the producer-supplied address opaquely and the picked item's payload is available via `{{claim.queue.payload.<f>}}` substitution paths. Producer disposition at terminal — what `Commit` / `Abandon` mean for the producer's own state — is governed entirely by the producer's own configuration. Templates carry no producer-internal vocabulary.

**Held claims (multi-node access to the same picked item).** A downstream node declares `inherits: [{claim: <alias>}]` to extend the claim's lifetime to cover its own run. The supervisor's auto-terminal mechanism (blessed invariant 13) fires exactly one producer verb at holding-subgraph completion based on aggregate outcome (all-success → `Commit`; any-failure → `Abandon`). See `node-graph-design.md` for the full inheritance model.

**Error-policy chains.** Declare `error_types.<class>.policy` as an ordered list of actions the scheduler walks on repeated failures. Actions: `retry`, `invalidate(targets)`, `give_up`. Always end chains with `give_up` so failures eventually reach a terminal state.

### 9.3 Validation

Before a template is accepted, the control API validates everything in §8.9 plus:

- `name`, `version` non-empty.
- `frame_resolution` is `coalesce` or `serial_queue`.
- Every `executor` name resolves to a declared `executors:` entry.
- Every `dependencies[]` entry resolves to another node in the same template.
- Placeholder strings: `{params.<key>}` (single brace, instantiation-time) references keys in `params_schema`; `{{...}}` (double brace, dispatch-time) references resolve to known dep / claim / params paths.
- `attributes.schema` is a valid JSON Schema (draft-07).

Validation errors come back as HTTP 400 with a `validation_errors` array.

---

## 10. Control API operations

All endpoints accept and return `application/json`. Paginated reads support `?limit=N&cursor=<opaque>`.

### 10.1 Templates

```bash
# Register a template:
curl -s -X POST http://localhost:8080/templates \
  -H 'Content-Type: application/json' -d @template.json

# List templates:
curl -s 'http://localhost:8080/templates?name=ingest-source&limit=20'

# Get a template (full spec):
curl -s "http://localhost:8080/templates/$TPL"

# Deploy / undeploy:
curl -s -X POST "http://localhost:8080/templates/$TPL/deploy"
curl -s -X POST "http://localhost:8080/templates/$TPL/undeploy"

# Deregister (only if unreferenced):
curl -s -X DELETE "http://localhost:8080/templates/$TPL"
```

Templates are content-addressed: `rimsky_templates.id` is `sha256-<64-hex>` over an RFC 8785 JCS-canonicalized spec. Tags (`rimsky_template_tags`) are movable aliases. The four states are `registered → deployed → undeployed → deregistered (absent)`.

### 10.2 Tags

```bash
# Create:
curl -s -X POST http://localhost:8080/tags -d '{"tag":"ingest@1.0","template":"<hash>"}'

# Move:
curl -s -X PUT "http://localhost:8080/tags/ingest@1.0" -d '{"template":"<hash>"}'

# Delete:
curl -s -X DELETE "http://localhost:8080/tags/ingest@1.0"
```

Tag identifiers must match `^[a-zA-Z][a-zA-Z0-9._:@/-]{0,254}$`. Hash-shape tags are rejected so `tag_or_hash` resolution stays unambiguous.

### 10.3 Instances

```bash
# Create:
curl -s -X POST http://localhost:8080/instances \
  -H 'Content-Type: application/json' \
  -d '{
    "template": "'"$TPL"'",
    "instance_key": "alpha",
    "params": {"name": "alpha", "region": "r1"}
  }'

# List:
curl -s 'http://localhost:8080/instances?template='"$TPL"

# Get (by UUID or instance_key):
curl -s "http://localhost:8080/instances/alpha"

# List nodes:
curl -s "http://localhost:8080/instances/alpha/nodes" | jq '.nodes[] | {node_type, state}'

# Terminate:
curl -s -X POST "http://localhost:8080/instances/alpha/terminate"
```

Instances bind to a template's content hash at creation time. Moving a tag to a different hash does NOT migrate live instances. The body shape is `{template, instance_key?, params}`; legacy `template_id` / `consumer_key` field names are no longer accepted.

### 10.4 Operator overrides

Overrides are emergency levers. Every override emits an `operator_override` event so audits can reconstruct who moved the state machine.

```bash
# Invalidate a node (mark stale, re-dispatch through frame engine):
curl -s -X POST http://localhost:8080/nodes/$NODE/invalidate \
  -d '{"reason": "upstream data corrected"}'

# Reset a failed node (only legal from state=failed):
curl -s -X POST http://localhost:8080/nodes/$NODE/reset
```

Operator invalidates do **not** preempt running work. Each invalidate goes through `frame.EnqueueOrCoalesce`: `serial_queue` templates queue a new frame that runs after the in-flight one completes; `coalesce` templates fold the invalidate into the pending coalesce row. There is no kill mechanism — the `kill_requested` column was removed.

### 10.5 Frame state

Inspect frame state per instance:

```sql
SELECT id, state, resolution, created_at, ended_at
  FROM rimsky_frames WHERE instance_id = '<instance-uuid>'
  ORDER BY created_at;
```

At most one frame is in `running` state per instance at any time (enforced by `uq_rimsky_frames_running`). Frames that exceed `frame_timeout_ms` are reaped.

### 10.6 Event log

Append-only and the authoritative audit trail. Filter by any combination of `instance_id`, `node_id`, `kind`, `since`, `until` (RFC3339).

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
| `claim_acquired` | `ClaimProducer.Open` succeeded for a claim |
| `claim_resolved` | auto-terminal fired `Commit` / `Abandon` |
| `claim_handle_orphan_reaped` | stale claim_handle row reaped |
| `worker_request_orphan_released` | active-phase worker_request released by orphan reaper |
| `operator_override` | invalidate / reset |
| `heartbeat_timeout` | in-flight node exceeded heartbeat cutoff |

### 10.7 Admin endpoints

All routes under `/admin/...` require admin auth (gated by an `Authenticator` checking `X-Rimsky-Admin-Token` against the configured token; processes started without an authenticator leave these routes anonymous in pre-v1).

**There is no rimsky-side admin endpoint for items insertion.** Items insertion is producer-internal — each producer that exposes items-table queue semantics owns its own admin surface on a separate listener port.

**Insert items into the postgres reference producer's pick-policy items table:**

The postgres reference producer ships `POST /admin/items/{selector}` on its `admin_port`:

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

Response: `201 Created` with `{"inserted": <n>}`.

**Bump a folder to the head of the queue (filesystem reference producer):**

The filesystem reference producer ships `POST /admin/bump-to-head/{selector}` on its `admin_port`:

```bash
curl -s -X POST 'http://store-filesystem:9120/admin/bump-to-head/%40docs-ring' \
  -H 'Content-Type: application/json' \
  -d '{"folder":"area-a"}'
```

Body: `{"folder":"<folder-name>"}`. The folder name must be a single path component.

Responses: `204` (sentinel mtime set to epoch); `400` (unknown selector / malformed); `404` (folder not found / not enqueued); `409` (folder currently held in_progress); `500` (I/O failure).

Operator constraint: store root must not contain symlinks pointing outside itself — the producer follows symlinks at runtime.

Authentication, TLS, and access control on these producer admin endpoints are deployment-layer concerns (mTLS, service mesh, IAM); the reference producers run unauthenticated by default and are intended to live on an internal network.

**Force-fire a scheduled node (set `next_fire_at = now()`):**

```bash
curl -s -X POST http://localhost:8080/admin/scheduled-nodes/$NODE/force-fire \
  -H 'X-Rimsky-Admin-Token: <token>'
```

Response: `204 No Content` immediately. The handler does NOT wait for the cascade — the next scheduler tick picks up the row and fires the schedule. This is the same primitive the smoke fixture uses to drive the source node 100 times in a row.

In production, prefer `POST /nodes/:id/invalidate` for one-off re-runs (it participates in the operator-override audit log); use `force-fire` only when you specifically need the schedule to advance via the regular scheduler tick path.

---

## 11. Monitoring

### 11.1 Health

Every long-running process exposes `/health`:

- `control-api`: `GET :8080/health` → supervisor list + node-state counts.
- `scheduler`: `GET :9097/health`.
- `supervisor`: `/health` on the metrics port.
- Executors and claim-producers: each peer's `/health` on its configured metrics port.

A healthy cluster has at least one `rimsky_supervisors` row with `last_heartbeat_at` within `2 × heartbeat_interval_ms` of now.

### 11.2 Postgres queries

When the API is unavailable or diagnosis needs raw data, these queries cut straight to Postgres. Note: schema names are post-Phase-5 (`rimsky_worker_request`, `rimsky_claim_handle`).

**Currently running nodes:**

```sql
SELECT id, node_type, instance_id, last_heartbeat_at, now() - last_heartbeat_at AS age
  FROM rimsky_nodes
 WHERE has_outstanding_request = true AND has_value = false
 ORDER BY last_heartbeat_at ASC;
```

**Stuck active worker-requests (claimed > 2 minutes, no heartbeat):**

```sql
SELECT wr.node_id, wr.claimed_by, wr.claimed_at, wr.phase,
       n.has_outstanding_request, n.last_heartbeat_at
  FROM rimsky_worker_request wr
  JOIN rimsky_nodes n ON n.id = wr.node_id
 WHERE wr.phase = 'active' AND wr.claimed_by IS NOT NULL
   AND wr.claimed_at < now() - interval '2 minutes'
   AND (n.last_heartbeat_at IS NULL
        OR n.last_heartbeat_at < now() - interval '2 minutes');
```

**Held claim handles (outliving their parent's active terminal):**

```sql
SELECT id, scope_data, holder_supervisor_id, expires_at, is_held, worker_request_id
  FROM rimsky_claim_handle
 WHERE is_held = true
 ORDER BY expires_at ASC;
```

**Recent errors:**

```sql
SELECT e.occurred_at, e.kind, e.node_id, e.payload
  FROM rimsky_events e
 WHERE e.kind IN ('executor_errored', 'executor_blocked', 'heartbeat_timeout')
   AND e.occurred_at > now() - interval '1 hour'
 ORDER BY e.occurred_at DESC LIMIT 100;
```

**Supervisors and last heartbeats:**

```sql
SELECT id, accepted_executors, active_node_count, last_heartbeat_at,
       now() - last_heartbeat_at AS since_heartbeat
  FROM rimsky_supervisors
 ORDER BY last_heartbeat_at DESC;
```

---

## 12. Common failure modes

### 12.1 Node stuck `running`

**Symptom:** node is in `running` (foundation: `has_outstanding_request=true, has_value=false`) but `last_heartbeat_at` is older than a few minutes; event log shows no terminal event.

**Diagnosis:**
1. Is the assigned supervisor still alive? Check `rimsky_supervisors.last_heartbeat_at`.
2. Is the executor process alive? Check executor logs.
3. Did the executor emit an async-accepted? `GET /events?node_id=<id>` — look for `executor_async_accepted`. If yes, the work is outside Rimsky and Rimsky is waiting for the callback.

**Remediation:** if the supervisor is dead, the heartbeat-loss sweep will release the orphan claim within `5 × heartbeat_interval` (blessed invariant 6). Watch for the `worker_request_orphan_released` event. If the sweep does not fire (e.g., scheduler is also down), restart the scheduler process.

If the sweep runs but the node immediately re-claims and re-stalls, the root cause is in the executor — stop dispatch and diagnose the executor directly.

### 12.2 Supervisor unreachable

**Symptom:** `GET /health` lists no supervisors, or the supervisor row is stale.

```sql
SELECT id, last_heartbeat_at, now() - last_heartbeat_at AS age
  FROM rimsky_supervisors ORDER BY last_heartbeat_at DESC LIMIT 5;
```

**Remediation:** restart the supervisor; orphan sweep releases stale active-phase worker-requests. No operator action is required to recover in-flight work beyond bringing a supervisor back up. Held claim handles are preserved until auto-terminal explicitly resolves them.

### 12.3 Orphaned claims

**Symptom:** event log shows `worker_request_orphan_released` and `claim_handle_orphan_reaped` entries; nodes that had been running are back in `stale`.

**Diagnosis:** normal recovery behavior. Expected after a supervisor crash, OOM, or scale-down. Payload carries the prior `claimed_by` and claim age.

**Remediation:** none. The scheduler re-enqueues the affected worker-requests; another supervisor picks them up.

If you see *continuous* orphan-released events against the same supervisor, that supervisor is failing its heartbeat writes — check Postgres connectivity.

### 12.4 Template validation rejects a field that looks right

**Symptom:** `POST /templates` returns 400 with `validation_errors: [{path, msg}]`.

**Diagnosis:** the `path` points to the offending node or claim. Common causes:

- `unknown executor "foo"` — name not in `executors:` block of `rimsky.yml`.
- `unknown claim producer "foo"` — name not in `claim_producers:` block.
- `unknown named lock "foo"` — name not in `named_locks:` block.
- `pick policy claim must be intent: rw` — the selector matches a configured `pick_policies` key on the producer; pick-policy claims are inherently mutating.
- `inherits.claim "X" not acquired by any dep` — `inherits:` reference doesn't resolve to a real upstream claim alias.

### 12.5 Capability handshake mismatch at startup

**Symptom:** scheduler / supervisor / control-api fails to start with `claim_producer "X": operator-declared write_semantics_envelope [staged_async] not subset of producer-declared envelope [sync]`.

**Diagnosis:** the operator's declared `write_semantics_envelope` in `rimsky.yml` exceeds what the producer actually advertises via `Capabilities()`.

**Remediation:** correct the operator-declared envelope to a subset of what the producer advertises, or update the producer's config to widen its envelope.

### 12.6 `claude-agent` appears to hang on every dispatch

**Symptom:** nodes using `claude-agent` sit `running` for a long time.

**Diagnosis:** check `RIMSKY_EXECUTOR_STUB_MODE`. In stub mode (`"1"`), claude-agent short-circuits and always returns instantly. If stub mode is off, verify `ANTHROPIC_API_KEY` (preferred) or `CLAUDE_CODE_OAUTH_TOKEN` (dev fallback) is set, and check the executor's metrics endpoint for rate-limit errors.

---

## 13. Upgrade procedure

1. Back up Postgres.
2. Apply new migrations:
   ```bash
   docker compose run --rm migrate
   ```
   Migrations are idempotent within a release; re-running an applied migration is a no-op.
3. Rolling-restart supervisors, then scheduler, then control-api. Executors and claim-producers last. Order preserves dispatch claim semantics: supervisors drain their claims gracefully when SIGTERM'd; scheduler pauses tick; control-api is stateless.
4. Verify:
   ```bash
   curl -s http://localhost:8080/health | jq .
   ```

### 13.1 Pre-v1 schema reset

Rimsky is pre-v1: there is no production data preservation guarantee. When a refactor changes the schema in place (rewriting an existing migration file rather than appending a new one), `rimsky_migrations` will record the file as already applied and the migration runner will skip the rewrite, leaving the schema wrong.

**Required step: nuke the dev database before running the new code.** This applies only to dev / staging environments — there is no production-data migration path, by design.

Compose:

```bash
docker compose -f deploy/docker-compose.yml down -v   # -v drops the postgres volume
docker compose -f deploy/docker-compose.yml up -d     # fresh DB; migrate runs cleanly
```

Kubernetes / external Postgres: drop and recreate the rimsky database (or drop the `rimsky_*` tables plus `rimsky_migrations`) before bringing up the new images.

---

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

## 15. Observability & dashboard

Rimsky exposes three optional public observability protocols, one per existing collection. Per `docs/specs/2026-05-02-dashboard-and-observability-design.md`:

- **Rimsky observability API** — read-only HTTP/JSON on `rimsky-control-api`, mounted under `/v1/observability/*`. Backed by the `rimsky_*` tables; no new schema. Resource-oriented browse + detail endpoints for templates, instances, frames, nodes, worker-requests, claim handles, schedules, events, plus per-peer topology (declared executors and claim-producers with their observability capabilities). Per the dashboard spec, claim-handle responses expose only `claim_id` and `scope_data`; payload/address bytes never traverse rimsky-side endpoints.
- **Executor observability protocol** — gRPC service + HTTP+JSON bridge per executor. `GetCapabilities`, `GetTrace(dispatch_id)`, `StreamTrace`. Optional; executors that don't implement it return `Unimplemented` for the RPCs.
- **Claim-producer observability protocol** — gRPC service + HTTP+JSON bridge per producer. `GetCapabilities`, `GetClaim`, `StreamClaim`, `ListClaims`, `GetAdminView`. Optional in the same way.

### 15.1 `observability_endpoint:` per peer

Each `executors:` and `claim_producers:` block in `rimsky.yml` accepts an optional `observability_endpoint:` field. When omitted, control-api uses the dispatch `endpoint` for the observability handshake. Override when the observability surface lives on a different port or host than the dispatch surface.

```yaml
executors:
  - name: claude-agent
    endpoint: claude-agent:9090
    observability_endpoint: claude-agent:9091

claim_producers:
  - name: topics-ring
    endpoint: store-postgres:9101
    observability_endpoint: store-postgres:9103
    write_semantics_envelope: [staged_async]
```

The observability handshake is best-effort: unreachable peers or absent endpoints are recorded as `reachability_status: unreachable`; control-api startup is not aborted. A background re-prober (`RIMSKY_OBSERVABILITY_REFRESH_INTERVAL`, default `60s`) re-probes peers so transient unreachability heals.

### 15.2 `http_bridge_url:` per peer

Each peer declares its own HTTP+JSON bridge URL via `http_bridge_url`. The bridge URL is returned in the peer's capabilities response and used by the dashboard's reverse proxy to dial the peer from the browser. When a peer's gRPC and HTTP-bridge ports differ (the case for every shipped executor and claim-producer), set this explicitly:

- **Claim producers** declare it in their own YAML.
- **Executors** declare it via env var.

When empty, the peer exposes only the gRPC observability surface and the dashboard cannot proxy to it from the browser.

### 15.3 Reference dashboard

`dashboards/rimsky-dashboard/` ships as a single Node + SPA process. Bundled with the dev `docker-compose.yml`:

```sh
docker compose -f deploy/docker-compose.yml up -d
# open http://localhost:8090
```

The dashboard reads `RIMSKY_CONTROL_API_URL` (default `http://control-api:8080`) and `PORT` (default `8090`). It listens on a single port and exposes `/healthz` for compose / k8s liveness.

### 15.4 Auth posture

V1 inherits the per-project deployment / network-perimeter model. The observability API is unauthenticated; the dashboard is unauthenticated. Run them behind the same perimeter as control-api. Tenant scoping and front-end auth are forward-scope.

### 15.5 Discovery cache refresh

The dashboard server caches the executor/claim-producer list it gets from the control-api for 30 seconds. Two operator-facing endpoints expose this cache:

```
GET  /api/admin/discovery               → cache age + counts
POST /api/admin/refresh-discovery       → invalidate cache; next lookup re-fetches
```
