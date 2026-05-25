# Host-agent and proxy: dev-machine services without static deployment

## Summary

A new rimsky-stack service `rimsky-host-agent-proxy` fronts a fleet of dev-machine daemons (`rimsky-host-agent`, bundled into the `rimsky` CLI), letting users run arbitrary local binaries as rimsky services (executors, claim-producers, eventually all kinds) on a per-invocation basis. The user-facing entry point is:

```
rimsky run --template my-workflow \
           --param cwd=. \
           --service codegen=codegen-binary \
           --service fs-claims=/opt/tools/fs-claim-producer
```

The CLI ensures a host-agent daemon is running locally (auto-starts if not), posts the run to control-api with per-instance `service_bindings`, and the user's connected agent — acting through the proxy — spawns the named binaries in the supplied cwd, holds them open for the run-scope's lifetime, and reaps them on close.

The proxy is a normal multi-protocol service from rimsky's perspective: declared in `rimsky.yml`, conformance-testable, dispatched-to like any other hosted executor / claim-producer / publisher / etc. No tunnel-awareness leaks into the supervisor, the dispatch path, the error vocabulary, or graph processing.

## Motivation

A common emerging pattern: a user has deployed an orchestrator workflow as a template and wants to run it against a specific local project — agentic code review, code authoring, build/test orchestration, anything that has to happen on the dev machine where the project files live. Today's path is manual: start the local executor process, set up reachability between docker-compose-resident rimsky and the local process, trigger an instance, tear down the process when done. The setup work overshadows the actual workflow.

The host-agent + proxy turns that into one CLI invocation. The user types `rimsky run --service codegen=./my-binary`, and rimsky just works — the binary runs on their machine, in the folder they pointed at, with no infrastructure setup, and gets cleaned up automatically.

## Architecture

```
   ┌────────────────┐      ┌──────────────────┐      ┌──────────────────┐
   │ rimsky CLI     │──────▶│ control-api      │      │ supervisor       │
   │ (user's box)   │ POST  │                  │      │                  │
   └────────────────┘ /inst └──────────────────┘      └──────────────────┘
           │                                                    │
           │ starts                                             │ Executor.Execute
           ▼                                                    │ (also ClaimProducer.Open, etc.)
   ┌────────────────┐      agent ↔ proxy      ┌──────────────────────────┐
   │ rimsky-agent   │──────────────────────────│ rimsky-host-agent-proxy │
   │ (user's box)   │       outbound dial      │ (rimsky stack)           │
   └────────────────┘                          └──────────────────────────┘
           │ exec()
           ▼
   ┌────────────────┐
   │ user's binary  │
   │ (gRPC server)  │
   └────────────────┘
```

Three new pieces:

- **`rimsky-host-agent`** — long-running daemon on the dev machine. Bundled into the `rimsky` CLI binary; `rimsky run` auto-starts it if not already running. Dials the proxy outbound with the user's api-key. Receives spawn/dispatch/reap frames; forwards local HTTP traffic; reaps processes on signal. No capability or discovery config.
- **`rimsky-host-agent-proxy`** — new rimsky-stack service. Implements every rimsky service protocol on the supervisor-facing side (per the existing multi-protocol composition pattern on `service`). Maintains agent connections on the dev-machine-facing side. Routes dispatched calls to whichever agent is connected for the instance's owner.
- **`rimsky_instances.service_bindings JSONB`** — new column. Populated at instance creation from the `--service` flags. Opaque to the server. Consumed by the proxy at dispatch time.

The supervisor sees the proxy as a normal hosted executor (or claim-producer, or publisher, etc.). The supervisor's code path, dispatch resolution algorithm, terminal handling, and callback receipt are bit-identical to today.

## Components

### The host agent

A Go binary that lives at `cmd/rimsky-host-agent/` and is also linked as the `rimsky agent` subcommand on the main `rimsky` CLI binary. Single-process daemon. Total responsibilities:

1. Load auth state from the active context in `~/.rimsky/config.yml` (the existing `code:control/cli/config.go::Config` shape, extended with an `api_key` field on the `Context` struct — see "CLI surface" below). Env vars `RIMSKY_API_KEY` + `RIMSKY_URL` override the context.
2. Dial the proxy's agent-facing endpoint, open a bidirectional gRPC stream, send `Register{api_key, agent_label}`. The proxy acks.
3. Heartbeat the stream on a configurable cadence (default ~10s).
4. On `Spawn{spawn_id, binding, cwd, run_scope_id, expected_protocols}`: call `exec(binding.path, args)` in `cwd`. Path resolution uses `exec()`'s built-in `$PATH` lookup; absolute, relative, and bare-name paths all work. Inherit the agent process's full environment. Wait for the child to bind its gRPC port (probe with a brief retry loop bounded by a timeout). Run a `Capabilities` handshake on each protocol in `expected_protocols`. Send `SpawnAck{spawn_id, status: ready, capabilities: {<protocol>: <CapabilitiesResponse>}}`. On failure: `SpawnAck{spawn_id, status: failed, error: {class, message}}`.
5. On `DispatchFrame{spawn_id, payload_bytes}`: deserialize the payload as a gRPC message destined for the spawned child's local gRPC server; relay; stream responses back as `DispatchFrame{spawn_id, payload_bytes}` in the other direction.
6. On the spawned process making an HTTP request to the agent's local listener (any URL — async callbacks, attribute writebacks, publisher message POSTs, etc.): wrap as `LocalHttpForward{forward_id, method, path, body, headers}`. Stream through to the proxy. Receive `LocalHttpResponse{forward_id, status, body, headers}` and reply locally to the spawned process.
7. On `Reap{spawn_id}`: SIGTERM the child, SIGKILL after a configurable timeout (default 30s). Ack `Reaped{spawn_id}`.
8. On stream close (clean or unclean): SIGKILL all live children after a brief grace, exit if shutting down or reconnect-with-backoff if not.

The agent has no capability config, no discovery file, no list of known binaries, no persistent state beyond auth. Restart loses all in-flight spawn state; on reconnect, all spawn-ids issued before the restart are dead, and the proxy detects this via reaped connection state.

Optional safety knob: `rimsky agent start --allow-paths <glob>` rejects spawn requests whose `binding.path` does not match. Default is permissive — the trust posture is "anyone with your api-key can spawn anything as you," equivalent to SSH key trust.

### The proxy

A Go binary at `cmd/rimsky-host-agent-proxy/`. Single process. Two protocol surfaces, served on the same gRPC port:

**Supervisor-facing.** Implements every rimsky gRPC service protocol via the multi-protocol composition pattern documented on `service`: distinct handler types per protocol, separately registered on the gRPC server, no shared `CapabilitiesProvider` interface. v1 wires up:

