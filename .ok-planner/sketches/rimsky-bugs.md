# Rimsky bugs and feature requests found while consuming as a third-party project

Tracked here so they survive zonebase sessions; copy over to
rimsky-core's own tracker when fixing. Each entry: version, what we
saw, where, repro.

Add new findings to the top of the relevant section. Strike through
(or move to "Fixed upstream") once we bump rimsky and the issue is
resolved.

Sections:
- **Open** — confirmed bugs, with workarounds where applicable.
- **Feature requests** — capability gaps surfaced by real-world use.
  Not bugs in the strict sense; rimsky works without them, but
  consumers (us) hit friction that a small addition upstream would
  remove.
- **Fixed upstream** — items rimsky has addressed in a later version.

---

## Open

### Supervisor `callback_advertise_host` is not propagated to the rimsky_supervisors DB row

- **Version:** `lib/protocols/v0.2.1` (image `rimskyai/rimsky-all-in-one:v0.2.1`)
- **Surface:** async-handoff callback URL handed to executors
- **Symptom:** even with `RIMSKY_SUPERVISOR_CALLBACK_ADVERTISE_HOST=rimsky-supervisor` (or any value) set on the supervisor's container env, the callback URL given to executors comes back as `http://127.0.0.1:9100/v1/callback/<ack>` — claude-agent reports `TypeError: fetch failed` when it tries to POST the terminal outcome there. The supervisor binds to `[::]:9100` and writes that bind addr (rendered as `127.0.0.1` by some intermediate) into `rimsky_supervisors.callback_host`; the in-memory `Handle.advertisedURL` uses the env-driven advertise host but the DB row does not.
- **Cause (suspected):** in `lib/runtime/supervisor.go` around lines 290–310, the supervisor calls `splitHostPort(addr)` on the listener bind address and registers that into `rimsky_supervisors` via `Persist.Supervisors().Register({CallbackHost: host, CallbackPort: port, ...})`. The `cfg.CallbackAdvertiseHost` / `CallbackAdvertisePort` fields are computed into `h.advertisedURL` (line 316) and threaded into the in-process dispatch (line 487 `CallbackURL: h.advertisedURL`), but they are NOT written into the persisted supervisor row.
- **Compounding factor:** the `rimsky-all-in-one` `rimsky-control-api` binary also starts an embedded supervisor. That container, in our deployment, doesn't have the advertise-host env var, so even if the standalone `rimsky-supervisor` is correctly configured, dispatches that route through the control-api's embedded supervisor still get `127.0.0.1`. Setting the env var on both services is the in-zonebase workaround.
- **Repro:** stand up a multi-container compose stack with `rimsky-supervisor` and `claude-agent` on different services, set `RIMSKY_SUPERVISOR_CALLBACK_ADVERTISE_HOST=rimsky-supervisor` on the supervisor, dispatch any claude-agent node, watch the executor log `callback POST failed → http://127.0.0.1:9100/...`.
- **Workaround in zonebase:** set the env var on **every** all-in-one service that runs a supervisor, not just `rimsky-supervisor` — also `rimsky-control-api` (and presumably `rimsky-scheduler` if it dispatches).

### Claude-agent MCP callback gated by Claude Code's permission prompt despite `bypassPermissions`

- **Version:** `rimskyai/rimsky-executor-claude-agent:v0.2.1` (running with `@anthropic-ai/claude-code` 2.1.153) — second dispatch in a session.
- **Surface:** the agent calling `mcp__rimsky-callback__report_complete`.
- **Symptom:** on a fresh dispatch, the Claude CLI returns `tool_use_result: "Error: Claude requested permissions to use mcp__rimsky-callback__report_complete, but you haven't granted it yet."` for every attempt — even though the rimsky claude-agent spawns the CLI with `--permission-mode bypassPermissions`. The agent retries, fails the same way, eventually clean-exits with no `report_complete` ever landing. The executor logs `cli.clean_exit_no_report; attempting resume` and retries; the resume hits the same gate.
- **What does work:** the FIRST dispatch in a fresh claude-agent container often succeeds the `report_complete` call (then fails on the network callback per the bug above). Whatever state primes that first call doesn't carry across.
- **Cause (suspected):** Claude Code 2.1.x's "deferred MCP tools" surface (the agent has to `ToolSearch select:mcp__rimsky-callback__report_complete` before it can call the tool). The deferred surface may be applying a permission check that `bypassPermissions` doesn't cover, or the per-dispatch internal MCP server's URL allowlist isn't being registered with the CLI's permission cache on subsequent spawns.
- **Repro:** create two consecutive claude-agent dispatches in the same `claude-agent` container (recreate the container if needed). On the second one (sometimes the first), watch for `permission_denials` containing `mcp__rimsky-callback__report_complete` in the CLI's terminal result payload.
- **Workaround:** none yet identified. Setting `cli.permission_mode: bypassPermissions` explicitly in the template attributes had no effect; the default already is `bypassPermissions`. Adding `mcp__rimsky-callback__report_complete` to `cli.allowed_tools` might work but we haven't tested.

