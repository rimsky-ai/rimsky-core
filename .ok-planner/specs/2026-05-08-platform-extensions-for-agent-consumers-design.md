# Platform Extensions for Agent-Driven Consumers

Date: 2026-05-08

## Summary

Rimsky's existing primitives — content-addressed templates, reactive cascade,
named locks, scope claims, frame resolution, lifecycle handlers — already
cover most of what a sophisticated agent-driven consumer needs from an
orchestration platform. This spec captures a coherent set of additions that
close the remaining gaps without compromising the platform's domain-agnostic
posture. The work splits across four surfaces: rimsky-platform changes
(foundation, protocols, modeling, control-API), reference-executor changes
(`claude-agent` and new bundled additions), reference-store changes (a new
pluggable blob-spill mechanism), and a new documentation architecture that
gives every shipped component its own doc surface separate from the
orchestrator's.

## Design philosophy: rimsky stays domain-agnostic

Every section in this spec follows one principle: **express domain shape in
terms of rimsky's primitives — nodes, claims, attributes, events, handler
resolves, executors, MCP servers — rather than adding domain-specific
features to rimsky.**

Where a need looks like it wants a new platform feature, the design
re-examines it for whether existing primitives compose to cover it. Most do:
human review collapses into a snooze with a `reason`; auto-repair becomes a
named event from a project-built executor with a declarative handler; prompt
context stores become project-built MCP servers; post-processors become
downstream deterministic nodes; confidence scores become ordinary attribute
fields. The platform's contribution is in each case to provide the small,
generalizable primitive that lets composition happen, not the
domain-specific feature itself.

When a section in this spec lands a new primitive on rimsky-platform, it is
because the primitive is genuinely cross-cutting — it serves many consumers,
not one. When a section lands work in a reference component, it is because
the component is a useful out-of-the-box example of a pattern, not because
the pattern is part of rimsky.

This section is intended to be promoted to a permanent orchestrator concept
page; the spec sections that follow inherit its lens.

## Documentation architecture

A meta-deliverable of this spec is a clean separation between orchestrator
documentation and per-shipped-component documentation. The orchestrator's
doc surface (`docs/concepts/`, `docs/protocols/`, the public glossary, etc.)
documents the platform: cascade, claims, frames, executor protocol, etc. It
does not document the behavior of any specific shipped executor, store, or
MCP server.

Each shipped component gets its own dedicated doc surface, mirroring the
directory layout of the components themselves:

- `docs/executors/<name>/` — per-executor docs. `docs/executors/claude-agent/`
  for the bundled Claude CLI executor, `docs/executors/http-node/` for the
  bundled HTTP executor, etc. Each surface documents the executor's userdata
  schema, configuration, deployment shape, and the patterns the executor
  supports.
- `docs/stores/<name>/` — per-store docs (claim-producer reference impls).
  `docs/stores/postgres/`, `docs/stores/filesystem/`, etc.
- `docs/mcp-servers/<name>/` — per-MCP-server docs for any MCP servers
  rimsky ships as bundled reference components.

The principle is: a shipped component is **a useful default, not part of
rimsky**. Operators who don't run it should not encounter it in
orchestrator-level documentation. Operators who do run it find its docs in
the component's own surface, where everything specific to that component
lives.

Each gap section below carries a `Lives in:` pointer naming the doc surface
that owns the work covered in that section.

## Layered work split

The work in this spec breaks across four layers:

1. **Rimsky-platform** — protocol additions, foundation persistence
   changes, modeling-layer changes, control-API changes. Lives in `protocols/`,
   `foundation/`, `modeling/`, and `cmd/rimsky-control-api/`.
2. **Reference executors** — changes to bundled `executors/claude-agent/`
   and `executors/http-node/`. New bundled executors are out of scope of
   this spec; consumers build project-specific executors as needed.
3. **Reference stores** — additions to `foundation/persistence/` for the
   pluggable blob-spill mechanism, plus new bundled blob backends shipped
   alongside.
4. **Reference MCP servers** — a new `mcp-servers/` directory for bundled
   MCP server reference components, with the control-API shim as the first
   entry.

Each gap section names which layers it touches.

## Cross-cutting protocol additions

These additions serve multiple gaps and are documented together for clarity.

### Capabilities returns a userdata JSON Schema

The executor protocol's `Capabilities()` startup handshake gains a
`userdata_schema: bytes` field carrying a JSON Schema describing the
userdata shape this executor accepts. Rimsky validates incoming template
userdata against this schema:

- **At template registration** — by default. Templates that reference an
  executor whose registered schema rejects the template's userdata fail
  registration.
- **At dispatch** — after substitution resolves any `{{...}}` directives,
  the resolved userdata is re-validated against the same schema. Substitution
  failures or shape mismatches surface as a `Errored { error_class:
  "userdata_validation_failed" }` terminal verdict, routed through the
  node's existing `on_executor_errored` handler.

A startup flag `--ignore-missing-refs` (operator-set, not template-set)
disables registration-time validation for environments where the catalog
state and the template registration are decoupled (e.g. infra-as-code that
registers templates before the operator-managed executor catalog is
provisioned). Dispatch-time validation always runs.

This addition is cross-cutting because every executor benefits from
declaring its userdata shape — `claude-agent` uses it heavily for the MCP
catalog and CLI configuration in the gap #1 section, but `http-node` and
any project-built executor uses the same surface.

