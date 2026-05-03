# `rimsky-cli` and `rimsky-compose` v1 — Design

## Status

- Spec, 2026-05-02.
- Outcome of the 2026-05-02 brainstorm covering the CLI and compose surface that the control-plane v1 spec deferred (`docs/specs/2026-05-01-control-plane-and-store-lifecycle-design.md` §11.1).
- Foundational dependencies:
  - `docs/specs/2026-05-01-control-plane-and-store-lifecycle-design.md` — the control-api surface this spec wraps.
  - `docs/specs/2026-04-27-stores-redesign-v3-design.md` and the `2026-04-30` cleanup overlay — runtime contract.
  - `docs/2026-04-26-control-layer.md` §2.6 — original CLI sketch.
  - `docs/2026-05-01-auth-and-multitenancy.md` — per-project deployment posture; the CLI assumes the operator's auth perimeter is the access-control surface.

## Context

The control-plane v1 surface is specced and (mostly) shipped. What's missing is the operator-facing tooling: a CLI for direct interaction with the control-api and a declarative manifest format (`rimsky-compose.yml`) for orchestrating templates and persistent instances. This spec lands both.

The architectural backbone:

- **A rimsky deployment is just three Go processes plus a state DB.** Store-services and executors are independent, rimsky-aware peer services (gRPC + HTTP+JSON bridges). The infrastructure that hosts these (Docker, Kubernetes, Terraform, ECS, bare metal) is operator-managed and **rimsky-invisible** — rimsky never inspects, names, or assumes anything about it. The word "substrate" is deliberately not used in this spec or the artifacts it produces; "operator infrastructure" or "infra" is the consistent term.
- **The CLI is a thin client over the control-api.** It contains no orchestration logic of its own. Every CLI verb either maps directly to a control-api endpoint or composes several into a higher-level workflow (`compose up`, `run`, `init`).
- **`rimsky-compose.yml` is application-layer.** It describes templates, tags, and persistent instances — what should exist inside an already-running rimsky deployment. It does not bring rimsky up. It does optionally invoke an operator-supplied infra command (`docker compose up`, `terraform apply`, `kubectl apply`, etc.) as a pre-step for the local dev loop.
- **Compose owns scoped resources only.** Like Docker Compose's project label, every compose-managed resource carries a project marker. Reconcile only acts on resources belonging to the manifest's project; manually-created resources and resources owned by other compose projects are invisible to it.
- **Apply-once-and-exit.** `compose up` computes a plan, executes it serially in dependency order, and exits. There is no long-running watcher. Drift between runs is the operator's responsibility (or detected by `compose plan` in CI).

What this spec does not cover (see §10):

- Auth, principal field, policy hook (`docs/2026-05-01-auth-and-multitenancy.md` §2).
- Package manager / OCI distribution (`docs/2026-04-26-package-manager.md`).
- Audit logging.
- Cron / scheduled instance management beyond what templates already declare.
- Multi-cluster / federation.
- Web UI.

---

## 1. CLI verb surface

### 1.1 Top-level layout

The CLI has three verb categories, all reachable from a single `rimsky-cli` binary:

1. **Ergonomic top-level** — flat, dev-loop-shaped verbs.
2. **Literal API subgroups** — one verb per control-api endpoint, grouped by resource.
3. **Compose** — declarative manifest reconciliation.

All categories coexist; the ergonomic verbs are aliases / compositions over the literal subgroups. Both shapes are documented in `--help`.

### 1.2 Verb inventory

```
# Ergonomic top-level (dev-loop)
rimsky-cli run <file> [--params <json|@file>] [--key <instance-key>] [--tag <tag>] [--keep | --no-keep]
rimsky-cli register <file> [--tag <tag>]
rimsky-cli deploy <ref>
rimsky-cli undeploy <ref>
rimsky-cli instantiate <ref> [--params <json|@file>] [--key <instance-key>]
rimsky-cli rm-instance <instance-id>                  # alias for `instance delete`; terminal-only
rimsky-cli ls [templates|instances|tags]              # default: instances
rimsky-cli logs <instance-id> [--follow] [--poll-interval <duration>]
rimsky-cli health
rimsky-cli init [<directory>] [--with-postgres-store] [--with-claude-agent]

# Compose
rimsky-cli compose up    [-f <manifest>] [--yes]
rimsky-cli compose down  [-f <manifest>] [--yes] [--infra]
rimsky-cli compose plan  [-f <manifest>]
rimsky-cli compose status [-f <manifest>]

# Literal API subgroups (foundation)
rimsky-cli template register <file> [--tag <tag>] [--source <source>]
rimsky-cli template list [--state <state>] [--tag-prefix <prefix>]
rimsky-cli template get <ref>
rimsky-cli template deploy <ref>
rimsky-cli template undeploy <ref>
rimsky-cli template rm <ref>

rimsky-cli tag create <tag> --template <ref>
rimsky-cli tag list [--prefix <prefix>]
rimsky-cli tag get <tag>
rimsky-cli tag mv <tag> --template <ref>
rimsky-cli tag rm <tag>

rimsky-cli instance create <template-ref> [--params <json|@file>] [--key <instance-key>]
rimsky-cli instance list [--template <ref>] [--key-prefix <prefix>]
rimsky-cli instance get <id-or-key>
rimsky-cli instance delete <id-or-key>                 # 409 if not in terminal state
rimsky-cli instance nodes <id-or-key>
rimsky-cli instance events <id-or-key> [--follow] [--poll-interval <duration>]

rimsky-cli node get <node-id>

rimsky-cli admin force-fire <node-id>
rimsky-cli admin invalidate <node-id> [--reason <text>]
rimsky-cli admin reset <node-id>

# Dev-loop infra wrapper
rimsky-cli dev up    [-f <manifest>]
rimsky-cli dev down  [-f <manifest>] [--infra]
rimsky-cli dev status [-f <manifest>]

# Context management (kubectl-shaped)
rimsky-cli ctx list
rimsky-cli ctx use <name>
rimsky-cli ctx add <name> --endpoint <url>
rimsky-cli ctx rm <name>
rimsky-cli ctx current
```

### 1.3 Ergonomic-verb semantics

- `rimsky-cli run <file>` — registers the spec at `<file>`, deploys it, instantiates it, and prints the instance ID. Composes `template register` + `template deploy` + `instance create` into one call. With `--keep` (default), exits after instance creation. With `--no-keep`, polls `GET /instances/{id}` until `terminated_at` is set, then `DELETE /instances/{id}`, then `template undeploy` + `template rm` (refuses with a non-fatal warning if other instances or tags reference the template; pre-v1 we accept the warning rather than building a more elaborate cleanup). The poll interval is configurable via `--poll-interval` (default 1s) and a `--timeout` (default unbounded). `--no-keep` is the only ergonomic verb that runs longer than a single API call; it remains an apply-once-and-exit verb (it just polls a single state until a condition resolves, then exits).
- `rimsky-cli register <file>` — alias for `template register`.
- `rimsky-cli deploy <ref>` — alias for `template deploy`.
- `rimsky-cli undeploy <ref>` — alias for `template undeploy`.
- `rimsky-cli instantiate <ref>` — alias for `instance create`.
- `rimsky-cli rm-instance <id>` — alias for `instance delete`. Refused (409 propagated as exit 1) on non-terminal instances; the error message includes the "instance has not reached terminal state" text from the control-api.
- `rimsky-cli ls` — defaults to listing instances; `ls templates`, `ls instances`, `ls tags` for explicit selection.
- `rimsky-cli logs <id-or-key>` — alias for `instance events --follow`. Polling-based; not a true stream.
- `rimsky-cli init` — see §1.5.