### Template validator's "permissive executor schema" check ignores `additionalProperties: true`

- **Version:** `lib/protocols/v0.2.1`
- **Surface:** `POST /templates` validation; `checkAttributesSchema` in `lib/graph/node/template_validator.go:1648-1626` and the matching `IsPermissiveExecutorSchema` helper at line 1640.
- **Symptom:** every output property a template author wants the agent to write back (e.g. `zone_codes`, `notes`, `parcel_config`) is rejected at registration with `property has no source:, no default:, and is not marked readOnly: true in the executor's expected_attributes_schema`. The validator forces template authors to either fabricate empty `default:` values (`zone_codes: { type: array, default: [] }`) or mark template-side `readOnly: true` — and the latter is also rejected because the executor's schema doesn't carry `readOnly` for properties it doesn't know about.
- **Cause:** `IsPermissiveExecutorSchema` returns true only when the executor's schema lacks a `properties:` block. claude-agent's schema (`lib/services/executors/claude-agent/src/expected-attributes-schema.ts`) DOES enumerate `properties:` (cwd, model, system_prompt, …) but ALSO declares `additionalProperties: true` — meaning the executor explicitly accepts any extension property name. The validator ignores the `additionalProperties: true` signal and treats the schema as constrained, so any author-declared extension output fails the readOnly-fallback check.
- **Workaround:** sprinkle synthetic `default:` values across every output property. Awkward (the values get immediately overwritten by the agent's write-back) but functional. See `docs/zonebase-rimsky-templates/zone-source-onboard.yaml` for ~20 examples.
- **Suspected fix upstream:** `IsPermissiveExecutorSchema` should return true when the executor's schema has `additionalProperties: true` AND the property in question is not enumerated in `properties:` (i.e. it's an extension property the executor has agreed to accept).

### Published claude-agent image's `EXPOSE` doesn't match the binary's default listen ports

- **Version:** `rimskyai/rimsky-executor-claude-agent:v0.2.1`
- **Surface:** docker-compose port mapping / service discovery.
- **Symptom:** the upstream image declares `EXPOSE 9090 9190` and rimsky.yml examples advertise the executor at `claude-agent:9090`. But the executor binary defaults to `RIMSKY_EXECUTOR_PORT_GRPC=7071` / `RIMSKY_EXECUTOR_PORT_HTTP=7072` (see `lib/services/executors/claude-agent/src/main.ts:35`). With a default-port deployment, the rimsky supervisor can't reach the executor (DNS resolves, port refuses connection), the dispatch fails at the network layer, and the deployer has to either set both ports via env or change the rimsky.yml endpoint. Either the binary's defaults should be 9090/9190 or the Dockerfile's EXPOSE (and the rimsky.yml example endpoint) should be 7071/7072. They should match.
- **Workaround in zonebase:** set `RIMSKY_EXECUTOR_PORT_GRPC: 9090` and `RIMSKY_EXECUTOR_PORT_HTTP: 9190` on the claude-agent service so the binary binds to the ports rimsky.yml advertises.

### CLI flag syntax: `--flag=value` works, `--flag value` is rejected

- **Version:** `rimsky` CLI bundled with `rimskyai/rimsky-all-in-one:v0.2.1`
- **Surface:** every verb with flags.
- **Symptom:** `rimsky tag create my-tag --template=sha256-abc...` works. `rimsky tag create my-tag --template sha256-abc...` fails with `usage: rimsky tag create <tag> --template <ref>`. Same for `template register --tag=foo` vs `template register --tag foo`. The CLI's flag parser appears to use Go's `flag` package (which treats positional args after the first non-flag as terminating flag parsing) rather than `cobra` or a `--`-aware parser.
- **Impact:** the help output's usage line (`--template <ref>`) is misleading — that syntax doesn't actually work. Discoverability suffers. Easily fixable upstream by switching to a more standard flag parser or by adjusting the help to show `--template=<ref>`.