**Lives in:** `docs/protocols/executor.md` (orchestrator-level: how the
schema flows through the protocol). Each executor's userdata-schema content
lives in that executor's own doc surface.

### Named events with payloads (executor → graph)

The executor protocol grows a new non-terminal event type alongside
`Heartbeat`:

```protobuf
Event {
  name: string         // domain-meaningful name; declared in Capabilities
  payload: bytes       // opaque to rimsky; subject to substitution by templates
}
```

Executors fire these mid-run, as many times as desired, in addition to
their terminal verdict (`Complete`, `Blocked`, `Errored`, `AsyncAccepted`).
Events are orthogonal to the terminal — a node can fire several events and
then complete normally, or fire an event and then fail.

`Capabilities()` gains a `declared_events: []string` field listing the
event names this executor emits. Rimsky validates that any `on_event`
handlers in templates referencing this executor name an event in this set;
unknown event names fail registration.

The async-callback path supports events as well: the callback body shape
extends from a single terminal verdict to:

```json
{
  "events": [{"name": "rework_requested", "payload": {...}}],
  "terminal": {"type": "complete", "changed": false}
}
```

Events are processed in order before the terminal. This makes the named-event
mechanism available to `AsyncAccepted` executors (the typical shape for
HTTP-callback patterns), not just streaming-stream ones.

**Lives in:** `docs/protocols/executor.md`.

### `on_event` handlers (graph reaction to executor events)

The template DSL grows `on_event:` declarations alongside the existing four
lifecycle-handler slots:

```yaml
nodes:
  - type: my_node
    executor: my_executor
    on_event:
      <event_name>:
        resolve: pass | retry | error | by_changed   # optional; default: do nothing to emitting node
        error_class: <string>                         # required if resolve=error
        invalidate:
          targets: [<node_type>, ...]
          frame: in | next                            # optional; default: next
```

The `resolve` and `invalidate` semantics mirror the existing handler slots.
Handler-emitted invalidates fire orthogonally to the resolve verdict, and
each handler can emit zero or more invalidates.

The four existing lifecycle slots (`on_acquire_unavailable`,
`on_executor_complete`, `on_executor_blocked`, `on_executor_errored`) remain
as DSL surface for clarity. Internally, they are equivalent to reserved
`on_event` entries with rimsky-defined event names and fixed payload shapes.
Implementation detail; not exposed in the DSL.

**Lives in:** `docs/concepts/handlers.md` (extends the existing handler doc).

### Event payload as a substitution source kind

Templates' attribute schemas can pull from event payloads via a new source
kind:

```yaml
attributes:
  schema:
    properties:
      rework_feedback:
        type: object
        source: nodes.<emitter_node>.event.<event_name>.<json_path>
```

Resolution: at dispatch time, the substitution engine looks up the most
recent event matching `(emitter_node, event_name)` from the cascade ledger
and walks the JSON path through its payload. Same byte-walking discipline
as `walkPath` for attribute values; payload bytes are never logged or
inspected outside this resolution.

When the same emitter fires multiple events with the same name in a frame
(or across frames), the most recent emission wins, consistent with how
`source: nodes.<dep>.value.<path>` resolves the most recent committed value.

**Invariant interaction.** Event payloads are inert to rimsky in the same
sense as substituted attribute values: rimsky reads them only via
`walkPath` (the named exception under invariant 11) when resolving
substitution leaves, and never logs, formats, validates, transforms, or
attaches them to traces or errors. No new `@blessed-invariant` is
introduced — event payloads ride on the existing walkPath discipline.
The implementation should annotate the new event-payload resolution code
with `@source: modeling/attribute/substitution.go::walkPath` to make the
provenance explicit.

**Lives in:** `docs/concepts/attributes.md` (extends existing substitution
source kinds).

### `ParkRequested` terminal event and the `parked` node state

The executor protocol grows a fifth terminal event:

```protobuf
ParkRequested {
  reason: string             // required; surfaced in observability
  payload: bytes             // opaque; persisted; passed back as resume_context.payload
  resume_at: timestamp       // optional; foundation sweep wakes time-based parks
  session_token: string      // optional; passed back as resume_context.session_token
}
```

`reason` is a required string — required because operators routinely need
to look at a held frame and know "why is this thing parked." The empty
string is permitted but actively discouraged.

A new node state `parked` joins `fresh / stale / running / failed`. The
state machine grows two transitions:

- `running → parked` on `ParkRequested`
- `parked → running` on resume (time-based or signal-based)

Cascade does not propagate from a parked node. The frame containing a
parked node enters a `held` frame state — the frame cannot `complete`
while any node is in `parked` state. The held frame still respects
`serial_queue` / `coalesce` rules for new invalidates that arrive during
the hold.

**Time-based resume:** a new foundation sweep, `SweepParkedNodes`,
periodically queries for parked nodes where `resume_at <= now()` and
re-dispatches them. Default sweep interval: 30s; configurable.

**Signal-based resume:** signal-based wake is **not** a special endpoint or
a special auth scheme. Rimsky is auth-agnostic at the platform level. A
parked node is woken by anything that produces an invalidate against it,
including:

- Intra-graph: another node's `on_event` handler emits an invalidate
  targeting the parked node.