### 1.4 `dev` wrapper semantics

`dev up` / `dev down` / `dev status` are conveniences over `compose up` / `compose down` / `compose status` when the manifest declares an `infra:` block (§2.5).

`dev up` order of operations:

1. Parse and locally-validate the manifest (§2.8).
2. If `rimsky_config.inline` is present, materialize it to `./.rimsky/rimsky.yml`, **always overwriting any prior file**. (Materialization is `dev up`-only; `compose up` never materializes — see §2.5.)
3. Run `infra.up.command` synchronously. Non-zero exit → exit code 1 with the command's stderr appended.
4. If `infra.up.wait_for` is set, GET-poll the URL at the configured interval until 2xx, or until `timeout` elapses (default 60s). Timeout → exit code 1.
5. Run `compose up` (the same plan-and-apply path described in §3).

`dev down` order of operations:

1. Parse the manifest.
2. Run `compose down` (cleans up app-state — see §3.7).
3. If `--infra` is set and `infra.down.command` is defined, run it. Without `--infra`, `dev down` is identical to `compose down`.

`compose up` and `dev up` differ only in steps 2–4 above; `compose up` skips them entirely and assumes the operator's infrastructure is already up. This split keeps CI scripts (which manage their own infra outside compose) free of accidental infra invocations.

If `infra:` is absent from the manifest, `dev up` is functionally identical to `compose up`.

### 1.5 `init` semantics

`rimsky-cli init [<directory>]` scaffolds a starter project. With no argument, scaffolds in the current directory. The scaffold writes:

- `rimsky-compose.yml` — a starter manifest with `project: <directory-basename>`, an `infra:` block whose `up.command` invokes `docker compose -f deploy/docker-compose.yml up -d` against a co-located `deploy/docker-compose.yml` (copied from embedded assets, see below), `wait_for: http://localhost:8080/health`, an inline `rimsky_config:` declaring one filesystem store-service and one HTTP-node executor in stub mode, one entry in `templates:` (state `deployed`), and one persistent instance with `restart: never`.
- `./deploy/docker-compose.yml` — copied from the embedded reference; matches the rimsky module's `deploy/docker-compose.yml` at the CLI version's build time. Users edit it freely; `dev up` shells out to `docker compose` against this file.
- `./deploy/store-filesystem.yml` — store-service config referenced by `docker-compose.yml`.
- `./deploy/supervisor-config.yml` — per-process tuning for the supervisor.
- `./graphs/example.yml` — a minimal one-node template using the HTTP-node executor in stub mode.
- `./.rimsky/` — empty directory; created on first `dev up` to hold the rendered `rimsky.yml`.
- `./.gitignore` — created or appended; adds `/.rimsky/`.

`init` does **not** write a `rimsky.yml` to disk; the inline `rimsky_config:` block in `rimsky-compose.yml` is the source of truth, materialized to `./.rimsky/rimsky.yml` by `dev up`. Users who prefer an external file can replace `rimsky_config.inline` with `rimsky_config.path: ./rimsky.yml` and write the file themselves.

Flags (all reduced from the brainstorm sketch to a single minimal scaffold for v1):

- `--force` — overwrites existing files in the target directory. Without it, `init` refuses if any of the scaffold files already exist.

The `--with-postgres-store` and `--with-claude-agent` flags from the brainstorm are deferred (see §10). The minimal scaffold is the only shape v1 ships; users extending to postgres or claude-agent edit the manifest and `docker-compose.yml` by hand using the operator-guide as reference.

### 1.6 Verb-to-endpoint mapping

Every literal subgroup verb corresponds to exactly one control-api endpoint. Endpoint paths reflect the current control-api implementation (chi router, no `/v1/` prefix). Both `instance get` and the lookup-by-key shapes accept the `idOrKey` chi parameter (a UUID or an `instance_key`).

| Verb | Method + Path |
|---|---|
| `template register` | `POST /templates` |
| `template list` | `GET /templates` |
| `template get` | `GET /templates/{ref}` |
| `template deploy` | `POST /templates/{ref}/deploy` |
| `template undeploy` | `POST /templates/{ref}/undeploy` |
| `template rm` | `DELETE /templates/{ref}` |
| `tag create` | `POST /tags` |
| `tag list` | `GET /tags` |
| `tag get` | `GET /tags/{tag}` (CLI-side lookup against `GET /tags` if no per-tag GET exists) |
| `tag mv` | `PUT /tags/{tag}` |
| `tag rm` | `DELETE /tags/{tag}` |
| `instance create` | `POST /instances` |
| `instance list` | `GET /instances` |
| `instance get` | `GET /instances/{idOrKey}` |
| `instance delete` | `DELETE /instances/{idOrKey}` (terminal-state only — see below) |
| `instance nodes` | `GET /instances/{idOrKey}/nodes` |
| `instance events` | `GET /events?instance_id={id}&cursor=…` (paginated; `--follow` polls) |
| `node get` | `GET /nodes/{id}` |
| `admin force-fire` | `POST /admin/scheduled-nodes/{node_id}/force-fire` |
| `admin invalidate` | `POST /nodes/{id}/invalidate` |
| `admin reset` | `POST /nodes/{id}/reset` |

**Instances cannot be aborted while running.** The control-api has no kill / abort path for in-flight work; the `rimsky_nodes.kill_requested` column was removed (per CLAUDE.md). Instances reach terminal state naturally when all nodes are `fresh` or `failed`, at which point the supervisor's auto-terminal mechanism sets `terminated_at` and the terminator worker fires `OnInstanceTerminated`. `DELETE /instances/{id}` is refused with HTTP 409 if `terminated_at IS NULL`.