### `rimskyai/rimsky-executor-claude-agent:v0.2.1` ships a dangling `@rimsky-ai/protocols` symlink

- **Version:** image tag `rimskyai/rimsky-executor-claude-agent:v0.2.1`
- **Surface:** container startup
- **Symptom:** `node dist/main.js` (the image's ENTRYPOINT) errors with
  `Cannot find package '@rimsky-ai/protocols' imported from /app/dist/proto-loader.js`
  and exits 1. The container can never reach a Listening state.
- **Cause:** the build stage uses `"@rimsky-ai/protocols": "file:../../../../lib/protocols"`
  in the executor's package.json; `npm ci` creates a symlink
  `/build/lib/services/executors/claude-agent/node_modules/@rimsky-ai/protocols
  → ../../../../../protocols` that resolves to `/build/lib/protocols` at build
  time. The final-stage `COPY --from=builder ... ./node_modules` copies the
  symlink verbatim — but the relative target now points outside the image
  (`/lib/protocols` doesn't exist).
- **Repro:**
  ```bash
  docker run --rm rimskyai/rimsky-executor-claude-agent:v0.2.1
  # => Error [ERR_MODULE_NOT_FOUND]: Cannot find package '@rimsky-ai/protocols'
  ```
- **Workaround in zonebase:** the extended image
  (`backend/infra/Dockerfile.claude-agent`) replaces the dangling symlink
  with a fresh `npm install @rimsky-ai/protocols@0.1.0` from the public
  npm registry.
- **Suspected fix upstream:** either `npm install --pack-destination` the
  protocols package before copying, or replace the file: dep with the
  published npm version in the executor's package.json, or `cp -L`
  during the COPY so the symlink is dereferenced.

### MCP `tools/call` panics on the inner HTTP route

- **Version:** `lib/protocols/v0.2.1` (image `rimskyai/rimsky-all-in-one:v0.2.1`)
- **Surface:** `POST /mcp` JSON-RPC `tools/call`
- **Symptom:** every `tools/call` we tried (`template_list`,
  `instance_list`) returns a 200 envelope whose inner content reports
  `{"body": null, "error": true, "isError": true, "status": 500}`.
  Server logs show
  `panic: runtime error: invalid memory address or nil pointer dereference`
  at `lib/control/controlapi/templates.go:42` (`defer req.Body.Close()`
  inside `readAllBody`), routed via
  `registerTemplatesRoutes.handleDeployTemplate.func1` — meaning the
  catalog adapter is dispatching the call as POST `/templates` rather
  than the action's declared route.
- **Compare:** the corresponding direct HTTP routes work fine
  (`GET /templates`, `GET /instances` both return 200).
- **Repro:**
  ```bash
  curl -s -X POST http://localhost:8088/mcp \
    -H 'content-type: application/json' \
    -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"instance_list","arguments":{}}}'
  ```
- **Impact for zonebase:** can't drive instance/breakpoint/template
  operations through MCP. The breakpoint test recipe falls back to
  direct HTTP curl for writes; MCP is only useful for
  `tools/list` and `resources/read` (breakpoint hits surface
  correctly).
- **Suspected cause (un-verified):** `lib/control/controlapi/mcp/catalog.go`
  ~line 180–205 — the dispatch builds `route.Method` from the action
  registry but seems to land on POST for actions registered against GET
  routes. Worth checking whether the action-registry adapter swaps GET
  for POST somewhere.

---

## Feature requests

### `rimsky template lint <file>` — validate without persisting

- **Surface:** CLI.
- **Use case:** template authors iterating on a YAML against a new
  rimsky version (or any time the executor's
  `expected_attributes_schema` evolves) currently have to round-trip
  through `rimsky template register`, which both persists a row AND
  bails on the FIRST validation error in any node's bag — so a single
  registration attempt only surfaces a slice of the contract drift.
  In this session we did ~6 register-fix-register loops on
  `zone-source-onboard.yaml` (43 contract-drift items, several rounds
  each) before the template took. A pure-validation verb that runs
  `node.ValidateTemplate` + `checkAttributesSchema` + the source_file
  preprocessor against the local YAML — without inserting a row or
  reporting only one error per node — would collapse those rounds.
- **Suggested shape:** `rimsky template lint <file> [--against-executor=<name>]`,
  emits ALL validation errors as a JSON array (paths + messages),
  exit 0 = clean, exit 1 = drift. Reuses the same code path as
  `template register` minus the `Persist.Templates().Insert` call.

### `rimsky instance kill --force <id>` — force-terminate stuck instances

- **Surface:** CLI + control-api (`POST /admin/instances/{id}/kill`?).
- **Use case:** instances that land in `transient/await_async` because
  the executor's callback never reached the supervisor (callback URL
  bug, network partition, container crash) are non-terminal — `DELETE
  /instances/{id}` rejects them with `instance is not in terminal
  state; wait for terminated_at to be set`. There's no obvious way to
  force the instance into a terminal state for cleanup, short of DB
  surgery. `rimsky admin invalidate | reset` exist but it's unclear
  whether they target this case (we didn't get to test them this
  session). A clearly-documented "abandon this run and free the
  instance_key" verb would close the loop.
- **Suggested shape:** `POST /instances/{id}/terminate { reason: "<...>" }`
  → sets `terminated_at` + status='aborted', best-effort cancels
  any in-flight dispatch, releases held claims via Abandon, frees
  the instance_key for reuse. CLI: `rimsky instance kill <id>
  --reason "<...>" [--force]`. The `--force` requirement makes the
  destructive verb explicit.

### `rimsky watch <instance>` — single-pane tail of events + breakpoint hits + state transitions

- **Surface:** CLI.
- **Use case:** during a step-through-with-breakpoints debug session,
  operators want a single moving view of: what frames started, what
  nodes terminated (with outcome), what breakpoint hits are pending,
  whether the instance has terminated. Today this requires three
  separate polling sources (`/events?instance_id=...`, MCP
  `resources/read rimsky://instances/.../breakpoint-hits`, and
  `/instances/{id}` for `terminated_at`) and the operator has to
  diff against the prior snapshot themselves. `rimsky logs <id>`
  exists (per CLI help) but its output volume + format wasn't
  obviously the right shape for this — we didn't test it
  comprehensively, so it may already cover the use case, in which
  case this request is "document `rimsky logs` as the canonical
  watch tool."
- **Suggested shape:** `rimsky watch <instance>` streams a unified
  feed: `[2026-05-28T07:21:16Z] frame.start node=discover-boundary`,
  `[2026-05-28T07:25:06Z] await_async node=discover-boundary
  ack=<id>`, `[2026-05-28T07:25:30Z] breakpoint.hit
  node=discover-boundary bp=<id> hit=<id>`. Exits when the instance
  terminates.

### Combined `rimsky instance status <id>` — single-call snapshot

- **Surface:** CLI / control-api.
- **Use case:** "where's this instance right now" is a frequent
  question during testing. Currently answered by combining
  `GET /instances/{id}` (paused, terminated_at) +
  `GET /nodes/{id}` (per-node state — currently returns 404 in v0.2.1
  on a valid instance ID, separate issue) + `GET
  /events?instance_id=...` (recent activity) +
  MCP `resources/read` (breakpoint hits). 4 requests, 4 different
  shapes. A `rimsky instance status <id>` (or
  `GET /instances/{id}?expand=nodes,events,breakpoint_hits`) that
  returns a single JSON object with all four sections would remove
  the assembly burden from every operator/dashboard.
- **Note:** mostly a CLI sugar request — the underlying endpoints
  exist (modulo the `/nodes/{id}` 404), and a CLI consumer can
  fan-out the calls. But the friction is real and the combined view
  is what humans actually want.

### Validator: surface contract drift as a category, not just per-path errors

- **Surface:** control-api `POST /templates` response.
- **Use case:** templates written against an older rimsky hit a flood
  of per-property validation errors at registration time (in our
  case: 43 items across 4 templates). Each error is technically
  correct but the SHAPE of the drift — "you have `on:state, when:
  failed` in 14 subscriptions, the v0.2.1 form is `type:
  terminal/error/*`" — is invisible in the response. The operator
  has to fix one at a time, register, fail on the next batch,
  repeat. A version-aware classifier that grouped errors by drift
  pattern and pointed at the relevant ADR/spec would be a
  significant DX win. (Adjacent to the `template lint` request
  above but distinct: this is about better error messages even at
  the register step.)
- **Suggested shape:** validation response includes a
  `drift_summary` field alongside `validation_errors`:
  `{ "drift_summary": [{"pattern":"subscriptions.on_when","count":14,"spec_ref":"2026-05-23-signal-taxonomy.md","fix_hint":"replace on:state with type: terminal/success; on:state, when:failed with type: terminal/error/*"}, ...]}`.

## Fixed upstream

_(none yet)_