- Cross-system: an external party with control-API access POSTs to the
  generic node-invalidate admin endpoint (see Cross-cutting control-API
  additions below).

Rimsky's invalidate handler checks the node's current state: if `parked`,
it re-dispatches with `resume_context` constructed from the persisted park
state; if `fresh`, it dispatches fresh. One code path; behavior keyed on
state.

`ExecuteRequest` gains a `resume_context` field carrying the original
`payload`, `session_token`, and `resume_reason` (`deadline_elapsed` or
`external_invalidate`). The original `userdata` and resolved `attributes`
are unchanged across the park boundary.

**Held claim handling:** the original claim acquired at first dispatch is
held across the park, auto-released only on a true terminal verdict
(`Complete` / `Blocked` / `Errored`). Concurrency and exclusivity guarantees
are preserved through the park.

**Phase column and orphan-reaper interaction.** Existing rimsky semantics:
the orphan-claim reaper covers `rimsky_worker_request` rows where
`phase='active'` and the heartbeat is stale (5× heartbeat interval per
invariant 6). Indefinite parks (e.g. waiting on human review for hours or
days) cannot heartbeat for that long, so parked rows must transition out
of `phase='active'` to avoid being reaped.

The lifecycle is: `phase` transitions from `'active'` to a new value
`'parked'` at the moment the supervisor processes `ParkRequested`.
Heartbeating stops when the executor's gRPC stream closes after emitting
`ParkRequested`; the supervisor releases its in-memory dispatch slot.
The orphan-claim reaper is updated to skip `phase='parked'` rows. The
parked row continues to hold its `rimsky_claim_handle` rows
(`is_held`-style retention is decoupled from worker_request phase via
existing FK semantics); claims are auto-released only when the row
transitions to a true terminal phase.