The CLI verb is **`instance delete`** (renamed from `terminate` in the brainstorm to match the endpoint and avoid promising a kill semantic that doesn't exist). The ergonomic top-level alias is `rimsky-cli rm-instance <id>`; the alias `terminate` is **not** provided (its name would mislead). For a running instance, the operator's options are:

- Wait for natural terminal state.
- Use `admin invalidate` on individual nodes to force re-execution (does not abort).
- Use `admin reset` on individual nodes (same caveat).

`instance events` uses the existing `GET /events?instance_id=…` paginated endpoint. `--follow` is client-side polling (default 1s interval; `--poll-interval` flag overrides). There is no SSE / streaming endpoint in v1; if one lands later, the CLI's `--follow` switches to it transparently.

The CLI does not transform request or response bodies beyond pretty-printing for human output. JSON output (`-o json`) emits the raw response body.

---

## 2. Manifest schema (`rimsky-compose.yml`)

### 2.1 Full shape

```yaml
# rimsky-compose.yml — application-layer manifest.

project: ingest-pipeline                 # required; ownership scope
context: dev                              # optional; pins CLI context

infra:                                    # optional; operator-owned commands
  up:
    command: ["docker", "compose", "-f", "deploy/docker-compose.yml", "up", "-d"]
    wait_for: "http://localhost:8080/health"
    timeout: 60s
  down:
    command: ["docker", "compose", "-f", "deploy/docker-compose.yml", "down", "-v"]

rimsky_config:                            # optional; rimsky.yml inline or by reference
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
  # OR (mutually exclusive with inline):
  # path: ./rimsky.yml

templates:
  - path: ./graphs/ingest.yml
    tag: ingest@1.0                       # optional; auto-derived from spec hash if omitted
    state: deployed                       # registered | deployed (default: deployed)

instances:
  - template: ingest@1.0                  # tag (project-relative) or full hash
    name: daily-ingest                    # required; project-unique
    params:
      window: "24h"
    restart: on_failure                   # never (default) | on_failure | always
```

### 2.2 `project` (required)

Identifier carrying the manifest's ownership scope. Format: `^[a-z][a-z0-9-]{0,62}$`.

Used as a prefix for compose-owned tags and instance keys (§2.6, §2.7). Also stamped into output (the human-readable form of `compose plan` prints `[project=<name>]` headers).

If absent, `compose up` exits with a usage error (exit 2). Auto-derivation from directory name is rejected as too implicit; explicit requirement removes a class of "wait, what project did I just operate against" mistakes. (`init` writes the field automatically using the directory basename as a starting point; the user can edit it before the first `compose up`.)

### 2.3 `context` (optional)

If set, every `compose` and `dev` invocation against this manifest is pinned to the named context regardless of the CLI's current context. Format: matches the context-name regex in `~/.rimsky/config.yml` (`^[a-zA-Z][a-zA-Z0-9._-]{0,62}$`).

If the named context does not exist in `~/.rimsky/config.yml` at invocation time, the CLI exits with a usage error (exit 2) before making any API calls. This is a guard against committing a manifest that references a context the local user doesn't have configured — the failure is loud and pre-flight.

If `context:` is absent, the CLI uses its current context (set via `rimsky-cli ctx use`, the `RIMSKY_CONTEXT` env var, or the `current_context` field in `~/.rimsky/config.yml`).

### 2.4 `infra` (optional)

Operator-supplied commands for bringing up and tearing down the infrastructure that hosts the rimsky deployment. Rimsky-invisible: the CLI shells out to the commands as-is, with no introspection of what they do.

Fields:

- `infra.up.command` — array of strings; argv to exec. Required.
- `infra.up.wait_for` — URL polled with `GET` until a 2xx response or `timeout` elapses. Optional; if absent, the CLI proceeds immediately after `infra.up.command` returns.
- `infra.up.timeout` — duration string (`60s`, `5m`); default `60s`.
- `infra.down.command` — array of strings. Required if `infra.down` is to be invocable; if absent, `dev down --infra` errors out with "no infra.down command defined."

Environment: the commands inherit the CLI's environment plus `RIMSKY_PROJECT=<project>` and `RIMSKY_CONTEXT=<context>` (if set).

The CLI does not retry `infra.up.command` on non-zero exit. The operator owns retry semantics if they want them inside their own command (e.g., `docker compose up -d --wait`).

### 2.5 `rimsky_config` (optional)

Mutually exclusive sub-blocks:

- `rimsky_config.inline` — full `rimsky.yml` body (the §3.1 of the control-plane v1 spec — `stores:`, `named_locks:`, `executors:`). **Materialization is `dev up`-only**: when present, `dev up` writes this to `./.rimsky/rimsky.yml` (gitignored) before running `infra.up.command`. `compose up` never materializes — it assumes the file already exists wherever the running rimsky processes are configured to read it. The operator's infra command is responsible for picking up the materialized file (e.g., a docker-compose.yml that mounts `./.rimsky/rimsky.yml:/etc/rimsky/rimsky.yml:ro`).
- `rimsky_config.path` — relative path to an external `rimsky.yml`. The CLI does not modify or copy the file; the operator's infra command is responsible for picking it up.

If both are present, the CLI exits with a usage error (exit 2). If neither is present, the CLI assumes the operator's infra command knows how to wire up `rimsky.yml` on its own.

`dev up`'s materialization always overwrites `./.rimsky/rimsky.yml` — there is no diff-and-warn or merge logic; the manifest's inline content is the source of truth. The materialized file is intended to be ephemeral (gitignored, regenerated each `dev up`).

The CLI does not validate the `rimsky_config` content beyond YAML well-formedness. The rimsky processes themselves do strict-equality validation of stores capabilities at startup; surfacing those errors is the running deployment's job, not compose's.

### 2.6 `templates` (optional)

Array of objects:

- `templates[].path` — relative path to a template spec file (YAML). Required.
- `templates[].tag` — user-facing tag for the spec. **Required.** Format: must match the control-api's tag regex `^[a-zA-Z][a-zA-Z0-9._:@/-]{0,254}$` minus a hash-shape match, AND must not start with `compose:` (the prefix is added automatically; users who include it explicitly get a usage error from §2.8 validation). The actual tag stored in the control-api is `compose:<project>:<tag>` (project-prefixed).
- `templates[].state` — `registered` or `deployed`. Default: `deployed`. Compose drives the state machine (registers if absent; deploys if `state: deployed`; undeploys back to registered if `state: registered` and the template is currently `deployed`).

A required `tag:` field is non-negotiable for compose-managed templates because the project-prefixed tag is the ownership marker — without it, compose has no way to scan the control-api for "templates I own" and cannot reconcile them on subsequent `compose up` or `compose down`. Users who want a register-once-and-leave-alone template create it via `rimsky-cli template register` outside the manifest.

The control-api's existing template hashing (RFC 8785 JCS canonicalization) determines identity. Two compose projects registering identical spec content produce one underlying template row; each project owns its own project-prefixed tag pointing at the shared hash. `compose down` removes only the prefixed tag, then attempts `DELETE /templates/{hash}` on the underlying template — the control-api refuses (HTTP 409) if any other tags still reference the hash or if any instances bind to it; compose treats the 409 as a successful cleanup-skip (the template is shared with another owner) and proceeds.

### 2.7 `instances` (optional)

Array of objects describing persistent instances:

- `instances[].template` — tag or full hash. Tag references resolve against the **project-prefixed** namespace: a manifest `template: ingest@1.0` resolves to `compose:ingest-pipeline:ingest@1.0` for lookup against the control-api's tag table. A `sha256-…` hash is used directly. Required.
- `instances[].name` — project-unique instance identifier. Format: `^[a-z][a-z0-9-]{0,62}$`. Required. Becomes part of `instance_key`: the actual instance key sent to `POST /instances` is `compose:<project>:<name>`.
- `instances[].params` — JSON-shaped object passed verbatim to the control-api as the instance's params. YAML scalars are converted to JSON scalars (string/number/boolean/null). No templating, no env-var substitution; users who need either preprocess the file with `envsubst`, `yq`, or similar before `compose up`.
- `instances[].restart` — `never` (default) | `on_failure` | `always`. Determines what happens on the next `compose up` if the instance has reached a terminal state since the last apply (§3.5).

Within a single manifest, two instances with the same `name` is a manifest-validation error (exit 2). Across compose projects, the project prefix scopes ownership, but operators sharing one control-api should still pick distinct project names — see §8.3 for the ownership-collision contract.

### 2.8 Validation

`compose up` validates the manifest locally before making any API calls. Failures exit with code 2 and an error listing every validation failure (not just the first):

- Required fields present (`project`; `name` on every instance; `path` and `tag` on every template).
- `project`, instance `name`, and template `tag` formats match their regexes.
- `templates[].tag` does not start with `compose:` (the prefix is automatically applied; explicit prefixing is an error).
- `rimsky_config.inline` and `rimsky_config.path` are mutually exclusive.
- Every `instances[].template` resolves to either a tag declared in `templates[]` or a hash matching `^sha256-[0-9a-f]{64}$`.
- No two `instances[]` entries share the same `name`.
- No two `templates[]` entries share the same `path` or the same `tag`.
- `infra.up.wait_for`, if set, parses as an absolute URL with scheme `http` or `https`. Only HTTP probes are supported in v1; non-HTTP infra readiness checks are out of scope (operators bake any complex readiness logic into `infra.up.command` itself, e.g., `docker compose up -d --wait`).
- `infra.up.timeout`, if set, parses as a Go-style duration (`60s`, `5m`).

The `wait_for` URL is not validated for reachability at parse time (the running deployment may not be up yet); just for syntactic well-formedness.

---

## 3. Reconcile semantics

### 3.1 Plan computation

`compose up` and `compose plan` both compute a plan; `up` executes it, `plan` prints and exits. The plan is computed by:

1. Parse and locally-validate the manifest (§2.8).
2. Resolve all template references (paths → spec content → content hashes via `core/canonical/jcs.go`).
3. Query the control-api for the current state of compose-owned resources. The control-api's list endpoints (`GET /tags`, `GET /instances`) do not currently support prefix filtering — the CLI lists the full set and filters client-side by the `compose:<project>:` prefix. For deployments with many tags / instances this is a list-then-filter cost; if it becomes a bottleneck, a server-side prefix-filter parameter is a small additive change to the control-api (out of scope here).
4. Compute the diff:
   - Templates in manifest but not in control-api → register + tag.
   - Templates in control-api with matching tag but a different hash → register the new hash, move the tag (PUT /tags/{tag}), then schedule **undeploy of the old hash if it is currently `deployed`** followed by `DELETE /templates/{old-hash}` (executed in steps 5 / 8 of §3.3). Without the undeploy step, the subsequent template delete would 409 and the old hash would orphan in `deployed` state.
   - Compose-owned tags not in manifest → schedule **undeploy of the underlying hash if it is currently `deployed`**, then delete tag, then `DELETE /templates/{hash}` (executed in steps 5 / 6 / 8). The undeploy is required because the control-api refuses `DELETE /templates/{hash}` on a `deployed` template, and a removed-from-manifest tag with no other tags pointing at the underlying hash should result in a fully-cleaned-up template.
   - Template state mismatches (manifest says `deployed`, control-api says `registered`/`undeployed`) → deploy. Manifest says `registered`, control-api says `deployed` → undeploy.
   - Instances in manifest but not in control-api (no row, or only a `terminated_at IS NOT NULL` row remains for the same key) → if a terminal-state row exists, schedule `DELETE /instances/{id}` first to free the unique-constraint slot (§3.5), then `POST /instances` with the manifest's params.
   - Compose-owned non-terminal instances not in manifest → **error**. The control-api has no in-flight termination, so removing a running instance from the manifest cannot be reconciled by `compose up`. The CLI prints "instance compose:<project>:<name> is still running and cannot be removed; wait for terminal state and re-run, or invalidate manually" and exits with code 1. (This is rare: instances reach terminal naturally; the case is "instance was removed from manifest mid-run before it finished.")
   - Compose-owned terminal instances not in manifest → schedule `DELETE /instances/{id}` (cleans up the row + fires `OnInstanceTerminated` lifecycle).
   - Instances with matching name in non-terminal state but mismatched `params` → mark as drift; v1 does not modify; print warning and continue. (Updating params on a running instance is not a control-api operation.)
   - Instances with matching name in terminal state and any restart policy → §3.5 governs whether to recreate.

### 3.2 Plan output

Plan output uses the **prefixed form consistently** for every resource. A compose-managed template has its tag stored in the control-api as `compose:<project>:<tag>`; that's what the plan prints. The manifest's bare-tag form (`ingest@1.0`) appears only when echoing the manifest itself.

Human form: a table of operations, grouped by resource kind, in execution order. Roughly:

```
Plan for project=ingest-pipeline, context=dev:

Templates:
  + register  hash=sha256-abc123…   from=./graphs/ingest.yml
  + tag       compose:ingest-pipeline:ingest@1.0   → sha256-abc123…
  + deploy    compose:ingest-pipeline:ingest@1.0
  - undeploy  compose:ingest-pipeline:legacy@0.9
  - tag-rm    compose:ingest-pipeline:legacy@0.9
  - delete    sha256-def456…   (no remaining references)

Instances:
  + create     compose:ingest-pipeline:daily-ingest  template=compose:ingest-pipeline:ingest@1.0
  ~ recreate   compose:ingest-pipeline:hourly-ingest  (restart=on_failure, current state=failed)
  - delete     compose:ingest-pipeline:obsolete-job   (terminal state)

7 changes.
```

JSON form: the same plan as a structured array, suitable for piping into `jq` for filtering or for CI gating. Field names match the control-api's response shape — instance keys use `instance_key`, template hashes use `template_hash`, etc. Example:

```json
{
  "project": "ingest-pipeline",
  "context": "dev",
  "plan": [
    {"action": "register", "kind": "template", "from": "./graphs/ingest.yml", "template_hash": "sha256-abc123..."},
    {"action": "tag",      "kind": "tag",      "tag": "compose:ingest-pipeline:ingest@1.0", "template_hash": "sha256-abc123..."},
    {"action": "deploy",   "kind": "template", "tag": "compose:ingest-pipeline:ingest@1.0"},
    {"action": "create",   "kind": "instance", "instance_key": "compose:ingest-pipeline:daily-ingest", "template_tag": "compose:ingest-pipeline:ingest@1.0"}
  ],
  "summary": {"changes": 4}
}
```

`compose plan` exits with:

- `0` if the plan is empty (manifest matches control-api state and no compose-owned resources exist outside the manifest) **and** no drift warnings were emitted.
- `1` if the control-api is unreachable or returns 5xx. (Consistent with `compose up`'s exit-on-API-failure; status query failures are real errors, not "no drift.")
- `2` if the manifest fails local validation.
- `3` if drift is detected. Drift means either (a) the plan has at least one actionable step, or (b) `ComputePlan` emitted at least one drift warning even when the step list is empty. The canonical warnings-only case is params drift on a running compose-owned instance: the manifest's `params:` no longer match the deployed row, the control-api offers no in-flight params update, and the plan therefore emits no Step — but the manifest's intent has diverged from reality, so CI must surface it. Mirrors `terraform plan -detailed-exitcode`.

### 3.3 Plan execution

`compose up` executes the plan serially in the following dependency order, regardless of how it was sorted in the plan output:

1. **Template registers.** `POST /templates` for new template hashes (manifest order; no inter-template deps in v1).
2. **Tag creates and moves.** `POST /tags` for new tags; `PUT /tags/{tag}` for tag moves.
3. **Template deploys.** `POST /templates/{ref}/deploy` for templates whose manifest state is `deployed` and whose control-api state is `registered`/`undeployed`.
4. **Instance deletes.** `DELETE /instances/{id}` for compose-owned instances scheduled for removal or for recreation under §3.5. The control-api refuses (409) if the instance is non-terminal; if any such 409 occurs in this step, the plan computation logic from §3.1 should already have caught it and bailed at the "compose-owned non-terminal instances not in manifest" diff bullet (an immediate error during plan computation). A 409 here is an unexpected race (instance became non-terminal between plan and apply) and exits with code 1.
5. **Template undeploys.** `POST /templates/{ref}/undeploy` for templates scheduled for state-transition (manifest `registered`, control-api `deployed`) and for templates whose tag is being removed from the manifest, the old hash after a tag-mv, or any other case where a deployed template will be deleted in step 8 (per §3.1's diff logic). Undeploy is required before delete because the control-api refuses `DELETE /templates/{hash}` on `deployed` templates. The undeploy itself can 409 (refused if any non-terminal instances bind to the template) — but the dependency is real: instance deletes (step 4) precede template undeploys (step 5), so by the time step 5 runs, no non-terminal instances bound to compose-owned templates remain. A 409 from undeploy here means an instance owned by another compose project (or a manual user) still binds to the template; compose treats it as a cleanup-skip for the affected template-delete (step 8) and continues.
6. **Tag deletes.** `DELETE /tags/{tag}` for compose-owned tags removed from the manifest. The order tag-delete-after-undeploy matches the control-api's lifecycle: undeploy fires `OnTemplateUndeployed`, then tag delete is a metadata operation.
7. **Instance creations.** `POST /instances` for new and recreated instances. The body shape is `{template, params, instance_key}` per the control-plane v1 spec §2.2; the CLI sends the project-prefixed `instance_key` and resolves `template` to either a hash or a project-prefixed tag. The dependency that places this step here: a manifest entry that creates an instance from `ingest@1.0` relies on `ingest@1.0` being registered, tagged, and deployed — steps 1–3 ensure that. Placing instance creates after the cleanup steps (4–6) and before the best-effort template deletes (8) keeps the cleanup of removed resources cleanly bracketed around the new-resource creation, but there is no hard cross-template dependency between this step and step 8 (instance creates target manifest-resident templates; step 8's deletes target manifest-removed templates).
8. **Template deletes.** `DELETE /templates/{hash}` for hashes whose last compose-owned tag was removed (step 6) or replaced (step 2's tag-mv). Each delete is best-effort: HTTP 409 means another tag still references the hash or instances bind to it — the CLI logs `template-delete: skipped (still referenced)` and continues. This is how compose avoids leaving dangling content-addressed templates without violating the control-api's "no GC in v1" stance — compose explicitly attempts the delete; the control-api is the authority on whether it's safe.

Each API call is synchronous (the control-api is synchronous per spec §5.5). Compose does not parallelize within a step. Logging prints one line per API call: action + target + result.

### 3.4 Failure handling

Fail-fast with resumable retry (no rollback). On the first non-2xx response from the control-api:

1. Print the failed operation, the HTTP status, the response body's structured error fields (validation_errors, failed_stores, etc.), and a one-line "to retry: `rimsky-cli compose up [-f <manifest>]`" hint.
2. Exit with code 1.

Already-applied operations are not undone. The control-api's lifecycle endpoints are progress-preserving (per spec §5.4): re-running `compose up` after fixing the underlying problem resumes from the failed step. Compose itself does not need failure-state bookkeeping; the control-api carries it via `rimsky_store_lifecycle`.

### 3.5 Restart-policy semantics

For each instance in the manifest:

- The plan looks up the instance by key (`compose:<project>:<name>`) in the control-api via `GET /instances?key_prefix=…`.
- **Terminal-vs-non-terminal classification reuses the control-api's `terminated_at` field.** Non-terminal: `terminated_at IS NULL`. Terminal: `terminated_at IS NOT NULL`. This matches the supervisor's auto-terminal predicate (blessed invariant 13) — compose does not re-derive terminality from node states; it reads the field the control-api already exposes.
- If the instance does not exist → schedule create (regardless of restart policy).
- If the instance exists and is non-terminal → leave alone (the restart policy doesn't apply to running instances; params drift is warned per §3.1 but not acted on).
- If the instance exists and is terminal:
  - **Aggregate outcome = success** (every node ended in `fresh`): policy `always` → schedule delete + create; `on_failure` → schedule delete-only (cleanup of the terminal row, no recreate); `never` → schedule delete-only.
  - **Aggregate outcome = failure** (at least one node ended in `failed`): policy `always` or `on_failure` → schedule delete + create; `never` → schedule delete-only.

The "aggregate outcome" classification is computed client-side from the `GET /instances/{id}/nodes` response — every node's `state` is checked. Alternative: if `GET /instances/{id}` exposes a `terminal_outcome` summary field, the CLI uses it instead of fetching the node list. Implementation chooses based on what the control-api actually returns (see §9 open questions).

**Recreate mechanics.** The control-api's `(template_hash, instance_key)` uniqueness constraint requires the prior row to be **deleted**, not just have `terminated_at` set, before a new row with the same `instance_key` can be inserted. Compose's plan handles this by ordering instance-deletes (step 4 of §3.3) before instance-creates (step 7), so by the time `POST /instances` fires, the slot is free. Each recreated instance gets a fresh UUID; the `instance_key` is reused.

**Why not just leave terminal rows alone?** A terminal row remains in the database (with `terminated_at` set) until something explicitly deletes it. Compose owns these rows for cleanup: `restart: never` instances that have terminated still get DELETEd on the next `compose up` to free the slot for a future re-instantiation if the manifest later changes the params. Operators who want to inspect terminated instances post-mortem do so before the next `compose up`.

### 3.6 Destructive-operation safety

The following operations require explicit confirmation:

- Deleting a terminal instance whose aggregate outcome was failure (the operator may want to inspect it before cleanup).
- Undeploying a template that is currently `deployed` and has any active instances bound to it (the control-api itself refuses with 409 in this case, but compose pre-checks and prompts so the operator gets a clearer error than the bare 409).
- Running `compose down` (which deletes terminal instances + undeploys templates + removes tags + attempts template deletes — destructive on the application-state surface even though it cannot abort running work).
- Running `dev down --infra` (the infra teardown command may drop volumes / databases — far more destructive than app-state cleanup alone).

Confirmation modes:

- Interactive TTY: prompt `Proceed? [y/N]`.
- Non-TTY (CI, scripts): require `--yes` flag; exit 2 with "destructive operation requires --yes" otherwise.

Non-destructive operations (registering, deploying, creating instances, deleting successfully-terminal instances during recreate) never prompt.

In-flight termination is not a destructive-confirmation case because it isn't a feature: the control-api has no "kill running instance" endpoint. `compose down` against a manifest with non-terminal compose-owned instances exits with the §3.1 "compose-owned non-terminal instances not in manifest" error before reaching the destructive-confirmation step.

### 3.7 `compose down` semantics

`compose down` reverses the application-state portion of the manifest:

1. Compute the plan: list all compose-owned instances and templates.
   - Non-terminal instances → exit code 1 with "compose down cannot abort running instances; wait for terminal state and re-run" (in-flight termination is not a control-api feature).
   - Terminal instances → schedule `DELETE /instances/{id}`.
   - Compose-owned tags → schedule undeploy (if currently `deployed`) + tag delete + best-effort template delete.
2. Confirm (§3.6).
3. Execute serially: instance deletes first, then template undeploys, then tag deletes, then best-effort template deletes (each may 409 if the template is still referenced — treated as a successful cleanup-skip).
4. If `--infra` is set and the manifest declares `infra.down`, run it last. (`dev down --infra` is the same as `compose down --infra` followed by the infra teardown.)

`compose down` without `--infra` does not run the infra hook even if `dev down` would. This split protects against `docker compose down -v` (which drops the postgres volume → loses all rimsky state) being run accidentally during routine app-state cleanup.

If `compose down` fails mid-execution (e.g., a `DELETE /instances/{id}` hits a 5xx), it exits 1 with the partial-progress hint. Already-deleted resources stay deleted; remaining work resumes on the next `compose down`. The control-api's idempotency surface (deleting an already-deleted instance returns 404, which the CLI treats as success-on-retry) makes this safe.

### 3.8 `compose status` semantics

Read-only. Queries the control-api for compose-owned resources and prints them grouped by kind. Compares against the manifest only to flag drift; does not compute or execute a plan.

Exit codes:

- `0` if the control-api responded successfully (regardless of drift; status is observational).
- `1` if the control-api was unreachable or returned 5xx (consistent with `compose plan` and `compose up`).
- `2` on local manifest validation errors.

Output annotates each compose-owned resource with one of: `in-manifest` (manifest declares it, control-api has it), `manifest-missing-from-api` (manifest declares it, control-api doesn't have it), `api-missing-from-manifest` (control-api has a compose-owned resource the manifest doesn't declare — drift). Manual users who create resources under the same `compose:<project>:` prefix outside the manifest appear under `api-missing-from-manifest`; the operator can decide whether to add them to the manifest or let `compose up` reconcile per §3.1 (which deletes terminal compose-owned instances and errors on non-terminal ones).

---

## 4. CLI configuration

### 4.1 Endpoint resolution precedence

Three tiers, highest precedence first:

1. `--endpoint <url>` flag.
2. `RIMSKY_CONTROL_API` environment variable.
3. `~/.rimsky/config.yml`'s `current_context` (or `RIMSKY_CONTEXT` env var override).

If all three are absent, the CLI exits with a usage error (exit 2) on any verb that talks to the control-api.

For compose verbs, the manifest's `context:` field overrides everything else when set (§2.3). This is intentional: the manifest pins the deployment to prevent cross-environment misfires.

### 4.2 `~/.rimsky/config.yml` shape

```yaml
current_context: dev

contexts:
  dev:
    endpoint: http://localhost:8080
  staging:
    endpoint: https://rimsky.staging.example.com
  prod:
    endpoint: https://rimsky.prod.example.com
```

Forward-compatible fields (added when auth lands per `docs/2026-05-01-auth-and-multitenancy.md` §2): `auth_token`, `auth_token_command` (for token-fetching scripts), `tls_skip_verify`, etc. Not in v1.

The file is not created by `init` (it's per-user, not per-project). `rimsky-cli ctx add` creates the file if absent.

### 4.3 Context management verbs

- `ctx list` — print context names and endpoints.
- `ctx use <name>` — set `current_context`.
- `ctx add <name> --endpoint <url>` — add a new context; refused if `<name>` already exists.
- `ctx rm <name>` — remove a context; refused if it's `current_context` (must `ctx use` something else first).
- `ctx current` — print the current context name and endpoint.

### 4.4 Embedded asset resolution

The CLI binary embeds (via Go's `embed` package) the reference assets used by `init`. The minimal v1 scaffold writes:

- `deploy/docker-compose.yml` — copied from the rimsky module's reference compose at the CLI version's build time.
- `deploy/store-filesystem.yml` — store-service config referenced by the docker-compose.yml.
- `deploy/supervisor-config.yml` — per-process tuning for the supervisor.
- `graphs/example.yml` — a single-node HTTP-stub graph.
- `rimsky-compose.yml` — generated from a template, parameterized by the project name (directory basename).

The CLI version determines which assets are embedded; users get a coherent reference set matching their CLI version. Cloud users who want different infrastructure edit the extracted `docker-compose.yml` (or replace the `infra:` block in `rimsky-compose.yml` to point at Terraform / Helm / etc.) post-`init`.

`deploy/store-postgres.yml` is **not** embedded in v1 because the minimal scaffold doesn't include the postgres store-service (per the simplified `init`, §1.5). It can be added in a later spec.

---

## 5. Output format and exit codes

### 5.1 Default human output

Tables for list operations (`ls`, `compose status`, `compose plan`); indented summaries for create/update operations; plain text for single-value outputs (`health` → "OK", `ctx current` → name + endpoint).

Color: ANSI codes when stdout is a TTY; plain when piped. `--no-color` overrides.

### 5.2 JSON output

`-o json` (or `--output json`) emits the raw control-api response body for endpoint-shaped verbs (`template list`, `instance get`, etc.). For compose verbs, emits a structured plan/status object whose field names match the control-api's response shape (`instance_key`, `template_hash`, etc. — never abbreviated to `key` or `hash`). The example in §3.2 above is the schema; field names are the contract for v1.

Schema is not versioned in v1; consumers that depend on it should pin their CLI version.

### 5.3 Exit codes

- `0` — success.
- `1` — runtime error (network failure, control-api 5xx, control-api 4xx other than 409, infra command non-zero exit).
- `2` — usage error (bad flag, malformed manifest, missing required input, destructive op without `--yes`, unknown context).
- `3` — drift detected by `compose plan` (non-empty plan **or** at least one drift warning emitted; see §3.2).

Exit codes are consistent across verbs; e.g., `template register` against an unreachable control-api exits 1, and so does `compose up`.

---

## 6. Distribution

### 6.1 Artifacts

- **Binaries.** Static Go binaries for linux/amd64, linux/arm64, darwin/amd64, darwin/arm64, windows/amd64. Built from `core/cmd/rimsky-cli/`.
- **GitHub Releases.** Tarball/zip per platform: `rimsky-cli_<version>_<os>_<arch>.tar.gz`. SHA-256 manifest published alongside. Source of truth for all other channels.
- **Install script.** A POSIX `sh` script published at `https://rimsky.io/install.sh` that detects OS/arch, downloads the corresponding GitHub Release artifact, verifies SHA-256, and installs to `/usr/local/bin/rimsky-cli`.
- **Homebrew tap.** `fallguy/rimsky/rimsky` formula. `brew install fallguy/rimsky/rimsky`. Tap repo separate from the rimsky repo (homebrew convention).
- **Container image.** `rimsky/cli:<version>` and `rimsky/cli:latest`, built from a minimal Dockerfile that copies the linux/amd64 binary into a `gcr.io/distroless/static` base. For CI use.
- **Go install.** `go install github.com/fallguy/rimsky/core/cmd/rimsky-cli@latest` works because the CLI is in the same module.

### 6.2 Versioning

The CLI version is set at build time via `-ldflags -X main.version=<version>`. `rimsky-cli version` prints it plus the embedded-asset version (the rimsky module commit at build time).

The CLI does not pin or check the control-api version. A vNext CLI talks to a vN control-api (and vice versa) on a best-effort basis: endpoints used by both versions work; endpoints only on one return 404 / 405. This matches the auth-doc's "rimsky is internal infra" posture; rolling upgrades are the operator's concern.

### 6.3 Server distribution (unchanged)

Server-side artifacts (`rimsky/scheduler:<version>`, etc.) are unchanged from current practice. The reference `deploy/docker-compose.yml` and `deploy/kubernetes/rimsky-chart/` continue to exist; cloud users (Terraform, ECS, etc.) write their own wrappers.

The Helm chart's known-stale env-vars (per `CLAUDE.md`) get refreshed as part of v1 polish; this is out of scope for the CLI/compose spec but flagged in §10.

---

## 7. Affected code

### 7.1 New files

- `core/cmd/rimsky-cli/main.go` — top-level dispatcher; subcommand routing on `os.Args[1]`.
- `core/cmd/rimsky-cli/version.go` — version stamp (set via `-ldflags`).
- `core/cmd/rimsky-cli/embed.go` — `//go:embed` directives for reference assets used by `init`.
- `core/cli/client.go` — HTTP client over the control-api; one method per endpoint; handles serialization, error parsing, retries (none in v1; pure pass-through).
- `core/cli/output.go` — table and JSON formatters; ANSI color logic.
- `core/cli/config.go` — `~/.rimsky/config.yml` load/save; context resolution.
- `core/cli/templates.go` — `template register/list/get/deploy/undeploy/rm` handlers.
- `core/cli/tags.go` — `tag create/list/get/mv/rm` handlers.
- `core/cli/instances.go` — `instance create/list/get/delete/nodes/events` handlers.
- `core/cli/nodes.go` — `node get` handler.
- `core/cli/admin.go` — `admin force-fire/invalidate/reset` handlers.
- `core/cli/run.go` — ergonomic top-level (`run`, `register`, `deploy`, `undeploy`, `instantiate`, `rm-instance`, `ls`, `logs`, `health`).
- `core/cli/init.go` — `init` scaffold logic; uses embedded assets.
- `core/cli/context.go` — `ctx list/use/add/rm/current` handlers.
- `core/cli/compose/parse.go` — manifest YAML loading and local validation.
- `core/cli/compose/plan.go` — plan computation against control-api state.
- `core/cli/compose/apply.go` — plan execution (dependency-ordered, fail-fast).
- `core/cli/compose/down.go` — `compose down` reverse-execution.
- `core/cli/compose/cmd.go` — `compose up/down/plan/status` dispatchers.
- `core/cli/compose/dev.go` — `dev up/down/status`; `infra` hook execution; `wait_for` polling.
- `core/cli/clitest/server.go` — fake control-api over `httptest.Server`; in-memory state; configurable failure injection.
- `core/cli/clitest/manifest.go` — manifest builders for tests.
- `Dockerfile.cli` — distroless-based image for `rimsky/cli`.
- `deploy/embed/example-graph.yml` — example template embedded by `init`.

### 7.2 Changed files

- `Makefile` — `build` target produces `rimsky-cli`; `release` target builds cross-platform binaries.
- `go.mod` — no new dependencies expected (stdlib `flag`, `net/http`, `embed`, `os/exec`, `gopkg.in/yaml.v3` already present transitively; verify and add if needed).
- `docs/operator-guide.md` — new "CLI" and "Compose" sections; cross-references.
- `docs/architecture.md` — add `core/cmd/rimsky-cli/` and `core/cli/` to the package layout.
- `CLAUDE.md` — add the CLI to the "Build & test" section and to the "Where to look first" pointers.
- `CHANGELOG.md` — Unreleased bullet describing this batch.

### 7.3 Tests

Test placement follows the "co-located `*_test.go`" convention.

- **Unit tests** under `core/cli/`:
  - `config_test.go` — endpoint resolution precedence; context CRUD round-trip.
  - `output_test.go` — human and JSON formatters for representative response shapes.
  - `compose/parse_test.go` — manifest parsing; every validation rule from §2.8.
  - `compose/plan_test.go` — diff computation across the resource matrix (create/update/delete × templates/tags/instances); restart-policy edge cases.
  - `compose/apply_test.go` — execution-order correctness; failure-mid-plan stops execution; idempotent re-run.
- **Fake-control-api tests** under `core/cli/clitest/`:
  - `clitest/server_test.go` — fake covers all 18 endpoints used by the CLI; failure injection works.
  - `compose/integration_test.go` — full `compose up` against fake; drift detection; `compose down` reverses; `compose plan -o json` schema.
  - `run_test.go` — ergonomic top-level verbs against fake.
- **End-to-end smoke** under `test/smoke/cli/`:
  - One test that brings up `deploy/docker-compose.yml`, runs `rimsky-cli init` in a temp dir (configured to talk to `localhost:8080`), runs `compose up`, polls until the instance reaches a terminal state, runs `compose down`, asserts `compose status` shows zero compose-owned resources. Reuses the existing smoke fixture's setup helpers.

No testcontainers-based control-api tests for the CLI specifically — the fake-control-api surface is sufficient for wire-level coverage, and the existing scenario-test suite already exercises the control-api against real Postgres.

### 7.4 Documentation

- `docs/operator-guide.md` — major addition. New sections: "Installing the CLI" (channels per §6), "Using the CLI" (verb tour with examples for the dev loop), "Compose manifests" (full `rimsky-compose.yml` reference), "Contexts" (`~/.rimsky/config.yml` shape, ctx verbs), "Cloud deployment workflows" (recipe for using rimsky-cli compose against a Terraform-deployed rimsky stack).
- `docs/architecture.md` — note the CLI as a thin client; link to this spec.
- `CLAUDE.md` — add a gotcha entry: "compose owns project-prefixed tags and instance keys; manual `rimsky-cli template register` against the same project prefix collides with compose-owned resources."
- `CHANGELOG.md` — bullet under `## Unreleased`.
- `docs/glossary.md` — add "compose project," "manifest," "context," "infra (operator-supplied)"; remove or scope any lingering "substrate" usage that creeps in (the existing auth-doc uses "substrate" in a different sense — flagged in §10 but out of scope here).

---

## 8. Compatibility & migration

### 8.1 Pre-v1 stance

Per `.claude/rules/rules.md`, rimsky is pre-v1. The CLI is a new artifact; no migration is needed for it. The `~/.rimsky/config.yml` format is greenfield. No control-api endpoints change shape; this spec only consumes them.

### 8.2 Schema migrations

None. The CLI does not touch the database. The `compose:<project>:` tag and instance-key prefix conventions live in the application's choice of identifiers; the schema is unchanged.

### 8.3 Coexistence with manual control-api use

The CLI does not assume it is the only user of a control-api. Manually-created resources (via `curl`, `rimsky-cli template register` outside compose, another tool) are invisible to compose unless they happen to fall under the same `compose:<project>:` prefix. The prefix `compose:` is reserved for compose's use; manual `rimsky-cli template register --tag compose:foo:bar` collides with compose's namespace and the CLI rejects it (the imperative `template register` validates that user-supplied tags do not start with `compose:`, mirroring the manifest-side validation in §2.6).

Within a coherent operator deployment, distinct compose projects use distinct project names — `project:` is the per-deployment ownership scope, not a globally-unique identifier. Two unrelated operators that pick `project: ingest-pipeline` against the same control-api will see each other's resources as compose-owned drift. The operator's deployment-naming convention is the contract; rimsky does not enforce it. The auth-doc's per-project deployment posture (`docs/2026-05-01-auth-and-multitenancy.md` §1) makes this a non-issue in practice — one rimsky per project means project naming collisions don't arise.

---

## 9. Open questions

Issues surfaced in design that the implementation phase settles:

1. **Hand-rolled flag parsing vs. small CLI library.** The verb tree has roughly 30 subcommands. CLAUDE.md's "resist heavier alternatives" note discourages cobra/viper-class libraries, but a small lib like `urfave/cli` or `peterbourgon/ff` would shave boilerplate. Implementation chooses; either is acceptable.
2. **Aggregate-outcome detection on terminal instances.** §3.5 needs to classify terminal instances as "success" or "failure" to apply `on_failure` policy. The control-api may already expose a summary field (e.g., `aggregate_outcome` on `GET /instances/{id}`); if not, the CLI fetches `GET /instances/{id}/nodes` and walks the node list. Implementation verifies what the control-api returns and picks the cheaper path.
3. **Plan output verbosity.** Whether to color-code action symbols (+ green, - red, ~ yellow), whether to print full hashes or first-12-chars, etc. UX details for the implementation pass.
4. **Configuration of `wait_for`.** The default polling interval (proposal: 1s) and any Authorization header / TLS config for the health-check URL. v1 keeps it minimal: GET, no headers, follow redirects, 1s interval; richer options can land later.
5. **CLI integration with shell completion.** `rimsky-cli completion bash|zsh|fish` is a common ergonomic. Out of scope for v1 spec; can land additively.
6. **Imperative `template register --tag` validation.** §8.3 says imperative `rimsky-cli template register --tag compose:foo:bar` should be rejected client-side; whether the rejection is purely client-side (the CLI returns exit 2 before calling) or threaded through the control-api as a reserved-prefix validation hook is an implementation choice. Client-side is sufficient; control-api hardening is a follow-up.
7. **Key-to-UUID resolution for `instance events`.** `GET /events?instance_id=…` requires a UUID; the CLI's `--follow` accepts `<id-or-key>`. When a key is supplied, the CLI does a one-time `GET /instances/{key}` to resolve to UUID before opening the events poll. Implementation detail; flagged so it's not skipped.
8. **List-and-filter performance for compose-owned scans.** §3.1 step 3 lists the full tag and instance sets and filters client-side. For deployments with thousands of tags / instances this is a bandwidth cost on every `compose up` / `plan` / `status`. If profiling shows it matters, adding `prefix=` query parameters to `GET /tags` and `GET /instances` is a small additive change to the control-api; v1 ships without it.

---

## 10. Out of scope

This spec deliberately does not cover:

- **Auth, principal field, policy hook, ACLs.** Per `docs/2026-05-01-auth-and-multitenancy.md` §1, v1 is per-project deployment with no rimsky-side auth. The CLI's config file leaves room for `auth_token` etc. as forward-compat fields.
- **Audit logging.** Lands with the auth-doc §2 surface.
- **Package manager / OCI distribution / signed packages.** Per `docs/2026-04-26-package-manager.md`, the package manager wraps `POST /v1/templates`. The CLI's `register` verb is the same code path the package manager will use; no special-casing needed.
- **Watch-and-converge mode for `compose up`.** Apply-once is the v1 model. Long-running reconciliation can land later if a real workflow demands it.
- **Multi-file manifests (`include:` directive).** Single-file only in v1. Additive to add later.
- **Templating in `params:` (env-var substitution, etc.).** Users preprocess with `envsubst` / `yq`. v1 stays out of templating.
- **Cron / scheduled instance management.** Templates already declare `cron:` for scheduled-node firing; compose deploys the template, the scheduler does the firing. No compose-level cron.
- **Web UI / dashboard.** Out of scope for v1.
- **`rimsky-cli pkg` verbs.** Reserved for the package manager; not in this spec.
- **`apt-get` / `yum` repositories, Windows installers, Snap / Flatpak.** Distribution channels beyond §6.1 are deferred.
- **Helm chart refresh.** The chart's known-stale env-var names (per `CLAUDE.md`) need updating to match `RIMSKY_CONFIG`. Flagged here but not implemented as part of this spec.
- **"Substrate" vocabulary cleanup elsewhere in the docs.** `docs/2026-05-01-auth-and-multitenancy.md` §3.1 uses "substrate" in the sense of "stores-as-application-substrate" — a different meaning from the rejected "rimsky's hosting substrate." That doc may benefit from a vocabulary review; out of scope for this spec.
- **Terraform module for rimsky.** Operators write their own in v1.
- **`init` flags `--with-postgres-store` and `--with-claude-agent`.** The brainstorm sketched these; v1 ships only the minimal scaffold. Users extending to postgres or claude-agent edit the manifest by hand using the operator-guide as reference.
- **`/v1/` URL prefix.** The control-plane v1 spec drafted endpoints with a `/v1/` prefix; the actual implementation uses bare paths. The CLI matches the implementation. If a future control-api revision adds the prefix, the CLI's verb-to-endpoint mapping in §1.6 needs updating in lockstep.

---

## 11. Summary of decisions

| # | Decision | Rationale |
|---|---|---|
| 1 | Dev-loop wrapper (`dev up`) runs operator-supplied infra commands; absent → BYO infra | Operator owns infra; rimsky stays infra-blind |
| 2 | `rimsky_config:` may inline or reference `rimsky.yml`; rimsky processes still load `$RIMSKY_CONFIG` from a path | One-stop-shop dev experience; cloud unchanged |
| 3 | Manifest scope: templates + tags + persistent instances | Bounded; ephemeral runs use `rimsky-cli run` |
| 4 | Owned-only reconcile via `project:` field; `compose:<project>:<name>` instance keys; `compose:<project>:<tag>` template tags | Mirrors Docker's project-label model; no separate ownership table |
| 5 | Apply-once-and-exit; verbs `compose up/down/plan/status` | Matches Terraform/kubectl; control-api owns retry semantics |
| 6 | Per-instance `restart` policy (`never` default, `on_failure`, `always`); manifest is persistent-only; recreate = `DELETE /instances/{id}` then `POST /instances` | Self-healing is opt-in per workload; ephemeral has its own surface; recreate respects the unique-key constraint |
| 7 | Ergonomic top-level + literal API subgroups + compose | Dev-loop UX + scriptability; both shapes documented |
| 8 | Three-tier endpoint config (flag > env > `~/.rimsky/config.yml`); kubectl-style contexts; manifest may pin `context:` | Familiar; scales to multi-deployment workflows |
| 9 | Human-default + `-o json`; exit codes 0/1/2/3 (3 = drift, matches `terraform plan -detailed-exitcode`) | CI-friendly without hostile-to-humans defaults |
| 10 | Go binary at `core/cmd/rimsky-cli/`; hand-rolled or small CLI library | Same module; reuses `core/canonical` for hashing |
| 11 | Distribution: Homebrew tap, install script, GitHub Releases, `go install`, `rimsky/cli` container; embedded reference assets | Standard channels; offline-capable `init` |
| 12 | Project-prefixed tags (`compose:<project>:<tag>`); pass-through params (no templating in v1); single file (no `include:`) | Bounded scope; users can preprocess if they need richer shape |
| 13 | Fail-fast with resumable retry; up-front local validation | Composes with control-api's progress-preserving idempotency |
| 14 | Heavy unit + fake-control-api; one E2E smoke under `test/smoke/cli/`; no testcontainers for control-api | CLI is wire-shaped; fake covers it; smoke catches integration |
| 15 | `compose down` defaults to app-state only; `--infra` flag for infra teardown; `--yes` for destructive ops on non-terminal instances | Avoids accidental volume-drop; CI can pass `--yes` |
| 16 | "Infra" / "operator infrastructure" / "rimsky-aware peer services" — no "substrate" | Rimsky is unaware; vocabulary should reflect that |