- `Executor` (+ `ExecutorObservability`) — late-bound executor fronting.
- `ClaimProducer` (+ `ClaimProducerObservability`) — late-bound claim-producer fronting.
- `LifecycleSubscriber` — implemented in **consumer role** in v1 (the proxy is itself a subscriber, receiving `OnInstanceCreated` to populate its binding cache and the new `OnRunScopeTerminal` to drive reap). For `OnInstanceCreated` and `OnRunScopeTerminal` the handler actually does work; for the other 5 methods (`OnTemplateRegistered/Deployed/Undeployed/Deregistered`, `OnInstanceTerminated`) the handler returns `LifecycleAck` immediately (no-op acks; the proxy doesn't care about template lifecycle).

The remaining handlers (`Publisher`, `Validation`, `DataProcessing`) ship as registered services that return `UNIMPLEMENTED` until wired in follow-up specs. These are for the future generalization where the proxy fronts dev-machine bindings for those protocols too — distinct from the proxy's LifecycleSubscriber-consumer role above.

`BlobBackend` is intentionally excluded — it is an in-process Go interface (`foundation/persistence/blob.go`), not a wire protocol, so it has no gRPC surface for the proxy to implement.

**Agent-facing.** New gRPC service `proto:host_agent.proto::HostAgent.Connect(stream ClientFrame) returns (stream ServerFrame)` — a single bidi long-lived stream per connected agent. Frame oneof shapes enumerated in "Wire shapes" below.

Single binary, multiple roles. Declared in `rimsky.yml` once per protocol it serves:

```yaml
executors:
  host-agent-proxy:
    transport: grpc
    endpoint: "agent-proxy:9090"
    tls: off
    # The proxy advertises lifecycle_subscriber on its executor entry — this
    # is what gets the proxy dialed as a lifecycle subscriber by
    # control/config/stores.go::dialLifecycleSubscribers (which walks
    # the union of claim_producers: and executors: entries looking for
    # entries whose protocols: list includes lifecycle_subscriber). The
    # proxy IS a lifecycle subscriber in v1 (consumer role: it receives
    # OnInstanceCreated to populate its binding cache and OnRunScopeTerminal
    # to drive reap).
    protocols: [executor, lifecycle_subscriber]
claim_producers:
  host-agent-proxy:
    # claim_producers: entries use the URL-scheme endpoint shape per
    # the existing rimsky-yml convention (the 2026-05-19 Notes entry on
    # concept:rimsky-yml documents this asymmetry with executors:).
    endpoint: "grpc://agent-proxy:9090"
    protocols: [claim_producer]
    write_semantics_allowed: [sync, staged_async, blocking_async, read_only]
# publisher / validation / data_processing entries appear when those handlers are wired up;
# in each case, the proxy's binary already serves those protocols at the same gRPC port — only
# the rimsky.yml declarations are gated on follow-up specs.
```

The `write_semantics_allowed` value on the claim-producer entry is the *envelope* — the proxy itself is transport, so it advertises the full envelope of write-semantics that any spawned binding might realize. Per-claim realized semantics still come from each spawned producer's `Open` response, as today; the proxy doesn't narrow the envelope.

**Proxy in-memory state** (lost on restart; rebuilt as agents reconnect and instance state is consulted):

- `api_key_id → agent_connection` (one entry per connected agent; populated on Register, dropped on disconnect; tiebreaker on duplicate: latest-wins, the older connection is closed gracefully).
- `spawn_id → (agent_connection, run_scope_id, binding_name, capabilities)` (populated when the proxy first issues `Spawn` for a (run_scope, name); dropped when reaped).
- `(run_scope_id, binding_name) → spawn_id` (deduplication index; "one spawn per (binding-name, run-scope)").
- `instance_id → service_bindings` (cached lookup; freshness via lifecycle-subscriber subscription, see "Cache freshness" below).

**Proxy on a supervisor's `Executor.Execute(req)` call:**

1. Extract executor name from the `x-rimsky-service-name` gRPC metadata header (added by the supervisor's executor client when dialing a proxy-routed endpoint).
2. Look up `req.instance_id` to determine `owner_api_key_id` (cached via lifecycle-subscriber, or queried via control-api on cache miss). The owner-api-key field is a new column on `rimsky_instances` — see "Per-instance service bindings" below.
3. Look up `agent_connection` for that api-key. If missing (or if the owner-api-key field is empty — e.g., for instances created under `concept:anonymous-mode`): return one-shot `StreamClose{Error, error_class: "host_agent_not_connected"}` and end the Execute stream.
4. Look up `service_bindings[name]`. If missing: `StreamClose{Error, error_class: "binding_not_found"}`.
5. Compute spawn key `(req.run_scope_id, name)`. If no spawn-id exists yet, read `cwd` from `instance.params.cwd` (well-known key, optional — see "Per-instance service bindings" below) and send `Spawn{spawn_id, binding, cwd, run_scope_id, expected_protocols: [executor]}` through the agent connection. Await `SpawnAck` (bounded by a configurable timeout). On failure: `StreamClose{Error, error_class: "spawn_failed"}`. On success: record the spawn-id.
6. Forward the Execute call: send `DispatchFrame{spawn_id, payload: serialized ExecuteRequest}` through the agent connection. The agent relays to the spawned binary's local gRPC server. Stream responses back as `DispatchFrame` in the other direction. Translate the inner spawned-process's terminal `StreamClose` into the outer Execute's terminal and return.
7. On unexpected agent disconnect mid-Execute: `StreamClose{Error, error_class: "host_agent_disconnected"}` to the supervisor; drop the spawn-id from local state.

Analogous handling for `ClaimProducer.Open` / `Commit` / `Abandon` / `Release`. The protocol-specific handlers all share the spawn-lifecycle machinery; they differ only in which inner RPC is forwarded.

**Cache freshness.** The proxy subscribes to rimsky's lifecycle events via the `LifecycleSubscriber` protocol, receiving `OnInstanceCreated` (extended to carry `service_bindings` and `owner_api_key_id` — see the two lifecycle-proto changes enumerated below) for the binding cache, and the new `OnRunScopeTerminal` event for reap. On cache miss, the proxy falls back to `GET /instances/{id}` (existing endpoint).

**Lifecycle fan-out filter** — for the proxy to receive `OnInstanceCreated`, it must pass the existing `code:control/controlapi/lifecycle.go::lifecyclePeersForSpec` filter, which today fans events only to peers whose names appear in the template's `Stores` or `Executor` references. Late-bound templates reference binding names (`codegen`, `fs-claims`), not the proxy's name (`host-agent-proxy`), so the proxy would be filtered out under today's rule. v1 extends `lifecyclePeersForSpec` (the deps-aware wrapper that today discards its `AppDeps` parameter — the extension consumes it) to additionally include the configured `late_bind_service_proxies.*` peer names whenever the template has a non-empty `late_bind_services` list. This requires plumbing a new `LateBindServiceProxies map[string]string` field on `code:control/controlapi/app.go::AppDeps`, populated by `code:control/config/controlapi.go::StartControlAPI` from the rimsky.yml config. The extension is **scoped to instance- and run-scope-keyed fan-out** (`FanOutInstanceEvent` and the new `FanOutRunScopeEvent`) — not to `FanOutTemplateEvent`. The proxy doesn't care about template events; restricting the extension to instance / run-scope fan-out keeps `rimsky_lifecycle_idempotencies` rows for the proxy bounded by instance count rather than template count.

The supervisor's new `FanOutRunScopeEvent` helper applies the same `lifecyclePeersForSpec`-equivalent filter to run-scope-terminal events. Because `code:.golangci.yml`'s `runtime-purity` depguard rule denies `runtime/` from importing `github.com/fallguyconsulting/rimsky/control`, the supervisor can't call `controlapi.lifecyclePeersForSpec` directly. Wiring follows the existing `ExpectedAttributesSchemaFor` function-pointer pattern (`code:control/config/supervisor.go::SupervisorConfig`): `SupervisorConfig` grows a `LifecyclePeersForSpec func(tplSpec node.TemplateSpec) []string` field. Production wiring at `code:control/cmd/rimsky-supervisor/main.go` (or equivalent entrypoint) supplies a closure that invokes the control-api-layer `lifecyclePeersForSpec` with the `late_bind_service_proxies` config baked in. The supervisor calls the function pointer at run-scope-close time; it has no compile-time dependency on `control/`.

(The `peersReferencedBySpec` inner walker iterates `spec.Nodes` only, but this is sufficient because `code:graph/node/template_validator_graphs.go::canonicalizeGraphs` flattens `Graphs[*].Nodes` into `spec.Nodes` during `node.ValidateTemplate` — the persisted canonical spec carries both forms unified. `graphs:`-form templates pose no problem.)

Two `LifecycleSubscriber` protocol changes ride this spec:

- **`OnInstanceCreatedRequest` gains a `service_bindings` field** carrying the opaque JSONB blob. Today's request body has `{instance_id, template_hash, instance_key, params}`; this spec extends it with `bytes service_bindings` (proto convention for opaque payloads). This is a **proto3-additive change** — existing subscribers receive an empty `service_bindings` field by default and behave unchanged.
- **New method `OnRunScopeTerminal(OnRunScopeTerminalRequest) returns (LifecycleAck)`.** Request body `{run_scope_id, terminal_reason}`. Fires when a run-scope reaches terminal state.

The new `OnRunScopeTerminal` method **breaks the existing `concept:lifecycle-subscriber` invariant that all events fire from control-api**. Run-scope terminal is owned by whichever process closes the scope: control-api closes the instance's main run-scope (in the polling fan-out worker `code:control/controlapi/instance_terminator.go::tick`); the supervisor closes sub-graph and fanout-partition run-scopes (`foundation/persistence/postgres/run_scopes.go::Close`, called from `runtime/subgraph_dispatch.go` and `runtime/auto_terminal_chain.go:158`).

The invariant is explicitly relaxed by this spec: the relaxed invariant becomes "lifecycle-subscriber events fire from the rimsky-side process that owns the state transition's post-commit fan-out (which may be the state-transition tx itself or a polling worker in the same process)" — template / instance / api-key events from control-api as today (note: `OnInstanceTerminated` is already polling-driven via `instance_terminator.tick`, so this isn't a new shape — just a more honest framing); run-scope-terminal events from control-api for main scopes (also polling-driven) and from the supervisor for sub-graph and fanout-partition scopes (synchronous, in-tx). DB-tracked idempotency via `rimsky_lifecycle_idempotencies` is preserved across both firing sites.

The supervisor process today has **no outbound lifecycle-subscriber machinery** — the existing `code:foundation/locks/lifecycle.go::LifecycleRegistry` and `code:control/config/stores.go::dialLifecycleSubscribers` are wired only in control-api (`code:control/config/controlapi.go:187`). v1 grows the supervisor process to dial its own subscriber connections at startup by calling the same `dialLifecycleSubscribers` against rimsky.yml (which walks the union of `claim_producers:` and `executors:` entries whose `protocols:` list includes `lifecycle_subscriber`; no new top-level YAML block). The supervisor maintains its own `*locks.LifecycleRegistry`, and exposes a `FanOutRunScopeEvent(ctx, tplSpec, request)` helper analogous to control-api's existing `FanOutInstanceEvent`. The supervisor's run-scope close sites (`runtime/subgraph_dispatch.go`, `runtime/auto_terminal_chain.go:158`) call the helper synchronously after the DB commit, mirroring how control-api fires `OnInstanceCreated` synchronously after instance commit.

`FanOutRunScopeEvent` needs the template spec to apply the filter (matching the existing `FanOutInstanceEvent` signature). At the supervisor's two sub-graph / fanout-partition close sites, neither `tplSpec` nor `template_hash` is in surrounding scope — the available state is the closing `RunScopeRow` (which carries `InstanceID` only). v1 introduces a two-lookup chain at the close caller: `args.Persist.Instances().Get(ctx, instanceID, tx)` to extract `TemplateHash`, then `args.Persist.Templates().GetByHash(ctx, hash, tx)` to load the spec. This matches the existing pattern at `code:runtime/runner_locks.go:171-175`. Both lookups happen inside the same transaction the close commits in.

At the control-api main-scope close site, the instance row is already in scope (instance termination is owned by control-api), so the chain reduces to one `Templates().GetByHash` lookup.

`rimsky_lifecycle_idempotencies` gains a third `scope_kind` value: `LifecycleIdempotencyScopeRunScope` (alongside the existing `Template` and `Instance` values). The new `OnRunScopeTerminal` event uses it with a new state value `LifecycleIdempotencyStateRunScopeTerminal` for the per-(peer, run_scope) idempotency record.

### Per-instance service bindings

New column `rimsky_instances.service_bindings JSONB`. Populated at instance creation from the `POST /instances {..., service_bindings: {...}}` request body. Opaque to control-api: the server stores it verbatim, exposes it via `GET /instances/{id}`, and never inspects it. The proxy is the sole consumer.

v1 binding shape (minimal; opaque container leaves room to extend):

```json
{
  "codegen":      {"path": "codegen-binary"},
  "fs-claims":    {"path": "/opt/tools/fs-claim-producer"},
  "file-watcher": {"path": "./tools/file-watcher"}
}
```

Per-binding extension fields (env, args, per-binding cwd, per-binding timeouts) are additive and backward-compatible: the proxy ignores unknown fields, and missing fields default sensibly (env: inherit from agent process; args: none; cwd: from instance-level `cwd` param; spawn-readiness timeout: a configurable default).

`cwd` itself is not part of the binding — it's an instance-level parameter (`--param cwd=.`), supplied through the existing `params` mechanism.

**How `cwd` reaches the proxy at spawn-time.** Since `cwd` is a process-level concern (the spawned binary's working directory, set once by `exec()`) rather than a per-call attribute, the proxy reads it from `instance.params.cwd` directly (the well-known key) at first-Spawn time for a given `(run_scope_id, binding_name)`. The proxy already has the instance state cached via its `OnInstanceCreated` subscription (or fetched via `GET /instances/{id}` on cache miss); the `params` blob is part of that payload. If `params.cwd` is absent, the proxy spawns the child without an explicit cwd (it inherits the agent's working directory). The well-known key (`cwd`) is a convention the CLI's `--param cwd=.` sugar populates; templates that don't supply `cwd` work fine — they just inherit the agent's directory.

Template authors can also reference `cwd` from substitution syntax for their own attribute purposes (e.g., `attributes: {workspace_root: "{{params.cwd}}"}`), but that's orthogonal to the proxy's spawn-time read. The proxy reads the raw param; the supervisor's substitution machinery produces resolved attributes independently.

**Owner identity (new column `created_by_api_key_id`).** For the proxy to route a dispatch to the right user's agent, it needs to know which api-key owns the instance — and today `rimsky_instances` doesn't track this. v1 adds a nullable `created_by_api_key_id UUID` column on `rimsky_instances`, populated at instance creation from the request's authenticated api-key context. Source: destructure via `ident, _ := IdentityFromContextOK(ctx)`; the `*shared.UUID`-typed `ident.KeyID` field is the source (natively nullable; matches the existing pattern at `code:control/controlapi/auth_handlers.go:118-129`). Do not source from `requestingKeyID(ctx)` — that helper returns the literal string `"anonymous"` for anonymous-mode and can't be parsed into UUID. The column is FK to `rimsky_api_keys(id)`. It's plumbed through `createInstanceRequest`, `InstanceCreateInput`, `InstanceRow`, exposed via `GET /instances/{id}`, and carried on `OnInstanceCreatedRequest` (extending the proto with `string owner_api_key_id` (empty string for null/anonymous; matches the existing `string instance_key` convention)) so the proxy's binding-cache entry pairs `service_bindings` with the owner-id needed for routing. For instances created under `concept:anonymous-mode` (no real api-key, `Identity.KeyID == nil`), the column is null — the proxy treats this as "no agent connected" and returns `host_agent_not_connected` for any dispatch that needs a binding lookup. (Anonymous-mode users therefore cannot use late-bound services in v1 — flagged as a tension below.)

### CLI surface

The CLI changes are additive to today's `cmd:rimsky` (per `concept:rimsky`). Existing verbs and shapes survive; the spec adds a new `agent` subcommand group, a new `auth login` verb, and a new flag shape on `rimsky run`.

```
# new agent subcommand group
rimsky agent start [--allow-paths <glob>] [--listen <addr>]  # daemonize the host-agent
rimsky agent status                        # report connection state
rimsky agent stop                          # graceful shutdown

# new auth verb, sibling to existing `auth {init, create-key, list, show, revoke, rotate, status}`
rimsky auth login                          # interactive; writes ~/.rimsky/config.yml (under the active context)

# rimsky run gains two new optional flags (additive; existing shape preserved)
rimsky run [<file> | --template <name>] \
           [--params <json> | --param k=v ...] \
           [--service <name>=<path>]...    # bindings; alias-resolved client-side if applicable
           [--instance-key <key>] [--tag <tag>] [--keep|--no-keep]
```

The existing `rimsky run <file>` shape (register + deploy + create from a template file) is unchanged. The new shape `rimsky run --template <name>` addresses an already-registered template by name without re-registering. The two are mutually exclusive (one or the other; the CLI errors if both are supplied). Existing `--params <json>` is preserved; the new `--param k=v` (singular, repeatable) is sugar for one key in the same params blob and may be mixed with `--params` (with later flags overriding earlier).

`--service` and the agent-daemon auto-start are entirely new.

How `rimsky run` handles the new flags:

1. If `--service` is present and the agent daemon is not already running locally, start it (`rimsky agent start` equivalent) and wait for tunnel-up before submitting.
2. Resolve each `--service <name>=<path>` value against optional client-side aliases (`~/.rimsky/aliases.yml` global, `.rimsky/aliases.yml` project-local; user can type `--service codegen` if an alias exists).
3. Include `service_bindings: {<name>: {path: <resolved>}, ...}` in the `POST /instances` body.
4. Stream progress events back to the terminal as today.

Server never sees aliases; only resolved values. Agent does path resolution at `exec()` time; the CLI's job for `--service` values is just to pass through whatever the user typed (or whatever the alias resolved to).

`rimsky auth login` is a sibling to the existing `rimsky auth init` and `rimsky auth create-key`. It does not replace them; it's an interactive convenience verb for the dev-machine use case where the user is logging into an existing rimsky deployment they didn't bootstrap themselves. It prompts for the rimsky URL and api-key (or interactively walks through `create-key` if they have admin auth), then writes the api-key to the active context in `~/.rimsky/config.yml`. The `init` verb retains its existing role (bootstrap a new deployment from anonymous-mode).

The `Context` struct in `code:control/cli/config.go` is extended with an `api_key` field (alongside the existing `endpoint`). One file, two responsibilities (endpoint + secret per context). Login writes the api-key in-place under the current context; `auth init` continues to do what it does today.

The agent's listener (`--listen <addr>`) defaults to an OS-assigned ephemeral port on `127.0.0.1` if not supplied. The bound address is what the agent reports to the proxy in the `local_callback_base_url` field of its `Register` frame; the URL is constructed as `http://<bound-addr>` (no path — it's a base URL). Operators who need the agent to bind elsewhere (e.g., a privileged port for a known firewall hole, or a non-loopback interface) supply `--listen <addr>`. Loopback-only by default avoids exposing the agent's spawn-callback surface to other hosts on the dev machine's network.

### Late-bind opt-in

New top-level template field:

```yaml
late_bind_services: [codegen, fs-claims, file-watcher]
```

Names in the list bypass registration-time existence checks (against the `discovery-cache` populated by the static `rimsky.yml` services) and registration-time schema validation (the executor's `expected_attributes_schema` cross-check). Names not in the list retain today's strict behavior: the resolver must know about them at template registration; their schemas validate against cached `Capabilities`.

Mechanically, the bypass plumbs in at `code:control/controlapi/templates.go::validatorHooksFor`: the registration validator consults `spec.LateBindServices` first and short-circuits the existing `ExecutorDeclared(name) bool` and `ExecutorExpectedAttributesSchema(name) ([]byte, bool)` hooks for names in the list (returns "declared, no schema cross-check needed"). Names absent from the list go through the existing hooks untouched.

At dispatch:

- Late-bound names route through the configured proxy (see "Dispatch resolution" below).
- The spawned binary's `Capabilities` handshake provides the actual schema; the proxy validates resolved attribute values against it; mismatch produces a `contract_mismatch` error.

Default is empty list → strict registration matches today's behavior. Opt-in laxness, not silent.

The list generalizes across all service kinds (executor, claim-producer, publisher, lifecycle-subscriber, blob-backend, validation, data-processing). For v1 implementation, only executor and claim-producer bindings have a run-time supply mechanism (via `service_bindings`); other late-bind kinds still need an eventual rimsky.yml registration before dispatch — they just don't have to exist at template registration time.

## Wire shapes

### `proto:host_agent.proto`

New proto file. Single service. Internal to the proxy; not part of the public agent-facing protocol set.

```protobuf
syntax = "proto3";

package rimsky.v1;

option go_package = "github.com/fallguyconsulting/rimsky/protocols/proto/v1/gen;genv1";

service HostAgent {
  rpc Connect(stream ClientFrame) returns (stream ServerFrame);
}

message ClientFrame {
  oneof body {
    Register         register          = 1;
    Heartbeat        heartbeat         = 2;
    SpawnAck         spawn_ack         = 3;
    Reaped           reaped            = 4;
    DispatchFrame    dispatch_frame    = 5;  // from spawned process → supervisor
    LocalHttpForward http_forward      = 6;  // from spawned process → rimsky-side
  }
}

message ServerFrame {
  oneof body {
    RegisterAck       register_ack       = 1;
    HeartbeatAck      heartbeat_ack      = 2;
    Spawn             spawn              = 3;
    Reap              reap               = 4;
    DispatchFrame     dispatch_frame     = 5;  // from supervisor → spawned process
    LocalHttpResponse http_response      = 6;  // from rimsky-side → spawned process
  }
}

message Register {
  string api_key   = 1;
  string agent_label = 2;       // e.g., "hostname-pid"; for multi-agent disambiguation
  string agent_version = 3;
  // Base URL the agent's local HTTP listener serves for spawned processes.
  // The proxy uses this to rewrite callback_url and other rimsky-side URLs
  // before tunneling them into the spawned process. See "Callback URL
  // rewriting" in the spec text.
  string local_callback_base_url = 4;
}

message RegisterAck {
  string proxy_version = 1;
  // If a prior agent for this api_key was connected, this ack carries
  // a notice that the prior connection has been displaced.
  bool displaced_prior = 2;
}

message Heartbeat       { int64 sent_at_unix_ms = 1; }
message HeartbeatAck    { int64 received_at_unix_ms = 1; }

message Spawn {
  string spawn_id           = 1;
  Binding binding           = 2;
  string cwd                = 3;
  string run_scope_id       = 4;
  repeated string expected_protocols = 5;  // e.g., ["executor"], ["claim_producer"], or both
  int32  ready_timeout_seconds = 6;
}

message Binding {
  string path               = 1;
  // future-extensible: args, env, per-binding cwd, etc.
}

message SpawnAck {
  string spawn_id           = 1;
  enum SpawnStatus {
    SPAWN_STATUS_UNSPECIFIED = 0;
    SPAWN_STATUS_READY       = 1;
    SPAWN_STATUS_FAILED      = 2;
  }
  SpawnStatus status        = 2;
  // On READY: per-protocol Capabilities responses, keyed by protocol name.
  map<string, bytes> capabilities = 3;  // bytes are the serialized CapabilitiesResponse for each protocol
  Error  error              = 4;        // populated when status = SPAWN_STATUS_FAILED
}

message Reap            { string spawn_id = 1; int32 sigterm_grace_seconds = 2; }
message Reaped          { string spawn_id = 1; bool clean = 2; Error error = 3; }

message DispatchFrame {
  string spawn_id           = 1;
  string protocol           = 2;  // "executor", "claim_producer", etc. — used at dispatch start
  bytes  payload            = 3;  // serialized gRPC frame for the named protocol
  // Stream multiplexing: a single spawn_id can host concurrent dispatch streams
  // (e.g., concurrent ClaimProducer.Open calls). Each stream carries a stream_id.
  string stream_id          = 4;
  enum DispatchFrameKind {
    DISPATCH_FRAME_KIND_UNSPECIFIED = 0;
    DISPATCH_FRAME_KIND_DATA        = 1;
    DISPATCH_FRAME_KIND_HALF_CLOSE  = 2;
    DISPATCH_FRAME_KIND_CANCEL      = 3;
  }
  DispatchFrameKind kind    = 5;
}

message LocalHttpForward {
  string forward_id         = 1;
  string method             = 2;
  string url                = 3;       // full URL as the spawned process saw it
  bytes  body               = 4;
  map<string, string> headers = 5;
  string spawn_id           = 6;       // for routing back through the proxy if needed
}

message LocalHttpResponse {
  string forward_id         = 1;
  int32  status             = 2;
  bytes  body               = 3;
  map<string, string> headers = 4;
}

message Error {
  string class              = 1;       // matches rimsky's error-class vocabulary
  string message            = 2;
}
```

The `DispatchFrame` is multiplexed across stream-ids inside a single spawn-id so that, e.g., concurrent `ClaimProducer.Open` calls against the same spawned binary can fan in and out without head-of-line blocking. `Kind` distinguishes data frames from half-close and cancellation signals (mirroring gRPC stream semantics).

### Supervisor-facing protocol

Unchanged. The proxy implements `Executor`, `ClaimProducer`, etc. as the existing protocols define them. Supervisors and other rimsky processes call them via the existing client paths: the executor client at `runtime/executor/{client.go,client_http.go}`; the claim-producer client at `runtime/remote/client.go`; and the other peer-service clients at `runtime/remote/{lifecycle_client.go, publisher_client.go, validation_client.go, data_processing_client.go}`.

The only addition on the supervisor side: every outbound call to a peer service carries an `x-rimsky-service-name` gRPC metadata header with the resolved-for service name. Hosted (non-proxy) services ignore the header; the proxy reads it to route to the right binding. See "Per-call service-name header" below.

### Per-call service-name header

Header injection is **per-call**, not per-endpoint-cache-entry. This avoids the `ClientPool.GetOrCreate` cache collision that would occur if a single proxy endpoint had to serve many different binding names (the existing pool in `code:runtime/executor/client.go::ClientPool.GetOrCreate` keys by `Transport://URL`; bolting per-binding metadata onto the cached client would either pollute the cache or require one connection per binding name).

Mechanism: the supervisor's gRPC dial config (for every peer service it dials) installs a small client-side interceptor — gRPC's `UnaryClientInterceptor` and `StreamClientInterceptor` — that reads the resolved service name from the per-call context and attaches it as the `x-rimsky-service-name` outgoing-metadata header. The supervisor's existing dispatch code already knows the service name (it's the `executor_name` column on the dispatch row, or the `required_stores` entry being acquired). One line at each dispatch site stamps the name onto the context before the call:

```go
ctx = metadata.AppendToOutgoingContext(ctx, "x-rimsky-service-name", serviceName)
```

The interceptor is a no-op for hosted (non-proxy) services because they don't read the header; the proxy reads it and routes. **`Endpoint` is unchanged** — no `Metadata` field, no per-endpoint state, no cache key change. The Resolver's job collapses to "return the endpoint to dial" (the existing job); the dispatch path's job grows by one line of context-stamping.

For the **claim-producer side**, the same mechanism applies even though the claim-producer client structure differs. `code:runtime/remote/dial.go::Dial(ctx, name, endpoint string)` returns a gRPC conn that's wrapped in a typed `ClaimProducer` client; the dial-time `grpc.NewClient` call grows the same interceptor. Each `Open` / `Commit` / `Abandon` / `Release` call site in the supervisor's claim-acquisition code already has the producer's name available; it stamps the header onto the context before the call. The claim-producer registry in `code:foundation/locks/registry.go` doesn't change shape; the call sites grow one `metadata.AppendToOutgoingContext` line each.

For other peer-service clients (`runtime/remote/{lifecycle_client.go, publisher_client.go, validation_client.go, data_processing_client.go}`), the same interceptor pattern applies at their respective dial sites. v1 only requires it on the executor and claim-producer paths because those are the only ones the proxy fronts in v1; the interceptor can be added everywhere or just on the v1 paths.

For the server-streaming `Executor.Execute` RPC specifically: the gRPC client-side stream interceptor fires once at stream creation, and the appended metadata travels in the initial HTTP/2 headers. Subsequent stream frames inherit the same call context, so no per-frame handling is required — the standard streaming semantics work transparently.

## Dispatch resolution

Today's `Resolver.Resolve(name) → Endpoint` is extended to accept run context:

```go
type Resolver interface {
    Resolve(name string, ctx DispatchContext) (Endpoint, bool)
    AcceptedNames() []string
}

// DispatchContext carries instance/run-scope identity into resolver
// lookups. Named DispatchContext (rather than ResolveContext) to avoid
// the symbol clash with code:graph/attribute/substitution.go::ResolveContext,
// which is rimsky's existing substitution context.
type DispatchContext struct {
    InstanceID   string  // empty for non-instance-scoped resolution
    RunScopeID   string  // ditto
}
```

`StaticResolver` ignores `DispatchContext` (backwards compatible). A new `LateBindResolver` is chained after `StaticResolver`:

1. Static lookup: hit → return endpoint.
2. Static miss + DispatchContext is empty → return false.
3. Static miss + DispatchContext present → check if `instance.service_bindings[name]` exists AND `rimsky.yml` declares `late_bind_service_proxies.<protocol>: <proxy_name>` for the protocol being resolved AND that proxy name resolves statically. If yes: return the proxy's endpoint. Otherwise: return false.

`Endpoint` is unchanged. The dispatch path attaches the original service name to the call context via the client-side interceptor described in "Per-call service-name header" above; the proxy reads the header to route. The resolver's job is purely "endpoint to dial."

### Claim-producer-side late-binding

Claim-producer resolution doesn't go through `runtime/executor/resolver.go::Resolver` — it goes through `code:foundation/locks/registry.go::Registry.Get(name) (ClaimProducer, bool)`. This is a separate code path with its own structure. The late-binding mechanism for claim-producers parallels the executor one but lands on this different surface.

To avoid a wrapper-type ripple across the five+ `*locks.Registry` field sites (`code:runtime/runner.go:122`, `runner.go:298`, `supervisor.go:96`, `sweep_parked.go:48`, plus assignment sites at `callback.go:512`, `runner_dispatch.go:282`, `sweep_parked.go:329`), v1 **adds the context-aware lookup as a new method directly on `*locks.Registry`** rather than introducing a wrapper interface:

```go
// On *locks.Registry, alongside the existing Get:
func (r *Registry) GetWithContext(name string, ctx DispatchContext) (ClaimProducer, bool)
```

The `Registry` struct grows two new fields (`lookupInstanceBindings func(...)`, `lateBindServiceProxies map[string]string`), both optional. To avoid breaking the existing zero-arg `NewRegistry()` constructor (used at every test-fixture site and the production startup paths in `cmd/rimsky-supervisor` and `cmd/rimsky-entrypoint`), `NewRegistry` adopts the functional-options pattern: `NewRegistry(opts ...Option)` with options `WithLookupInstanceBindings(fn)` and `WithLateBindServiceProxies(m)`. Existing zero-arg call sites keep compiling; only the production supervisor-startup site supplies the options. The struct fields stay private; only the options touch them.

Semantics of `GetWithContext`:

1. Underlying static `Get(name)` hit → return the static producer.
2. Static miss + ctx empty → return false.
3. Static miss + ctx present + `lookupInstanceBindings == nil` → return false (registry was constructed without late-bind support).
4. Static miss + ctx present + hook set → consult `lookupInstanceBindings(ctx, instanceID)` for `service_bindings[name]` AND check `lateBindServiceProxies["claim_producer"]` resolves to a statically-registered proxy via the underlying `Get`. If yes: return the underlying static proxy producer (a normal gRPC-dialed `ClaimProducer` for the proxy peer; the gRPC client-side interceptor described in "Per-call service-name header" attaches the `x-rimsky-service-name: <name>` header at call time when the caller stamps it). Otherwise: return false.

The returned `ClaimProducer` is just the static proxy producer — no per-binding wrapper, no per-binding connection. The header-stamping happens at the runtime call sites (Open/Commit/Abandon/Release) as part of the per-call interceptor pattern; the registry's job is only resolution.

**Call sites that need late-binding context.** The dispatch-time `Registry.Get` callers don't carry `InstanceID` today; `persistence.Candidate` doesn't include it either. The natural source is `nd.InstanceID` on the `NodeRunRow` already fetched at `code:runtime/runner_acquire.go::tryAcquire` (around line 424, via `args.Persist.Nodes().Get(ctx, cand.NodeID, tx)`). v1 threads it down:

- `acquireOneLock` (`code:runtime/runner_acquire_named_locks.go:59` is the call site into `acquireClaim`) gains an `instanceID shared.UUID` parameter, sourced from `nd.InstanceID` at `tryAcquire`. Both `acquireOneLock` and `acquireClaim` signatures grow the parameter.
- `acquireFanOutIfDeclared` (`code:runtime/runner_acquire_helpers.go:36`) gains an `instanceID shared.UUID` parameter, since today the function takes `(ctx, args, tx, *acquisition, persistence.Candidate, *node.TemplateNodeDef, []AcquiredLock, time.Duration)` and does not hold `NodeRunRow` in scope. The caller `tryAcquire` (at `runner_acquire.go:553`) passes `nd.InstanceID` (already fetched at line 424) down.
- `AcquireSubClaimsInput` (`code:runtime/runner_subclaim.go`) gains an `InstanceID shared.UUID` field. Production caller `code:runtime/runner_acquire_helpers.go:89` (inside `acquireFanOutIfDeclared`) populates it from the new `instanceID` parameter just threaded in. Test callers (`runner_subclaim_test.go:49,72,301`) supply zero-UUID for static-only test fixtures, which `GetWithContext` cleanly returns false on at step (2) of its semantics.
- Both call sites switch from `Registry.Get(name)` to `Registry.GetWithContext(name, DispatchContext{InstanceID: instanceID, RunScopeID: runScopeID})`.

Static-only producer registrations remain functionally identical. Call sites without an instance context (e.g., startup-time conformance checks) continue to use the bare `Registry.Get(name)`.

### Admit-list extension

Today the supervisor's claim-query (in `foundation/persistence/postgres/queue.go::SelectCandidates`) filters with `d.executor_name = ANY($2::text[])` (where `$2` is the supervisor's `accepted_executors` array) and `d.required_stores <@ $1::text[]` (where `$1` is `accepted_stores`). Tunneled service names appear only in the dispatch row's `instance.service_bindings`, not in any supervisor's static accept list, so without an extension those rows would never be claimed.

Extension to `SelectCandidates` (pseudocode, written against the actual column names):

```
WHERE (
        d.executor_name = ANY($2::text[])  -- today
        OR (
              i.service_bindings ? d.executor_name
              AND <late_bind_service_proxies.executor> = ANY($2::text[])
        )
      )
  AND (
        d.required_stores <@ $1::text[]    -- today
        OR (
              <every name in d.required_stores is in i.service_bindings>
              AND <late_bind_service_proxies.claim_producer> = ANY($1::text[])
        )
      )
```

The "late_bind_service_proxies.executor" / ".claim_producer" lookups are static rimsky.yml config read at supervisor startup, so the actual SQL parameters can be precomputed. Operators retain the ability to specialize supervisors with static accept lists; late-bound names additionally admit if a proxy serving the relevant protocol is in the supervisor's accept list.

The existing NULL-executor clause (`d.executor_name IS NULL AND COALESCE(array_length(d.required_stores, 1), 0) > 0` at `queue.go:225`, for claim-producer-only dispatches) is unaffected: the new executor-side OR-term evaluates to false when `d.executor_name IS NULL` (`i.service_bindings ? NULL` is NULL in Postgres, treated as false in WHERE), so the existing claim-producer-only admit path stays correct. The claim-producer-side OR-clause covers late-bound claim-producer-only dispatches independently.

## Spawn lifecycle and reap

**Birth.** Lazy. The proxy holds no spawned processes alive until a supervisor's dispatched call arrives. On first dispatch for `(run_scope_id, name)`, the proxy issues `Spawn` and awaits `SpawnAck`. Subsequent dispatches for the same `(run_scope_id, name)` reuse the spawn-id.

**Lifetime.** Per run-scope. One spawned process per `(run_scope_id, binding_name)` serves all dispatches within that run-scope. If the same template's same node fires multiple times within a run-scope (e.g., via the existing fan-out / cascade machinery), the same spawned process handles each. Different binding names within the same run-scope produce different spawn-ids (and thus different child processes, possibly on the same binary if the user supplied the same path under two names).

A single binding can serve multiple protocols when its binary advertises multiple Capabilities (the existing `stores/postgres/` pattern). In that case the proxy issues one Spawn with `expected_protocols: [executor, claim_producer]`, the agent runs both handshakes, and subsequent dispatches across either protocol route to the same spawn-id.

**Reap.** Pushed from the proxy on run-scope termination:

1. Proxy subscribes to rimsky's `LifecycleSubscriber` events. The new `OnRunScopeTerminal` method fires when a run-scope reaches terminal state.
2. On receipt, the proxy looks up all spawn-ids associated with `run_scope_id` (via its `(run_scope_id, name) → spawn_id` map).
3. For each, send `Reap{spawn_id, sigterm_grace_seconds}` through the corresponding agent connection.
4. Agent SIGTERMs the child, SIGKILLs after the grace period, acks `Reaped`.
5. Proxy drops the spawn-id from local state.

**Firing sites for `OnRunScopeTerminal`.** Today's rimsky has two run-scope close call sites (`runtime/subgraph_dispatch.go` and `runtime/auto_terminal_chain.go:158`), both in supervisor land, both closing sub-graph or fanout-partition run-scopes only. **Main run-scopes (one per instance, allocated at instance creation) are not closed today.** For the common case — a top-level `rimsky run --service codegen=...` against a single-graph workflow with no sub-graphs and no fan-out — every binding's spawn would live on the instance's main run-scope, and `OnRunScopeTerminal` would never fire. Spawns would only be reaped on agent disconnect.

v1 closes this gap by adding a third firing site: **alongside the existing `FanOutInstanceEvent(EventInstanceTerminated)` call inside `code:control/controlapi/instance_terminator.go::tick`'s polling loop**, the same loop closes the instance's main RunScope and fires `FanOutRunScopeEvent(OnRunScopeTerminal)` *before* the `OnInstanceTerminated` fan-out. The same insertion pattern lands at the explicit-DELETE path at `code:control/controlapi/instances.go` (around line 614).

Natural-termination is marked in the frame-completion transaction at `code:graph/frame/engine.go::transitionFrameEnd` (which calls `MarkInstanceTerminatedIfDone`); the `instance_terminator.tick` polling worker detects the terminated row one tick later (default ~5s lag) and fires the lifecycle event. The main-scope close + `OnRunScopeTerminal` rides this same polling-driven fan-out — there's a sub-tick lag between the row's terminal mark and the reap signal reaching the proxy, acceptable for v1.

Control-api gets its own `FanOutRunScopeEvent(ctx, tplSpec, request)` helper analogous to its existing `FanOutInstanceEvent` and to the supervisor's parallel helper described below. Both control-api and the supervisor become `OnRunScopeTerminal` firers. Each layer has its own `*locks.LifecycleRegistry` and its own `FanOutRunScopeEvent` helper. Supervisor's helper takes `(ctx, Persist, LifecycleSubs, LifecyclePeersForSpec, tplSpec, req)` — no shared deps type with control-api's helper (which uses `AppDeps`). The two helpers are small; they share no code in v1.

The two layers close disjoint kinds of run-scopes: control-api closes only main scopes (instance-bound), supervisor closes only sub-graph and fanout-partition scopes (runtime-bound). No double-fire risk.

**Crash recovery.**

- *Spawned process crashes.* The inner dispatch stream errors; the proxy translates into a `StreamClose{Error, error_class: "executor_crashed"}` (or the protocol-equivalent terminal) and returns to the supervisor. Existing terminal-resolution applies. The proxy drops the spawn-id; next dispatch for the same `(run_scope, name)` triggers a fresh spawn.
- *Agent disconnects.* The proxy marks all spawn-ids on that connection as dead. In-flight dispatches return `host_agent_disconnected` errors. The agent's local children are orphaned briefly until the agent's reconnect logic kicks in or its own SIGKILL-on-disconnect-timeout fires.
- *Proxy crashes.* All agent connections drop. Agents reconnect with backoff. In-flight dispatches errored out before the crash; new dispatches work once an agent is reconnected. Spawned processes on dev machines that were orphaned during the gap are SIGKILLed by the agent's reconnect-recovery logic.
- *Supervisor crashes.* Existing `orphan-reaper` reclaims the supervisor's dispatch rows; another supervisor picks them up. The proxy doesn't notice; it just sees subsequent calls from a different supervisor. Spawned process survives; routes to whichever supervisor currently holds the row.

## Multi-process behavior

The proxy is a normal rimsky-stack service. Single-process unified deployments (`rimsky-entrypoint --role unified`) and multi-process deployments behave identically:

- The supervisor dials the proxy at its `rimsky.yml`-declared endpoint, exactly as it dials any other hosted executor.
- Callbacks from spawned processes flow: spawned → agent local listener → agent → proxy → supervisor's existing `/v1/callback/{ack_id}` route → supervisor's existing callback handler. The proxy → supervisor hop is a normal executor → supervisor callback (proxy is the "executor" in the supervisor's eyes).

### Callback URL rewriting

For the spawned process to POST its async callback to the agent's local listener rather than dialing the supervisor directly (which the spawned process can't do — it's behind NAT, no route to the rimsky stack), the proxy **rewrites the `callback_url` field in `ExecuteRequest` before serializing the payload into `DispatchFrame`**. Specifically:

- The supervisor synthesizes `callback_url` as today: `http://<supervisor-advertised-host>:<port>/v1/callback/{ack_id}`. The proxy receives this in the inbound `Executor.Execute` call.
- Before forwarding the request to the agent via `DispatchFrame`, the proxy substitutes the host:port with the agent's locally-reachable address. The agent's local address is reported by the agent in its `Register` frame (a new `local_callback_base_url` field), e.g., `http://127.0.0.1:<agent-port>`. The rewritten `callback_url` becomes `http://127.0.0.1:<agent-port>/v1/callback/{ack_id}` — same path; substituted host.
- The spawned process eventually POSTs to that URL. The agent's local HTTP listener receives it, wraps as `LocalHttpForward{forward_id, method, url, body, headers, spawn_id}`, sends through the tunnel.
- The proxy receives `LocalHttpForward`, then POSTs the body to the original (un-rewritten) supervisor `callback_url` via HTTP. The supervisor's chi handler processes it as a normal executor callback.

This rewrite is the **only URL the proxy touches**. Other fields in `ExecuteRequest` (attribute values, etc.) ride through opaque. Other HTTP traffic from the spawned process (anything not initiated via a rimsky-side URL substitution) is the spawned process's concern; the agent does not transparently proxy arbitrary localhost traffic. The agent's local HTTP listener is bound to a specific port (reported in `Register`) and only serves URLs the rimsky stack has injected via the rewrite mechanism.

(For protocols beyond executor that need their own URL substitutions — e.g., a future tunneled publisher pushing messages to control-api's `/instances/{id}/messages` endpoint — the proxy applies analogous rewrites. The principle is: the proxy is the URL-rewriting boundary for any rimsky URL handed to a spawned process.)

### Callback-hostname-split scope

The `callback-hostname-split` tension stays open and applies to the proxy → supervisor hop exactly as it applies to any other executor → supervisor hop. No new requirement, no new relief. The proxy inherits the existing constraint cleanly. The agent's local listener address introduces a third hostname class in the broader system (agent-local, supervisor-advertised, agent-dialed proxy URL), but the agent's local address is implicit (loopback by default; agents dial outbound, so they don't have an "advertised" address visible to rimsky beyond what they report on Register).

**Internal-RPC scope.** The supervisor's outbound `OnRunScopeTerminal` call to the proxy is a new supervisor-process → host-agent-proxy RPC (introduced by this spec), but it uses the existing `LifecycleSubscriber` protocol — no new wire surface. Supervisor → control-api / scheduler / supervisor coordination remains DB-only as today; this spec doesn't add internal RPCs between supervisor and other rimsky-stack processes.

## Error handling

All proxy-side failures surface as terminal events on the supervisor-facing protocol. The shape differs per protocol:

- **Executor-side** failures surface as `StreamClose{Error, error_class: ..., message: ...}` outcomes on the streaming `Executor.Execute` RPC. This is the existing executor-Error vocabulary; new error_class values listed below slot into it directly.
- **Claim-producer-side** failures surface as **gRPC error status codes**. `proto:claim_producer.proto::Unavailable` is intentionally empty (`message Unavailable {}` — "Producer-side faults flow as gRPC error status codes, not as an Unavailable response"). The proxy returns a gRPC `Internal` or `FailedPrecondition` status with the error_class carried in a status detail (`google.rpc.ErrorInfo` with `reason: <error_class>` is the standard mapping). The supervisor's claim-producer client at `code:runtime/remote/client.go` translates gRPC status → an internal error struct that the existing `error-policy` `error_types:` chain consumes — v1 extends the translator to read the `reason` field and pass it through as `error_class`.

The existing `error-policy` `error_types:` chain handles both shapes; the only wire-level difference is where the error_class string lives (StreamClose payload vs. gRPC status detail). No new policy mechanism, no new acquisition error classes.

New `error_class` values introduced (all on the executor-Error / claim-producer-error vocabulary, not new synthetic supervisor-side classes):

- `host_agent_not_connected` — instance has bindings but the owner's agent isn't connected. Default operator policy: retry with backoff (the user might just be starting their agent).
- `binding_not_found` — proxy received a dispatch for a name not in `service_bindings`. Configuration error. Default give-up.
- `spawn_failed` — agent acked failure (binary not found, exec error, capabilities handshake timed out). Default give-up.
- `host_agent_disconnected` — the agent connection dropped mid-dispatch. Default give-up (the in-flight work is gone); operator can override to retry for idempotent workflows.
- `contract_mismatch` — at dispatch, the spawned binary's Capabilities revealed a schema incompatible with the resolved attribute values. The cost of late-binding's deferred validation.
- `executor_crashed` — for the executor protocol specifically; the spawned process's gRPC server died mid-Execute. The claim-producer equivalent uses the existing claim-producer error vocabulary.

These names are new entries in the executor-Error / claim-producer-error class spaces. They do not add new policy mechanisms or new pre-dispatch acquisition failure classes — they ride the existing `error_types:` chain.

## Failure modes

| Situation | Behavior |
|---|---|
| Proxy not declared in rimsky.yml | Late-bind names don't resolve. Dispatch rows sit unclaimed (existing rimsky behavior for unknown executors; covered by a follow-up tension, see Design changes). |
| Proxy running, agent not running | `Execute` returns `StreamClose{Error, host_agent_not_connected}`. Operator policy retries (default with backoff) or gives up. |
| Agent connects mid-instance after a `host_agent_not_connected` retry | Subsequent retried dispatches succeed. |
| Agent reconnects after dropping mid-RunScope | Old spawn-ids dead. Subsequent dispatches force fresh spawns. Spawned processes orphaned during the gap are SIGKILLed by the agent's reconnect-recovery. |
| Proxy restart | All agent connections drop and reconnect. All in-flight dispatches fail. New dispatches resume cleanly. |
| Supervisor crash mid-dispatch | `orphan-reaper` reclaims the row; another supervisor retries. Spawned process unchanged; routes to the new supervisor's call transparently. |
| Two agents under same api-key | Latest Register wins; older connection is gracefully closed. The RegisterAck carries `displaced_prior: true` for the new connection. (If the user wants explicit multi-agent routing, future work; for now they should stop one agent before starting the other.) |
| Binary not in `$PATH` and path is a bare name | `exec()` fails; agent returns `SpawnAck{failed, error}`; proxy returns `spawn_failed`. |
| Binary requires args the user didn't supply | Caller's problem; the v1 binding shape doesn't carry `args`. (Future extension.) |
| Spawned process opens its own outbound network connections (not via agent) | Allowed; the agent doesn't sandbox the child's network. Optional safety controls are out of scope for v1. |

## Testing strategy

- **Unit tests** alongside each new module (`cmd/rimsky-host-agent/...`, `cmd/rimsky-host-agent-proxy/...`).
- **Proxy conformance against existing executor / claim-producer conformance binaries.** Run `rimsky-executor-conformance` against the proxy with a stub spawned process at the other end of an in-process agent fake. Run `rimsky-claim-producer-conformance` similarly. The proxy should pass both without modification to the conformance binaries.
- **End-to-end scenario test** under `test/scenarios/`: a template with a late-bound executor, an instance created with `service_bindings`, an agent connecting in-process, a spawned stub binary, full dispatch + callback + reap cycle. Verifies the integration across proxy, agent, supervisor, and lifecycle-subscriber.
- **Failure-mode scenario tests**: agent disconnect mid-dispatch (asserts `host_agent_disconnected`), spawn timeout (asserts `spawn_failed`), missing binding (asserts `binding_not_found`), proxy restart with reconnect (asserts agents reconnect and subsequent runs succeed).
- **Multi-process integration test**: spin up control-api + supervisor + proxy in separate processes via `deploy/dev-up.sh`; assert that a `rimsky run` against a stub binary completes end-to-end.

The proxy as a `concept:service` is conformance-testable by construction — no new conformance binary is required for the proxy itself; the existing per-protocol conformance binaries cover it. A separate `rimsky-host-agent-conformance` binary (covering the agent ↔ proxy protocol from the agent side) is a follow-up; not v1.

## v1 implementation scope

In scope:

- `cmd/rimsky-host-agent-proxy/` binary with full `HostAgent.Connect` server and the agent-connection state machine.
- Proxy-side handlers for `Executor` (+ `ExecutorObservability`) and `ClaimProducer` (+ `ClaimProducerObservability`) (supervisor-facing — late-bound service fronting). Plus `LifecycleSubscriber` (consumer role — active handlers for `OnInstanceCreated` and `OnRunScopeTerminal`; no-op `LifecycleAck` returns for the other 5 methods). Handlers for the remaining gRPC rimsky protocols (`Publisher`, `Validation`, `DataProcessing`) are registered services that return `UNIMPLEMENTED`. `BlobBackend` is not implemented because it has no wire protocol.
- `cmd/rimsky-host-agent/` binary with full spawn / dispatch / reap / local-http-forward flow.
- `rimsky agent {start, status, stop}` subcommands on the main `rimsky` CLI binary (or a separate `rimsky-host-agent` binary alias — same code).
- Additive flags on `rimsky run`: `--template <name>` (as a sibling to the existing positional `<file>` form), `--param k=v` (sibling to existing `--params <json>`), `--service <name>=<path>` (new), plus the auto-start-agent behavior.
- `rimsky auth login` verb. Extends `code:control/cli/config.go::Context` with an `api_key` field; written by the login flow.
- Optional CLI-side aliases at `~/.rimsky/aliases.yml` and `.rimsky/aliases.yml`.
- New column `rimsky_instances.service_bindings JSONB` and the migration (postgres + sqlite).
- New column `rimsky_instances.created_by_api_key_id UUID` (nullable, FK to `rimsky_api_keys(id)`). Populated at instance-creation by destructuring `ident, _ := IdentityFromContextOK(ctx)` and reading the `*shared.UUID`-typed `ident.KeyID` field (matches the pattern at `code:control/controlapi/auth_handlers.go:118-129`). Null for anonymous-mode-created instances.
- Request-body and provisioning plumbing for both new columns: extend `code:control/controlapi/instances.go::createInstanceRequest` with `ServiceBindings json.RawMessage` (`json:"service_bindings,omitempty"`); thread the requesting api-key id from `ctx` through `provisionArgs` and `provisionInstanceTx`; extend `persistence.InstanceCreateInput` and `InstanceRow` with both fields; update postgres + sqlite INSERT SQL to land the values.
- New proto file `protocols/proto/v1/host_agent.proto` (per "Wire shapes" §).
- Proto change to `protocols/proto/v1/lifecycle.proto`: extend `OnInstanceCreatedRequest` with `bytes service_bindings` and `string owner_api_key_id`; add `OnRunScopeTerminal` RPC + `OnRunScopeTerminalRequest` message.
- Control-api wiring for the new payload fields: extend `code:control/controlapi/lifecycle.go::InstancePayload` with `ServiceBindings json.RawMessage` and `OwnerAPIKeyID shared.UUID`; update `dispatchInstanceEvent` (near line 307) to forward both fields; update the create call site at `code:control/controlapi/instances.go` (around line 415) to read them from the freshly-created `rimsky_instances` row and populate the payload.
- Add `LifecycleIdempotencyScopeRunScope` (third value alongside the existing `Template` and `Instance`) on `code:foundation/persistence/lifecycle_idempotency.go` (or wherever `LifecycleIdempotencyScope*` is defined), plus a new state value `LifecycleIdempotencyStateRunScopeTerminal`. Migration adds the new scope value to the `rimsky_lifecycle_idempotencies` table's check constraints if any.
- Extend the registration validator at `code:control/controlapi/templates.go::validatorHooksFor` to consult `spec.LateBindServices` first and short-circuit `ExecutorDeclared` / `ExecutorExpectedAttributesSchema` checks for names in the list.
- **Supervisor process gains outbound `LifecycleSubscriber` machinery**: a `*locks.LifecycleRegistry`, a `dialLifecycleSubscribers` invocation at startup (walking the union of `claim_producers:` and `executors:` entries whose `protocols:` list includes `lifecycle_subscriber`, analogous to the existing control-api wiring at `code:control/config/controlapi.go:187`), and a `FanOutRunScopeEvent(ctx, tplSpec, request)` helper analogous to control-api's `FanOutInstanceEvent`. The helper is called synchronously after `code:foundation/persistence/postgres/run_scopes.go::Close` commits at the two existing close sites (`runtime/subgraph_dispatch.go`, `runtime/auto_terminal_chain.go:158`). The close-site callers resolve `tplSpec` via a two-step lookup: `args.Persist.Instances().Get(ctx, instanceID, tx)` for the `TemplateHash`, then `args.Persist.Templates().GetByHash(ctx, hash, tx)` for the spec (same pattern as `runtime/runner_locks.go:171-175`).
- **Main RunScope close at instance termination**: control-api closes the instance's main run-scope and fires `OnRunScopeTerminal` before the existing `OnInstanceTerminated`. New control-api-side `FanOutRunScopeEvent(ctx, tplSpec, request)` helper sibling to `FanOutInstanceEvent`. Insertion sites: (a) alongside the existing `FanOutInstanceEvent(EventInstanceTerminated)` call in `code:control/controlapi/instance_terminator.go::tick`'s polling loop — close the main scope, then fire `OnRunScopeTerminal`, then fire `OnInstanceTerminated` (sub-tick lag vs the row's terminal mark; acceptable for v1); (b) the analogous insertion at the explicit-DELETE path in `code:control/controlapi/instances.go` (around line 614) — synchronous in the DELETE request, no lag. Without this, spawns on the main run-scope are never reaped except by agent disconnect — the common case (single-graph workflows with no sub-graphs / fan-out).
- Extend `code:control/controlapi/lifecycle.go::lifecyclePeersForSpec` to include `late_bind_service_proxies.*` peer names when a template has a non-empty `late_bind_services` list, so the proxy actually receives `OnInstanceCreated` events for late-bound templates.
- Add a `LifecyclePeersForSpec func(tplSpec node.TemplateSpec) []string` field on `code:control/config/supervisor.go::SupervisorConfig`. Production wiring at the supervisor entrypoint supplies a closure that calls `controlapi.lifecyclePeersForSpec` with the rimsky.yml `late_bind_service_proxies` baked in. The supervisor invokes the function pointer at `FanOutRunScopeEvent` time. Matches the existing `ExpectedAttributesSchemaFor` function-pointer pattern; avoids `runtime/` → `control/` import (denied by `.golangci.yml`'s `runtime-purity` rule).
- Add a `LateBindServiceProxies map[string]string` field on `code:control/controlapi/app.go::AppDeps`, populated by `StartControlAPI` from rimsky.yml. Consumed by `lifecyclePeersForSpec` to know which proxy peer-names to include when a template declares `late_bind_services`.
- gRPC client-side `UnaryClientInterceptor` + `StreamClientInterceptor` installed on the supervisor's dial of all peer services, attaching the `x-rimsky-service-name` header from the per-call context. Dispatch call sites stamp the name via `metadata.AppendToOutgoingContext(ctx, "x-rimsky-service-name", serviceName)` before each call (executor's `Execute` site; claim-producer's `Open`/`Commit`/`Abandon`/`Release` sites).
- `LateBindResolver` and the `Resolver.Resolve(name, DispatchContext)` signature extension. **No `Endpoint.Metadata` field** — the resolver returns the plain endpoint and the per-call interceptor handles routing-name attachment.
- Add `GetWithContext(name string, ctx DispatchContext) (ClaimProducer, bool)` as a new method directly on `code:foundation/locks/registry.go::Registry` (alongside the existing `Get`). Add `LookupInstanceBindings` and `LateBindServiceProxies` fields to the registry's construction; both nil → registry behaves as static-only. Returns the static proxy producer for names found in `instance.service_bindings`; header-stamping is the call-site interceptor's job, not the registry's. Direct method (not a wrapper interface) avoids re-typing the 5+ `*locks.Registry` field sites.
- Thread `InstanceID` into the dispatch call paths: extend `acquireOneLock` (`code:runtime/runner_acquire_named_locks.go`), `acquireClaim` (`code:runtime/runner_acquire_claims.go`), and `acquireFanOutIfDeclared` (`code:runtime/runner_acquire_helpers.go`) with an `instanceID shared.UUID` parameter. Source: `nd.InstanceID` (the `NodeRunRow` already fetched at `tryAcquire` in `code:runtime/runner_acquire.go` around line 424); `tryAcquire` passes it down to both `acquireOneLock` and `acquireFanOutIfDeclared`. Extend `AcquireSubClaimsInput` (`code:runtime/runner_subclaim.go`) with an `InstanceID shared.UUID` field; populate at the production caller `code:runtime/runner_acquire_helpers.go:89` (inside `acquireFanOutIfDeclared`) from the newly-threaded parameter, and at test callsites. Switch the call sites from `Registry.Get(name)` to `Registry.GetWithContext(name, DispatchContext{InstanceID, RunScopeID})`.
- `LateBindResolver` and `*locks.Registry`'s new `GetWithContext` both carry a new persistence dependency: a `LookupInstanceBindings func(ctx, instanceID) (map[string]json.RawMessage, bool, error)` hook supplied at construction time. Production wiring derives it from `persistence.Tables().Instances().Get(ctx, id, tx)` reading the `service_bindings` column. Wired through `code:control/config/supervisor.go::StartSupervisor`'s `Resolver` and `Stores` fields. The hot-path DB hit is acceptable for v1 (one query per dispatch resolution); future work can add a supervisor-local cache populated via lifecycle subscription if measured load demands it.
- Supervisor claim-query admit-list extension (executor + claim-producer), as a targeted modification to `code:foundation/persistence/postgres/queue.go::SelectCandidates` (SQL aliases `rimsky_instances` as `i`; the proposed clause adds `i.service_bindings ? d.executor_name AND <proxy-name> = ANY($2::text[])`).
- Proxy's `OnInstanceCreated` / `OnRunScopeTerminal` subscription handler implementations (consumer role), plus no-op `LifecycleAck` returns for the 4 template lifecycle methods + `OnInstanceTerminated`, plus `GET /instances/{id}` fallback path for cache misses.
- Proxy's callback-URL rewrite mechanism, including reading the agent's `local_callback_base_url` from `Register`.
- Late-bind template field: `late_bind_services: [...]`, parsed and **stored inside the canonical-spec bytes** (so the field affects the template's content-address hash; changing the list reregisters the template under a new hash, preserving `concept:template`'s identity invariant); honored at registration to skip resolver / schema checks.
- Optional `--allow-paths` flag and `--listen <addr>` flag on `rimsky agent start`.
- New `error_class` values plumbed through the executor-Error vocabulary (via `StreamClose{Error, error_class}`) and the claim-producer gRPC-status path (via `google.rpc.ErrorInfo` reason field).
- Extend the supervisor's claim-producer client at `code:runtime/remote/client.go` to translate gRPC status → an internal error struct that carries `error_class` (the `reason` field of `google.rpc.ErrorInfo`).
- Route claim-producer errors through `applyErrorPolicy` at the call sites that today bubble them as `openResultBail` — primarily `code:runtime/runner_acquire_claims.go` (the `producer.Open` failure path) and the analogous `Commit`/`Abandon`/`Release` call sites. The error carries the translated `error_class`, which `code:runtime/runner_error_policy.go::applyErrorPolicy` reads to consult the template's `error_types:` chain. Without this receiving-side change, the translator from the previous item never reaches the policy chain on the claim-producer path.
- New `rimsky.yml` field `late_bind_service_proxies` (per-protocol map).
- Tests per "Testing strategy" above.

Out of scope (deferred):

- Other proxy supervisor-facing protocol handlers (`Publisher`, `Validation`, `DataProcessing`). Architecturally admitted, implementation phased in by use case. (LifecycleSubscriber is in v1 as the proxy's consumer-role handler — not deferred.)
- `rimsky-host-agent-conformance` binary.
- Pool-routed bindings (multiple agents serving a capability with rimsky picking one).
- Long-running pinned bindings (B from the earlier 2×2 — attach to an already-running local process rather than spawn).
- Per-binding env / args / cwd / timeout overrides.
- Sandboxing / spawn-allowlist UI beyond `--allow-paths`.
- Internal-service auth between rimsky processes (separate tension, see Design changes).
- A real synthetic error class for "executor / claim-producer unreachable, no supervisor accepts it" (separate tension, see Design changes).

## Design changes

This spec creates two concept files, mutates several existing ones, and catalogs two new tensions.

### New concepts

- **Create `.ok-planner/design/concepts/host-agent.md`.** Frontmatter:

  ```yaml
  ---
  concept: host-agent
  status: as-is
  aliases: []
  references:
    - ../../specs/2026-05-24-host-agent-and-proxy-design.md
  ---
  ```

  Sections (use the prevailing heading set; "Adjacent" goes inside Boundaries as a sub-paragraph per the convention in `concepts/instance.md` and most other concept files):

  - `## What it is` — A long-running daemon on a user's dev machine, bundled into the `rimsky` CLI binary. Authenticates outbound to a `concept:host-agent-proxy` with the user's `concept:api-key`. Serves spawn / dispatch / reap / local-HTTP-forward requests against locally-running binaries. Lives at `cmd/rimsky-host-agent/`; subcommand alias `rimsky agent` on the main `cmd/rimsky/` binary.
  - `## Purpose` — Lets users run arbitrary local binaries as rimsky services on a per-invocation basis without static deployment configuration. Eliminates the manual "start the local process, wire up reachability, trigger an instance, tear down on completion" setup that plagued pre-host-agent dev workflows.
  - `## Boundaries` — Owns: dev-machine process spawn/exec, local HTTP listener termination, the agent-side end of the agent ↔ proxy bidi stream, child-process reaping on Reap or connection close. Does NOT own: service discovery, capability advertisement (the spawned binary advertises its own Capabilities via the proxy-driven handshake), persistent state across restarts, the supervisor-facing service protocols (those live on the proxy). Adjacent: `concept:host-agent-proxy`, `concept:service`, `concept:api-key`.
  - `## Invariants` — (a) no capability config; the agent does not know in advance what binaries exist. (b) `exec()` does path resolution; absolute, relative, and bare-name paths all work via `$PATH`. (c) spawned children inherit the agent's full environment unless overridden by future per-binding fields. (d) on bidi-stream close (clean or unclean), all live children are SIGTERM'd and SIGKILL'd after a configurable grace period. (e) the agent has no persistent state of its own; it reads auth from `~/.rimsky/config.yml`'s active context (the existing CLI config file, extended with an `api_key` field on `Context`).
  - `## Aliases and historical names` — None.
  - `## Open within this concept` — None at creation time.
  - `## Notes` — Empty initially; per-spec creation entry.

- **Create `.ok-planner/design/concepts/host-agent-proxy.md`.** Frontmatter:

  ```yaml
  ---
  concept: host-agent-proxy
  status: as-is
  aliases: []
  references:
    - ../../specs/2026-05-24-host-agent-and-proxy-design.md
  ---
  ```

  Sections:

  - `## What it is` — A rimsky-stack `concept:service` implementing the multi-protocol composition pattern (per `concept:service` invariants: distinct handler types per protocol, separately registered on one gRPC server). Presents the rimsky gRPC service protocols (`Executor`, `ClaimProducer`, eventually `Publisher` / `LifecycleSubscriber` / `Validation` / `DataProcessing`) on the supervisor-facing side. Maintains agent connections on the dev-facing side via the new `proto:host_agent.proto::HostAgent.Connect` bidi stream. Routes dispatches to whichever agent is connected for the instance's owner. Lives at `cmd/rimsky-host-agent-proxy/`. Declared in `cfg:rimsky.yml` per protocol it serves (one entry under `executors:`, one under `claim_producers:`, etc., all pointing at the same binary).
  - `## Purpose` — Lets rimsky dispatch work to dev-machine binaries declared per-instance, without changing any supervisor or graph-processing code path. The proxy is the single architectural addition; supervisors, dispatch resolution, error vocabulary, and callback handling are unchanged.
  - `## Boundaries` — Owns: the agent ↔ proxy bidi stream protocol (`proto:host_agent.proto`), the spawn-lifecycle state machine, the per-instance `service_bindings` cache (populated via `concept:lifecycle-subscriber`), the per-protocol dispatch handlers that proxy through to spawned processes, the callback-URL rewriting that lets spawned processes POST to the agent's local listener rather than dialing the supervisor. Does NOT own: the rimsky-side service protocols themselves (those are `concept:executor`, `concept:claim-producer`, etc.), the supervisor's dispatch logic, the per-instance state in `table:rimsky_instances` (that's `concept:instance`), the lifecycle-subscriber wire protocol (that's `concept:lifecycle-subscriber`). Adjacent: `concept:host-agent`, `concept:service`, `concept:executor`, `concept:claim-producer`, `concept:lifecycle-subscriber`, `concept:instance`, `concept:rimsky-yml`.
  - `## Invariants` — (a) implemented via the existing multi-protocol composition pattern on `concept:service` — distinct handler types, no shared CapabilitiesProvider. (b) one spawn per `(run_scope_id, binding_name)`, lazy birth on first dispatch, run-scope-lifetime, reaped on `OnRunScopeTerminal`. (c) all dispatch failures surface as executor-Error / claim-producer-Unavailable terminals on the supervisor-facing protocol — no new synthetic supervisor-side `acquire/*` error classes. (d) the proxy is declared in `cfg:rimsky.yml` per protocol it serves, using the same binary across all entries (one endpoint, N namespace registrations). (e) the proxy is the URL-rewriting boundary for rimsky URLs handed to spawned processes (callback_url specifically; other rimsky URLs follow the same principle as additional protocols are wired).
  - `## Aliases and historical names` — None.
  - `## Open within this concept` — None at creation time.
  - `## Notes` — Initial entry per spec 2026-05-24-host-agent-and-proxy-design: v1 implements `Executor` and `ClaimProducer` supervisor-facing handlers (late-bound service fronting) plus `LifecycleSubscriber` as the proxy's own consumer-role handler (`OnInstanceCreated` for binding-cache, `OnRunScopeTerminal` for reap; other 5 methods no-op `LifecycleAck`). `Publisher` / `Validation` / `DataProcessing` handlers ship registered but `UNIMPLEMENTED`. `BlobBackend` is intentionally excluded — it's an in-process Go interface, not a gRPC wire protocol.

After creating the two new concept files, **regenerate `.ok-planner/design/concepts.md`** so the TOC includes one-sentence entries for `host-agent` and `host-agent-proxy`. (Per `.ok-planner/CLAUDE.md`, `concepts.md` is auto-generated; the regeneration runs as part of `/execute-plan`'s design-doc mutation step.)

### Mutations to existing concepts

- **Mutate `concepts/executor.md`.** Append a Notes entry: `[2026-05-24] Proxy-mediated late-bound executors are admitted via the host-agent + host-agent-proxy pattern (see concept:host-agent-proxy). The protocol surface is unchanged; the proxy implements Executor like any other service binary, dispatching through agent connections to dev-machine-resident workers. Per spec 2026-05-24-host-agent-and-proxy-design.`

- **Mutate `concepts/claim-producer.md`.** Append a Notes entry analogous to the above, for the claim-producer protocol.

- **Mutate `concepts/service.md`.** Append a Notes entry: `[2026-05-24] The host-agent-proxy is a multi-protocol service that bridges rimsky-side protocols to dev-machine binaries declared per-instance. It follows the existing multi-protocol composition pattern (one binary, N handler types) and inherits all of concept:service's invariants. Per spec 2026-05-24-host-agent-and-proxy-design.`

- **Mutate `concepts/instance.md`.** Boundaries section: add `service_bindings` to the list of per-deployment knobs (alongside `params` and `attribute_overrides`); add `created_by_api_key_id` as a new identity-carrying field (FK to `rimsky_api_keys.id`). Invariants: add an entry stating `service_bindings` is opaque JSONB, set at instance creation, consumed by the host-agent-proxy at dispatch time; add an entry stating `created_by_api_key_id` is the api-key whose authenticated request created the instance (nullable for instances created under `concept:anonymous-mode`). Append Notes entry referencing the spec.

- **Mutate `concepts/rimsky-yml.md`.** Boundaries section: add the per-protocol `late_bind_service_proxies` map. Invariants: add an entry stating that late-bound service names resolve via the named proxy declared per-protocol in this map. Append Notes entry referencing the spec.

- **Mutate `concepts/template.md`.** Invariants section: add `late_bind_services` as a top-level template field; names in the list bypass registration-time existence and schema validation. The field is **stored inside the canonical spec bytes** — so it participates in the JCS-canonicalized template hash, preserving the existing content-addressing invariant (changing the list reregisters the template under a new hash). Append Notes entry referencing the spec.

- **Mutate `concepts/lifecycle-subscriber.md`.** Invariants section: extend the method count from 6 to 7. Add the new method `OnRunScopeTerminal(run_scope_id, terminal_reason)`. **Relax the existing "events fire from control-api, synchronously at state-transition time" invariant**: the relaxed invariant becomes "lifecycle-subscriber events fire synchronously from the rimsky-side process that owns the state transition" — template / instance events from control-api as today, run-scope-terminal events from the supervisor that closes the scope (via the run-scope close path at `code:foundation/persistence/postgres/run_scopes.go::Close`, called from `code:runtime/subgraph_dispatch.go` and `code:runtime/auto_terminal_chain.go:158`). DB-tracked idempotency via `table:rimsky_lifecycle_idempotencies` is preserved across both firing sites. Boundaries section: add an entry recording that the supervisor process is now also a lifecycle-event firer (in addition to control-api), with its own `*locks.LifecycleRegistry` dialed at startup via the existing `dialLifecycleSubscribers` walker (which finds entries in `claim_producers:` and `executors:` whose `protocols:` list includes `lifecycle_subscriber` — no new top-level YAML block). Append a Notes entry recording the rationale (run-scope close is a runtime concern, not a control-plane concern; routing it through control-api would require new internal-service plumbing for no semantic gain) and noting that the peer-filter extension at `code:control/controlapi/lifecycle.go::lifecyclePeersForSpec` includes the late-bind proxy when a template declares `late_bind_services` — scoped to instance- and run-scope-keyed fan-out only, not template-event fan-out (the proxy doesn't consume template events).

- **Mutate `concepts/supervisor.md`.** Boundaries section: (a) note that `Resolver.Resolve` now accepts a `DispatchContext` parameter for instance-aware resolution; (b) note that the supervisor process now dials outbound `LifecycleSubscriber` peers via `dialLifecycleSubscribers` (which walks the union of `claim_producers:` and `executors:` entries with `protocols: [..., lifecycle_subscriber]` — no new top-level YAML block), maintains its own `*locks.LifecycleRegistry`, and fires `OnRunScopeTerminal` synchronously after run-scope close; (c) note that the supervisor's gRPC dial config for every peer service installs a client-side interceptor that attaches an `x-rimsky-service-name` header from the per-call context. Invariants section: extend the `accepted_executors` / `accepted_stores` filter clause with the late-bind OR-clause described in this spec. Append Notes entry referencing the spec.

- **Mutate `concepts/error-policy.md`.** Append a Notes entry: `[2026-05-24] New error_class values added to the executor-Error and claim-producer-error vocabularies for proxy-mediated dispatch failures: host_agent_not_connected, binding_not_found, spawn_failed, host_agent_disconnected, contract_mismatch, executor_crashed. These ride the existing error_types: chain with no policy-mechanism changes. Per spec 2026-05-24-host-agent-and-proxy-design.`

- **Mutate `concepts/conformance.md`.** Append a Notes entry: `[2026-05-24] The host-agent-proxy is conformance-testable as a normal service via the existing rimsky-executor-conformance and rimsky-claim-producer-conformance binaries, run against the proxy with a stub spawned process behind an in-process agent fake. A dedicated rimsky-host-agent-conformance binary covering the agent ↔ proxy protocol from the agent side is a follow-up. Per spec 2026-05-24-host-agent-and-proxy-design.`

- **Mutate `concepts/rimsky.md`.** This spec adds substantial CLI surface that the concept doc must reflect:
  - "What it is" section: extend the subcommand-groups enumeration to include the new `agent` group (`start`, `status`, `stop`) and the new `auth login` verb (alongside existing `auth init | create-key | list | show | revoke | rotate | status`).
  - Boundaries section: add the agent-daemon-bundling responsibility (the CLI binary doubles as the host-agent daemon when invoked as `rimsky agent start`).
  - Invariants section: add an entry recording the additive flag changes on `rimsky run` — `--template <name>` is a new sibling to the existing positional `<file>` shape (mutually exclusive); `--param k=v` is a new sibling to existing `--params <json>` (mixable, later-wins); `--service <name>=<path>` is new. The existing positional `<file>` shape and `--params` flag retain today's semantics. Also add an entry recording that the `code:control/cli/config.go::Context` struct is extended with an `api_key` field, populated by `auth login` and consumed by the agent for outbound authentication.
  - Notes entry: per spec 2026-05-24-host-agent-and-proxy-design. The `auth login` verb is sibling to `auth init` (does not replace it); `init` retains its bootstrap-from-anonymous-mode role, while `login` is the convenience verb for the dev-machine user logging into an already-bootstrapped rimsky deployment. The CLI also reads optional alias files at `~/.rimsky/aliases.yml` (global) and `.rimsky/aliases.yml` (project-local) for client-side `--service` resolution; these are pure CLI sugar and the server never sees aliases.

### Tensions touched

- **`tensions/callback-hostname-split.md`** — no move. Append an addendum recording two things: (1) proxy-mediated executors do not sidestep this tension — the proxy → supervisor callback hop has the same advertise-host requirement as any other executor → supervisor hop today; (2) a new hostname class joins the system implicitly — the agent's local-listener address (used by spawned processes to POST callbacks back through the agent). The agent's local address is implicit (loopback by default, reported to the proxy via the `local_callback_base_url` field on `Register`) and doesn't need an advertise-host knob because agents dial outbound. Per spec 2026-05-24-host-agent-and-proxy-design.

### New tensions to catalog

- **Create `.ok-planner/design/tensions/unreachable-service-row-stall.md`.** Contents:

  ```markdown
  ---
  tension: unreachable-service-row-stall
  category: unspecified
  status: open
  affects:
    - executor
    - claim-producer
    - supervisor
    - error-policy
  ---

  # Dispatch rows for unknown / unreachable services sit in queue with no error class

  ## What is muddy

  When a dispatch row's `executor_name` (or required claim-producer) is not in any supervisor's `accepted_executors`, the row sits in queue indefinitely. No synthetic error class fires. Hosted and proxy-mediated executors share this behavior.

  ## Why it matters

  In the proxy case, the user's agent not being connected is a normal transient state, and the rimsky operator may want to alert on it after a threshold. There is no mechanism for that today. The hosted case has the same problem (a misconfigured `accepted_executors` or a dead executor service silently swallows dispatches).

  ## Resolution candidates (do NOT pick)

  - A new synthetic error class `acquire/unreachable` fired after a threshold age on the row.
  - A watchdog that flips rows from `queued` to `failed` with a configured class after a timeout.
  - A per-template `min_admissible_age:` field.

  ## Evidence

  - This spec: `.ok-planner/specs/2026-05-24-host-agent-and-proxy-design.md` §"Error handling".
  ```

- **Create `.ok-planner/design/tensions/anonymous-mode-locks-out-late-bind.md`.** Contents:

  ```markdown
  ---
  tension: anonymous-mode-locks-out-late-bind
  category: unclear
  status: open
  affects:
    - anonymous-mode
    - host-agent-proxy
    - instance
  ---

  # Anonymous-mode users can register late-bind templates but cannot dispatch them

  ## What is muddy

  Anonymous-mode (`Identity.KeyID == nil`) leaves `rimsky_instances.created_by_api_key_id` null. The proxy's routing requires the owner-api-key to find a connected agent, so any late-bound service dispatch fails with `host_agent_not_connected` for anonymous-mode-created instances. Anonymous-mode users can register templates and trigger workflows, but cannot use late-bound services.

  ## Why it matters

  Anonymous-mode is the documented bootstrap path for unauthenticated rimsky deployments. Locking it out of a major feature is a real product asymmetry. For dev-machine workflows (the host-agent's primary use case) the user is already authenticated, so the v1 constraint is acceptable; but a hosted deployment where some users are anonymous and want code-gen would hit this wall.

  ## Resolution candidates (do NOT pick)

  - Emit a synthetic admin identity (already done; but `KeyID == nil` still).
  - Allow CLI to supply an "agent-routing-key" header that the proxy uses instead of `owner_api_key_id`.
  - Require all late-bind-using deployments to disable anonymous-mode.

  ## Evidence

  - This spec: `.ok-planner/specs/2026-05-24-host-agent-and-proxy-design.md` §"Per-instance service bindings — Owner identity".
  ```

- **Create `.ok-planner/design/tensions/internal-service-auth-unspeced.md`.** Contents:

  ```markdown
  ---
  tension: internal-service-auth-unspeced
  category: unspecified
  status: open
  affects:
    - supervisor
    - control-api
    - host-agent-proxy
  ---

  # No mechanism for rimsky-process-to-rimsky-process authentication

  ## What is muddy

  Rimsky has no mechanism for one rimsky process to authenticate to another. Supervisor → control-api coordination is DB-only today. The host-agent-proxy → control-api lifecycle subscription introduces a service-to-service call path (proxy subscribing to lifecycle events, proxy POSTing publisher messages to `/instances/{id}/messages`) that relies on deployment-level network isolation rather than explicit auth.

  ## Why it matters

  Production deployments may want mTLS or service-tokens between rimsky processes. Today's posture is implicit.

  ## Resolution candidates (do NOT pick)

  - Internal-service api-key kind with a system-permission grant.
  - mTLS via per-process certificates.
  - A service-mesh handoff.

  ## Evidence

  - This spec: `.ok-planner/specs/2026-05-24-host-agent-and-proxy-design.md` §"Multi-process behavior" and §"Cache freshness".
  ```