On resume (time-based or signal-based), the supervisor transitions
`phase` back to `'active'`, claims a fresh `claimed_by` (the resuming
supervisor's id), starts heartbeating, and re-dispatches with
`resume_context`. The verify-before-run discipline (invariant 5) applies
the same way it does for first dispatch.

**Watchdog:** an optional per-node `max_park_duration` setting — declared
as a top-level node field in the template DSL, **sibling to** `on_event`
and the existing handler slots (not nested inside any event entry) —
caps how long the node may stay parked. On overrun, the foundation sweep
transitions the node to `failed` with `error_class: "park_timeout"`.
Default: unset (parks may be indefinite).

**Persistence:** `rimsky_worker_request` gains columns: `parked_at`,
`resume_at`, `parked_payload_handle` (resolved through the blob backend),
`session_token`, `parked_reason`. The `phase` column gains the new
`'parked'` value. Survives supervisor restart trivially; any replica can
resume from the persisted state.

**State-machine invariant impact.** Invariant 1 in CLAUDE.md asserts the
state machine "rejects illegal transitions" and is annotated at
`foundation/cascade/state.go`. Adding `running → parked` and
`parked → running` extends the legal transition set. Implementation must
update the `@blessed-invariant 1` annotation to enumerate `parked` as a
fifth state alongside `fresh`/`stale`/`running`/`failed`, and the
corresponding scenario test (`test/scenarios/state_machine_*`) must cover
the new transitions and reject illegal ones (e.g. `parked → running`
under reason other than resume; `parked → failed` only via the
`max_park_duration` watchdog path).

**Lives in:** `docs/concepts/parked.md` (new) and `docs/protocols/executor.md`
(the `ParkRequested` event shape).

## Cross-cutting foundation additions

### Pluggable blob backend (transparent large-attribute spill)

Rimsky's attribute persistence becomes pluggable: small values stay inline
in the row, large values transparently spill to a configured backend, and
the read path is unaware of the difference. Executors and template authors
always operate on attribute values as values, regardless of size.

**Interface (in `foundation/persistence/`):**

```go
type BlobBackend interface {
    Write(ctx context.Context, key BlobKey, bytes []byte) (Handle, error)
    Read(ctx context.Context, handle Handle) ([]byte, error)
    Delete(ctx context.Context, handle Handle) error
}
```

The attribute storage layer wraps writes: values below
`persistence.blob.spill_threshold_bytes` (default 64KB) write inline; above
the threshold, write to the configured `BlobBackend` and store the returned
handle in the column. Reads transparently fetch from inline or from the
backend.

`walkPath` extraction for substitution either lazy-loads ranges from
backends that support range reads, or eager-loads on touch. The choice is
backend-internal. Substitution semantics are unchanged from the consumer's
perspective.

**v1 backends shipped:**

- `inline` — default; no out-of-line storage. Behavior is identical to
  pre-spill rimsky.
- `pg-largeobject` — uses Postgres LOBs. Convenient when the operator
  doesn't want a second storage system.
- `filesystem` — local disk; for single-host deployments. Typically
  mounted as a Docker volume.
- `memory` — in-process; **single-process / dev-only**. Only works in the
  unified `rimsky/all` image. In multi-process deployments the supervisor
  would write a blob the scheduler cannot read; this backend must be
  rejected at startup if the deployment topology is multi-process.
  Documented next to the existing "SQLite is dev-only driver" caveat.

Future backends (s3, gcs, azure-blob, redis, etc.) are operator-implementable
without protocol changes, by implementing the `BlobBackend` interface. v1
explicitly defers them.

**Operator config (`rimsky.yml` `persistence:` block):**

```yaml
persistence:
  driver: postgres
  blob:
    backend: pg-largeobject
    spill_threshold_bytes: 65536
    pg_largeobject:
      schema: rimsky_blobs
    retention:
      orphan_sweep_interval: 1h
      retention_after_unreferenced: 24h
```

**Lifecycle / orphan reaping:** when a `rimsky_nodes` row is deleted or its
attribute value is overwritten, the old blob handle becomes orphaned. A new
foundation sweep `SweepOrphanedBlobs` removes orphans after a retention
window (default 24h, configurable). Same pattern as orphan-claim reaping;
distinct sweep so the cadences can differ.

**gRPC ceiling.** This mechanism handles values that are too large for a
row but fit in a single gRPC `Complete` event (default 4MB; tunable up to
a configured per-process maximum). For genuinely huge outputs (hundreds of
MB, streaming generation), the existing claim-producer write-channel
pattern remains the right tool: the agent acquires a write claim, streams
bytes through it during the run, and emits a small `attributes_delta`
containing the claim handle. That stays an explicit "big-data" choice and
is documented separately from the spill mechanism.

**Cold-read note:** a new `@blessed-invariant` documents that the spill
mechanism is the second named exception to "rimsky doesn't inspect
content," after `walkPath`. Rimsky reads bytes only to (a) walk paths into
substituted attribute values and (b) move them between the inline column
and the backend. No logging, no formatting, no validation beyond the
schema gate.

**Lives in:** `docs/operator-guide.md` (configuration), `docs/concepts/attributes.md`
(consumer-facing semantics), and per-backend pages in `docs/stores/`-style
surfaces — though for `BlobBackend` rather than `ClaimProducer`. These
likely warrant a new `docs/blob-backends/<name>/` surface alongside
`docs/stores/`. (Layout decision: blob backends are logically a foundation
concern, distinct from claim-producers; keeping them in their own surface
preserves the layering.)

### Held-frame and parked-node diagnostic endpoints

Two new control-API endpoints:

```
GET /admin/diagnostics/held-frames
GET /admin/diagnostics/parked-nodes
```

`held-frames` returns frames currently in `held` state with: frame_id,
instance_id, node_id_list (parked or pending nodes), `held_since`
timestamp, and per-node `reason` strings.

`parked-nodes` returns nodes currently in `parked` state with: node_id,
instance_id, `parked_at`, `resume_at` (if set), `reason`, and an
optional filter by reason for targeted querying.

These endpoints are intended for external watchdogs and the bundled
control-API MCP shim (#11) to detect held frames or parked nodes that
have outlived their expected duration and decide on remediation. Foundation
does not auto-remediate held frames or indefinite parks beyond
`max_park_duration` — they are legitimately waiting; the operator's domain
logic decides when "too long" has passed.

**Lives in:** `docs/operator-guide.md` and the orchestrator concepts pages
on frames and parked nodes.

### Max-retries-without-progress cap

The existing error-policy chain (`retry | invalidate(targets) | give_up`)
gains a foundation-level safety cap: if a node fires `retry` more than
`max_retries_without_progress` times in succession with no change to its
`last_outcome`, foundation forces a transition to `failed` with
`error_class: "retry_loop_no_progress"`.

Default cap: 100. Configurable per-deployment (`scheduler.max_retries_without_progress`)
and overridable per-node in the template DSL (`max_retries_without_progress: 0`
disables the cap for nodes that legitimately need long retry sequences).

This is footgun-prevention for misconfigured templates. Templates that hit
the cap should be inspected; the cap exists to bound the worst case.

**Lives in:** `docs/concepts/error-policy.md`.

### Prometheus metrics export

Each rimsky process (`rimsky-scheduler`, `rimsky-supervisor`,
`rimsky-control-api`) exposes a standard `/metrics` endpoint speaking
Prometheus text format. Stdlib-only implementation; no new dependency
beyond what's already in the build.

Initial metric set:

- Counters: dispatches, terminal verdicts by class, errors by error_class,
  invalidates by source kind, claim acquisitions by producer.
- Gauges: nodes by state (per-process), parked nodes by reason, held
  frames count, dispatch queue depth.
- Histograms: dispatch latency, claim-acquisition latency, frame duration,
  parked duration on resume.

Metrics are operator-facing observability. Watchdogs and dashboards (both
project-built and the bundled rimsky dashboard) consume them via standard
Prometheus scraping.

**Lives in:** `docs/operator-guide.md`.

### Generic admin node-invalidate endpoint

A new admin control-API endpoint:

```
POST /admin/instances/{instance}/nodes/{node_id}/invalidate
```

Invalidates the named node. Behavior depends on the node's current state:
if `parked`, treats the call as resume (re-dispatches with `resume_context`);
if `fresh`, dispatches fresh; if `running` or `failed`, the call is
rejected with a clear error.

The endpoint is admin-only, gated by the operator's perimeter auth (no
rimsky-specific auth scheme). It serves as the resume mechanism for
signal-based park wake-up (#3, #4) and as a generally useful operational
hook (#10, #11). It supersedes the more-specialized resume-key /
resolve-review primitives explored earlier in design.

**Lives in:** `docs/operator-guide.md` and `docs/control-api.md` (or
wherever the control-API endpoint reference lives).

## Gap-by-gap design

### Gap 1 — claude-agent userdata schema, MCP catalog, four transports

**The need.** Per-node tool whitelisting and per-node MCP server mounting
in the bundled Claude CLI executor.

**The shape.**

Rimsky-platform contribution: the userdata-schema-in-Capabilities
mechanism (cross-cutting; described above).

Claude-agent contribution: a comprehensive userdata schema declared via
`Capabilities()`, supporting:

- `cli.model`, `cli.system_prompt`, `cli.user_prompt_template`,
  `cli.allowedTools`, `cli.disallowedTools`, `cli.permissionMode`,
  `cli.max_schema_corrections`, `cli.handle_rate_limits` (all already
  partially landed; this spec formalizes the schema)
- `cli.tools` — friendlier list-of-names alias that compiles to
  `cli.allowedTools`
- `cli.mcpServers` — list of MCP server references; each entry is either
  a named ref (`{ref: "project-tracker"}`) resolving against claude-agent's
  startup catalog, or a full inline definition (`{name, transport, ...}`)
  gated by startup policy

**Catalog (claude-agent startup config):**

```yaml
mcp_catalog:
  project-tracker:
    transport: http
    url: https://example.test/mcp
    headers:
      Authorization: "Bearer ${PROJECT_TRACKER_TOKEN}"
  workspace-files:
    transport: stdio
    command: project-mcp
    args: ["--workspace", "/data/workspace"]
    lifetime: per-dispatch
policy:
  allow_inline: false
  allow_modules_from: ["@project-alpha/*"]
```

Catalog entries declare a `transport`. Four transports supported:

- **`http`** — long-running HTTP MCP service; URL + headers; auth via
  startup-time env-var indirection (`${VAR}`). Multiple dispatches share
  the same connection or open per-dispatch sessions, depending on the
  service.
- **`stdio`** — claude-agent spawns the MCP server as a subprocess.
  `lifetime: persistent` reuses the subprocess across dispatches;
  `lifetime: per-dispatch` (default) spawns and tears down per dispatch.
- **`module`** — claude-agent dynamically `import()`s a Node module at
  dispatch time. The module exports `register(server, config)`; `config`
  is a userdata-substituted object the module receives at load time.
  Lifetime is always per-dispatch. Used for in-process tool bundles that
  need direct access to dispatch context.
- **`http-loopback`** — claude-agent imports a Node module the same way
  as `module`, but spins it up as a small HTTP listener on a loopback port
  and points the Claude CLI at the URL. Identical use cases to `module`;
  necessary because the Claude CLI talks the wire protocol rather than
  importing Node modules. Lifetime per-dispatch.

**Three decisions** (resolved during brainstorm):

- Inline declarations: **disabled by default**. Operators opt in via
  `policy.allow_inline: true` in the startup config. Catalog refs are the
  curated path; inline is a dev/exploratory escape hatch.
- Transport split: **all four in-tree** in the single `claude-agent`
  binary. The threat-model concern around dynamic code loading
  (`module`/`http-loopback`) is addressed via the
  `policy.allow_modules_from` allow-list; an empty list disables both
  transports entirely.
- Catalog resolution timing: **registration time by default**, with the
  cross-cutting `--ignore-missing-refs` operator override for environments
  where the catalog is provisioned separately from template registration.

**Operator deployment shape.** For all transports other than `http` (which
is a pure network call), operators extend the base `rimsky/claude-agent`
image with `FROM rimsky/claude-agent:<tag>` plus `RUN npm install` (or
equivalent) to bake in the stdio binaries / Node modules they reference in
their catalog. Standard pattern for container-shipped systems. `http`
transport pointed at a co-located service (sidecar container, same
docker-compose network) sidesteps image extension entirely.

**Lives in:** `docs/executors/claude-agent/`. The userdata schema, catalog
shape, transport semantics, deployment patterns, and security
considerations all live there. The `Capabilities` userdata-schema mechanism
itself lives in `docs/protocols/executor.md`.

### Gap 2 — schema-validated retry on `report_complete`

**The need.** Agent occasionally produces output that fails schema
validation. Re-running the entire agent loop is expensive; corrective
retry inside the same logical run is much cheaper.

**The shape.**

This collapses to a thin extension of claude-agent's existing
resume-with-prompt mechanism.

When the agent calls `report_complete(token, attributes_delta, ...)`,
claude-agent validates `attributes_delta` against the `attributes_schema`
that rimsky sent in `ExecuteRequest`. If the validation fails, claude-agent
reuses the same resume-with-prompt code path it already uses for "agent
exited cleanly without calling report_complete," with the resume prompt
templated as: *"Your `report_complete` call failed schema validation:
{error}. Please correct and call `report_complete` again."*

The corrective retry is bounded by `userdata.cli.max_schema_corrections`
(default 3). On exhaustion, claude-agent emits `Errored { error_class:
"schema_validation_failed" }`, which then routes through the node's
existing `on_executor_errored` handler.

The phase-2 JSON-output-mode-coercion pattern that motivated this gap was a
workaround for streaming-log behavior in the Claude CLI's `--output-format
json` mode. Claude-agent uses MCP tool calls for structured output rather
than CLI flags, so the original constraint doesn't apply. The two-phase
shape is dead.

**Rimsky-platform contribution: none.**

**Claude-agent contribution:** the validate-on-`report_complete` plus
corrective-resume-prompt logic; the `cli.max_schema_corrections` userdata
field.

**Lives in:** `docs/executors/claude-agent/`.

### Gap 3 — snooze (rate-limit-aware deferral)

**The need.** A running node may need to defer its own continuation:
rate-limit hit, quota window pending, downstream API unavailable, scheduled
work-window.

**The shape.**

The `ParkRequested` terminal event, `parked` node state, and resume
mechanics described in the cross-cutting protocol additions section above
cover this gap directly. Time-based resume via foundation sweep;
signal-based resume via the generic node-invalidate admin endpoint;
indefinite parks supported and bounded only by optional
`max_park_duration`.

**Claude-agent contribution:** automatic rate-limit handling. Userdata
gains `cli.handle_rate_limits: true` (default true). When the CLI hits a
429, claude-agent automatically emits `ParkRequested { reason:
"rate_limit", resume_at: <ratelimit_reset_time>, session_token:
<cli_session_id> }`. On resume, it uses `session_token` to resume the same
CLI session. Template authors don't wire any of this up.

**Lives in:** `docs/concepts/parked.md` (orchestrator-level primitive),
`docs/executors/claude-agent/` (auto rate-limit handling).

### Gap 4 — human review

**The need.** Some workflows require human approval, rework feedback, or
rejection before downstream nodes proceed.

**The shape.**

Human review is **not** a rimsky-platform primitive. It is a project
concern, fully expressible via existing primitives:

- An indefinite park (`ParkRequested` with a `reason` like `human_review`,
  no `resume_at`) signals that a node is awaiting external resolution.
- The bundled `http-node` executor or a project-built executor handles
  the init-and-wait-for-callback dance — POSTs to a project-specific
  review-creation endpoint, emits `AsyncAccepted`, awaits a webhook with
  the verdict.
- Verdicts route via the existing terminal-verdict + named-event
  mechanisms: approve → `Complete`, reject → `Errored`, rework → an
  `on_event` handler emitting an invalidate against the upstream producer
  with the rework feedback as payload.
- The reviewer UI is fully external — project dashboards, project notifications,
  project audit trails — none of which are rimsky's concern.

**Documented antipattern:** mid-frame human review serializes any
parallel work that would naturally fan into the same frame, and creates
long-lived held frames that complicate operational reasoning. The
recommended idiom is **post-frame review**: the producing frame runs to
completion; review happens externally; a follow-on graph or instance kicks
off post-review for downstream effects. Frame-blocking review is supported
and works correctly, but should be used sparingly — only when downstream
genuinely cannot proceed safely without approval, rather than when "we'd
like a human to look at this eventually."

**Rimsky-platform contribution: none.**

**Reference-component contribution: none.** The `http-node` executor
already supports the init-and-wait pattern via `AsyncAccepted` (extended
to support events alongside the terminal in the cross-cutting protocol
section).

**Lives in:** orchestrator-level pattern doc on `docs/concepts/parked.md`
naming human-review-as-indefinite-park; documented antipattern on
post-frame review.

### Gap 5 — auto-repair triggering across system boundaries

**The need.** A workflow detects a problem (config validation failure,
ingestion anomaly, schema drift) and wants to fan out to a repair subgraph,
optionally re-firing the originally failing step on success.

**The shape.**

The named-event mechanism + `on_event` handlers (cross-cutting protocol
addition) cover this. Two paths:

**Intra-graph repair:** a deterministic node detects the problem, emits a
domain-specific named event (`config_validation_failed`, etc.) carrying
the validation errors as payload. The node's `on_event.<event>` handler
emits an invalidate targeting the repair subgraph. The repair node's
`on_executor_complete` handler, in turn, emits an invalidate targeting the
original failing node. The cycle terminates because the original
eventually completes without firing its `on_event` handler.

**Cross-system repair:** the consumer's existing pipeline is wrapped as
an executor — the **X-as-executor idiom** documented as a first-class
orchestrator pattern. Validation failures inside the wrapped pipeline emit
named events the same way an in-graph executor would. There is no separate
"external invalidate with payload" control-API back door; consumers either
model their pipeline as an executor (recommended), or use the generic
admin node-invalidate endpoint with the payload staged out-of-band (escape
hatch).

**Rimsky-platform contribution:** the named-event protocol addition,
`on_event` handler DSL, event-payload substitution source kind, and
declared-events validation in `Capabilities`. Already enumerated in the
cross-cutting section.

**Claude-agent contribution: none specific to this gap.**

**Lives in:** `docs/concepts/handlers.md` (the `on_event` DSL),
`docs/protocols/executor.md` (the `Event` wire shape), and a new
`docs/concepts/x-as-executor.md` orchestrator-level pattern doc.

### Gaps 6 + 7 — domain prompt-context stores

**The need.** Few-shot examples accumulating across runs; reviewer
corrections accumulating across rejections; per-domain learnings
accumulating across instances. Read at agent dispatch, written at agent
terminal.

**The shape.**

Both gaps dissolve into existing primitives plus a project-built MCP
server:

- **Reads at dispatch:** the agent calls a tool (`read_examples(...)`,
  `read_learnings(...)`) via its MCP surface. The MCP server backing the
  tools is project-built; storage backend (Postgres table, filesystem,
  vector store, custom DB) is the project's choice.
- **Writes at terminal:** the agent calls `append_*` tools via the same
  MCP server. Or a downstream "archive" deterministic node performs the
  write.
- **Corrections:** flow through the same write path, written by the
  project's review system (which is itself project-built per #4).

The MCP server is wired into claude-agent's catalog (#1) and referenced by
templates' `userdata.cli.mcpServers`. Per the design philosophy, this is a
project concern; rimsky stays out.

**Rimsky-platform contribution: none.**

**Reference-component contribution: none.**

**Anti-patterns called out in documentation:**

- Don't try to encode prompt context in rimsky attributes that persist
  across instances. Attributes are per-instance.
- Don't try to use claim-producers for read-only prompt context. Claim
  semantics coordinate write access; prompt-context reads are plain reads
  and don't need claim ceremony.
- Don't put domain stores in `rimsky.yml`. They are executor-side
  configuration (the executor's MCP catalog), not platform configuration.

**Lives in:** new orchestrator-level pattern doc
`docs/concepts/domain-stores.md`. Per-store implementation details live in
the project's own documentation; rimsky's docs only document the pattern.

### Gaps 8 + 9 — confidence scores and post-processors

**The need.** Per-field confidence values on agent outputs (gap 8); and
deterministic transformations of agent output before persistence (gap 9).

**The shape.**

Both decompose entirely into existing primitives.

**Confidence (gap 8):** an ordinary attribute field, declared in the
template's schema. Rimsky has no opinion on confidence — different domains
use different shapes (probabilities, calibrated logits, ordinal
categories), and baking any of them into the platform would pin all
consumers. Templates that want confidence include it in their schema; the
agent computes it; downstream nodes read it via standard substitution.

Confidence-driven routing uses existing primitives:

- A downstream deterministic node reads upstream confidence, emits a
  named event (`low_confidence_output`), and the node's `on_event`
  handler invalidates a review or remediation node.
- For the simple case "agent produced output but isn't confident in it,"
  the agent emits `Blocked { reason: "low_confidence", payload: {...} }`
  instead of `Complete`. The node's `on_executor_blocked` handler routes
  it. `Blocked` semantics are extended in documentation to cover this case
  explicitly: "use `Blocked` when the agent produced something but
  explicitly chose not to claim success — typically as a routing signal
  for downstream cleanup or human-in-the-loop steps."

**Post-processors (gap 9):** downstream deterministic nodes. The
post-processor "registry" pattern (named transformations dispatched by
type) becomes either:

- A project-built deterministic executor that hosts the project's
  transformations, with `userdata.transform: <name>` selecting which one
  to run.
- The bundled `http-node` executor calling a project-built HTTP service
  exposing each transformation as an endpoint.

Either path uses existing primitives.

**Rimsky-platform contribution: none.**

**Reference-component contribution: none.**

**Lives in:** new orchestrator-level pattern doc
`docs/concepts/deterministic-transformations.md` covering both
post-processors and confidence-driven routing. A short paragraph in the
existing `Blocked` semantics documentation covers agent-self-blocks for
routing.

### Gap 10 — watchdog detectors

**The need.** Periodic detection of stuck or anomalous orchestration
state, with safe automated remediation where appropriate.

**The shape.**

Split by where the concern lives.

**Platform-level concerns** — orchestration-machinery anomalies — are
foundation responsibility:

- Stuck running tasks: covered by the existing heartbeat orphan reaper.
- Orphan pending: covered by the existing `SweepReady`.
- Long parks: covered by the new `SweepParkedNodes` and optional
  `max_park_duration` from gap #3.
- Held frames: surfaced via the new `GET /admin/diagnostics/held-frames`
  diagnostic endpoint (cross-cutting). Foundation does not auto-remediate
  held frames.
- Pathological retry loops: covered by the new
  `max_retries_without_progress` cap (cross-cutting).

**Domain-level concerns** — business-state anomalies (e.g. "ingestion
didn't run after a config promotion," "the queue has been empty too long")
— are project work using existing primitives:

- A project-built **lifecycle-subscriber peer service** subscribes to
  `OnInstanceTerminated` (or other lifecycle events) and checks domain
  invariants. It POSTs corrections via control-API.
- Or a **periodic watchdog graph** — a separate template that runs hourly
  via cron, fans out to per-source health-check nodes, each of which
  queries control-API and emits anomaly events. Anomaly events trigger
  remediation nodes via the named-event mechanism. The watchdog is itself
  a rimsky graph.

**Rimsky-platform contribution:** the held-frame diagnostic endpoint, the
max-retries-without-progress cap, the Prometheus metrics export. All
already enumerated in the cross-cutting foundation additions section.

**Reference-component contribution: none.** Rimsky does not ship a
bundled watchdog runner; consumers build the project-side logic.

**Lives in:** new orchestrator-level pattern doc
`docs/concepts/operational-health.md` covering the lifecycle-subscriber-peer
and watchdog-graph idioms. Foundation-side additions documented in
`docs/operator-guide.md`.

### Gap 11 — MCP shim over control-API

**The need.** External agents (Claude-based ops tools, custom automation)
should be able to drive rimsky operations through the same MCP surface
they use for everything else, rather than directly through REST.

**The shape.**

A new bundled reference MCP server: `mcp-servers/control-api/`. Distribution
tier alongside `stores/postgres/`, `executors/claude-agent/`, etc. Thin
streamable-HTTP MCP service that wraps control-API endpoints as MCP tools.

**Standard tool set (initial):**

- Templates: `template_list`, `template_get`, `template_register`,
  `template_deploy`, `template_undeploy`, `template_deregister`
- Tags: `tag_list`, `tag_set`, `tag_delete`
- Instances: `instance_list`, `instance_get`, `instance_create`,
  `instance_terminate`
- Nodes: `node_get(instance, node_id)`, `node_invalidate(instance,
  node_id)`
- Scheduled-node admin: `force_fire_scheduled(node_id)`
- Diagnostics: `held_frames_list`, `parked_nodes_list`

This stays consistent with Position A (rimsky-platform doesn't know about
MCP). The shim is a **consumer** of the REST control-API; rimsky-platform
stays pure. The shim is bundled the same way `stores/` reference impls are
bundled — useful out-of-the-box, fully optional, separately documented and
versioned.

**Domain-specific MCP shims** (e.g. project-specific operational tools)
stay project-side. A project may ship an MCP server that re-exports some
control-API tools alongside its own domain tools; it is welcome to depend
on or fork the bundled `mcp-servers/control-api/` for the control-API
parts. Same pattern as project-built domain MCP servers from #6/#7.

**Rimsky-platform contribution:** confirming the generic admin node-invalidate
endpoint exists (cross-cutting; same endpoint serves #3 and #11).

**Reference-component contribution:** the new bundled
`mcp-servers/control-api/` server with the standard tool set above.

**Lives in:** new doc surface `docs/mcp-servers/control-api/`. Orchestrator
concept docs reference the pattern but stay agnostic about specific
implementations.

## Testing and conformance strategy

The existing rimsky conformance suites (`rimsky-conformance`,
`rimsky-claim-producer-conformance`, `rimsky-conformance-probe`) cover the
existing executor and claim-producer protocols. The protocol additions in
this spec extend them:

- **Executor conformance** gains tests for: `Capabilities` returning a
  valid userdata schema; declared events in `Capabilities`; emitting
  named `Event` records during a run; emitting `ParkRequested` with valid
  fields; emitting events alongside terminal verdicts via async callback.
- **Foundation persistence tests** gain coverage for: blob-spill round-trip
  (write large value, read back, observe out-of-line storage); orphan
  blob reaping; the `memory` backend rejecting multi-process deployment
  topologies; multi-process spill via `pg-largeobject` and `filesystem`
  backends.
- **Scenario tests** gain coverage for: the `parked` node state and its
  transitions; held-frame semantics; signal-based resume via
  node-invalidate; named-event-driven repair-subgraph cycles;
  `on_event`-emitted invalidates with payload substitution.

A new conformance suite scope: **`rimsky-blob-backend-conformance`** —
small program that validates a blob-backend implementation against the
`BlobBackend` interface contract. Same pattern as the existing
claim-producer conformance suite.

Reference-component tests stay scoped to their component:
`executors/claude-agent/` has its own vitest suite covering the userdata
schema, MCP catalog resolution, transport behaviors, schema-validated
retry, and rate-limit auto-park. `mcp-servers/control-api/` has its own
test suite covering tool dispatch, control-API client behavior, and error
mapping.

The existing testcontainers-based scenario testing pattern (real Postgres
via Docker) extends to scenarios involving the new pg-largeobject backend.

## Migration considerations

This spec is pre-v1; per the project's "break freely" rule, schema
changes are allowed without compat shims. The schema additions
(`parked_at`, `resume_at`, `parked_payload_handle`, `session_token`,
`parked_reason` columns on `rimsky_worker_request`; new attribute storage
columns for blob handles; new `rimsky_review_history` table is **not**
created — review collapsed into snooze) require a fresh dev DB or a
migration for any pre-existing dev installation.

Wire-protocol additions (`Capabilities.userdata_schema`,
`Capabilities.declared_events`, `Event` wire type, `ParkRequested` wire
type, `ResumeContext` field on `ExecuteRequest`, callback body events
field) are additive on the gRPC side. Existing executors that don't
implement them continue working; templates that don't reference them
continue working. New features opt in via template DSL changes.

The bundled-component additions (`mcp-servers/control-api/`, additional
blob backends in `foundation/persistence/`) are net-new code; no
migration impact beyond adding them to the build.

## Open questions deferred to plan-writing

None substantive at the spec level. The following items are
implementation-level decisions appropriate for the plan phase:

- Exact Go package names and import paths for the new
  `mcp-servers/` directory (decision: parallel to `stores/` and
  `executors/`, with a top-level `go.work` entry; or a separate Go module
  per server).
- Concrete schema-migration ordering for the persistence-layer additions.
- Default `spill_threshold_bytes` value tuning based on profiling data
  from the v1 backends.
- Whether `mcp-servers/control-api/` is implemented in Go (consistent
  with the rest of rimsky's bundled components) or TypeScript (consistent
  with `claude-agent`). Likely Go; flagged for the plan phase.
- Specific Prometheus metric names and labels — should follow Prometheus
  naming conventions, with rimsky-specific `rimsky_*` prefix.

These are plan-phase concerns; capturing them here so they aren't lost in
the transition.
