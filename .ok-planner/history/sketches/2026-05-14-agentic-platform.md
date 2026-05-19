# Agentic platform for rimsky

**Date:** 2026-05-14
**Status:** sketch / forward-looking design after detailed discussion
**Companion sketch:** `2026-05-14-data-platform-extensions.md` covers the
data-platform side; this sketch covers the agentic side.

## What this covers

The set of platform additions that make AI agents first-class participants
in rimsky workloads — both as workers (claude-agent-style executors) and as
control-plane participants (lifecycle-subscribers, in-template supervisors).

Today's "agent as just another executor" is conceptually clean but
under-leverages what agents can do. A worker-shape agent: "given this
input, produce that output." A control-plane participant: "watch the run;
understand what's happening; act on the graph itself." The killer-app
pattern moves to the second.

Three deliverables shape this work:

- A **bundled MCP server for the control-api** — the foundation that makes
  every other agentic pattern possible.
- A **bundled knowledge store** — durable cross-instance memory for
  agentic workloads.
- **Worked-example patterns** for two supervisor topologies (external
  lifecycle-agent and in-template supervisor), both built from existing
  primitives plus the additions above.

Plus a small protocol extension for executor-level MCP tool surfaces
bound to claims.

---

# Part 1: control-plane MCP server

Smallest scope of the three; concrete packaging; highest leverage as
enablement work. Almost entirely a translation layer — the control-api
already exists; the MCP server makes it natively LLM-tool-accessible.

## Tools, resources, prompts

MCP has three surface types — tools (callable functions), resources
(referenceable read-only data), prompts (pre-built workflows). All three
apply.

### Tools

Curated, operator-shaped, not "every control-api endpoint exposed
mechanically." LLMs do better with focused tool sets; too many overlapping
tools cause selection confusion. The bundled v1 set:

**Read tools:**

- `list_instances` — filter by template, state, age, instance-key pattern.
- `get_instance` — full state for one instance.
- `list_templates` — registered / deployed templates.
- `get_template_spec` — canonical spec.
- `list_nodes` — all nodes for an instance with their states.
- `get_node` — single node state, lifecycle history, current run.
- `get_events` — events log, paginated, filterable.
- `get_attribute_value` — fetch a specific version. For blessed-typed
  attributes: resolves the handle and returns either the value (small) or
  a description with size + a follow-up resource URI (large).
- `get_lineage` — content lineage walk forward / backward, bounded by
  depth.
- `list_parked_nodes` — `?reason=` filter (per the parked-state extension
  in the data-platform sketch).
- `list_failed_nodes` — diagnostic shortcut.

**Write tools:**

- `invalidate_node` — fire an invalidate against a specific node.
- `wake_parked_node` — invalidate-equivalent semantics, surfaced as a
  distinct verb because operators reason about wake separately.
- `create_instance` — `POST /instances` equivalent.
- `terminate_instance` — `POST /instances/{id}/terminate` equivalent.
- `force_fire_schedule` — bypass cron for one scheduled-node tick.
- `backfill` — schedule a backfill operation over a partition range.

Total: ~13 tools. Enough surface for diagnostic dialogue and corrective
action; small enough to hold in LLM working context without drift.

### Resources

Referenceable read-only data, URI-addressed:

- `rimsky://instances/{id}` — instance metadata.
- `rimsky://instances/{id}/events?since=...` — streamed events resource.
- `rimsky://instances/{id}/nodes/{name}` — node state.
- `rimsky://instances/{id}/assets/{name}/{version}` — asset version
  (handle for blessed types).
- `rimsky://templates/{tag}` — template spec.

Resources let an LLM reference an entity in conversation context without a
separate tool call. Support MCP's resource-update subscription — the
server pushes update notifications when subscribed resources change.

### Prompts

Pre-built operator workflows. V2 only; ship tools and resources in v1:

- "Diagnose instance failure" — preloaded with the failing instance
  context.
- "Plan a backfill" — wizard-shaped for partition-range targeting.

## Transports

Two transport modes:

- **HTTP/SSE** — primary. Long-running server; remote agents connect over
  network. Production deployment shape.
- **stdio** — secondary. Local subprocess for development, ad-hoc agent
  runs, single-operator setups. The agent (e.g., Claude Desktop, a custom
  MCP client) spawns the server as a subprocess.

Both are standard MCP shapes; minimal extra implementation.

## Authentication and authorization

Inherits the control-api's auth surface — whatever rimsky-control-api
uses (API keys, mTLS, OIDC), the MCP server uses the same.

**Tool-level scoping per credential** is non-optional. An MCP server that
exposes `terminate_instance` to a read-only agent is a footgun.
Credentials carry scopes — read-only, full-operator, specific-template-
only — and the server enforces.

This means the MCP server can be deployed safely with multiple agents at
different access levels. A data-engineering agent gets read tools +
backfill; a platform-operator agent gets the full set; an external
monitoring agent gets read-only.

## Audit

Every MCP tool call recorded in the existing event-log surface as a
control-plane action with:

- The credential identity.
- The tool name and arguments.
- The result (or error).
- Timestamp.
- A `source: mcp` marker distinguishing MCP-driven actions from direct
  API calls.

Non-negotiable. Once agents can invalidate nodes and create instances, the
audit trail is critical for compliance and post-mortem.

## Deployment

Bundled server lives at `mcp-servers/rimsky-control-api/`. Single Go
binary; minimal deps; talks to rimsky-control-api over loopback or
configured endpoint.

```yaml
# rimsky.yml
mcp_server:
  control_api_endpoint: http://rimsky-control-api:8080
  bind: 0.0.0.0:9200
  transport: [http_sse, stdio]
  auth:
    credentials_source: vault://...
```

Operators run one MCP server per rimsky cluster they want exposed to
agents. Multi-cluster scenarios deploy multiple servers; one cluster, one
server, one endpoint.

## Usage patterns

The shapes this unlocks:

**Operator-Claude dialogue.** Operator chats with Claude about platform
state. Claude calls read tools (`get_instance`, `get_events`, `get_node`,
`get_lineage`), interprets, explains. Operator authorizes specific
remediations; Claude calls write tools (`invalidate_node`, etc.) under the
operator's permission.

**Automated agent (the foundation for the supervisor patterns in Part 4).**
A continuously-running agent subscribes to lifecycle events via resource
subscriptions; calls read tools on event arrival; decides; calls write
tools (or escalates).

**Batch operator scripting.** An operator writes a one-off MCP-based
script: "for every instance where node X is parked > 1 hour, call
wake_parked_node and log the result." Same MCP server; ad-hoc agent
client.

## Interactions

- **Content lineage** (data-platform sketch): `get_lineage` tool wraps
  the lineage query surface. Agents reasoning about failures naturally use
  lineage.
- **Asset-thinking** (data-platform sketch): tools accept asset-named
  arguments (`backfill asset:parcels`). Asset endpoints surface through
  MCP tools.
- **Verifier executors**: when a quality rule fails, the failure surface
  (which checks ran, what details) is what an agent reads via
  `get_events` to diagnose.
- **Parked-state reasons**: `list_parked_nodes` accepts the `reason`
  filter; the agent can distinguish "rate-limit waits we can ignore"
  from "awaiting-external-signal that's been stuck."
- **Sensors**: agents use `get_events` to see what a sensor observed;
  use `invalidate_node` to force a sensor re-evaluation.

## Open design questions

1. **Pagination on read tools.** `get_events` for a long-running instance
   returns a lot of data. Tools should return paginated results with a
   cursor; the LLM walks the cursor when needed.
2. **Long-form data in tool responses.** A `get_attribute_value` against a
   100MB blob shouldn't return inline. The tool returns a description
   (size, type, sample) and a resource URI; LLM fetches the resource only
   if needed.
3. **Tool-name namespacing.** Default to prefixed (`rimsky_*`) to avoid
   collisions with other MCP servers the agent has loaded.
4. **Server-pushed update events.** Resource subscriptions and event
   streams use MCP's notification mechanism. Backpressure and slow-
   consumer handling need care.
5. **Cross-cluster access.** Agent loads multiple MCP servers (one per
   cluster). Routing is in the agent client; multi-cluster aggregation
   server is a future consideration.

---

# Part 2: bundled knowledge store

A bundled claim producer designed for the access patterns of agent
knowledge — durable cross-instance memory, indexed lookups, mutable
entries with supersession and expiration.

Conceptually: just another store. Templates that want to use it grab a
claim like any other store; the producer is configured in `rimsky.yml`;
the claim acquisition uses the standard verbs. The bundling adds:

- Substrate driver tuned for the access shape.
- Canonical entry shape (provenance, supersession, expiration baked in).
- MCP tool ergonomics for read / write / expire.

## Substrate variants

Two bundled in v1; one deferred:

**`stores/knowledge-fs`** (v1). Filesystem-backed; documents per scope
path; one JSON document per knowledge entry, organized as
`{root}/{scope_path}/entries/{entry-id}.json` plus an index. Suitable for
hundreds-to-thousands of entries per scope. Easy to operate, easy to
inspect (operators can `cat` files), easy to edit directly.

**`stores/knowledge-pg`** (v1). Postgres-backed; one table per knowledge
scope with structured fields plus a JSONB content column. SQL-queryable.
Scales better than filesystem; richer indexing. Suitable for
tens-of-thousands-plus entries per scope.

**`stores/knowledge-vector`** (deferred). Vector-DB-backed (pgvector or
external Pinecone / Weaviate); LLM-distilled knowledge stored as
embeddings; agents query semantically. Complex enough to be its own
bundled shape; defer to v2 unless a consumer pushes.

The filesystem and Postgres variants share scope / claim semantics; they
differ in storage backend. Consumers can swap variants without changing
templates — just operator config.

## Scope shape and namespacing

Default scope path: `knowledge/{template-tag}` — per template,
cluster-wide.

Finer-grained scoping the producer supports:

- `knowledge/{template-tag}/{topic}` — per template, per topic.
- `knowledge/{template-tag}/{dimension}/{value}` — by an application
  dimension.
- `knowledge/global` — cluster-wide knowledge spanning templates.

Templates declare which scopes their agents read / write. Operators can
write directly to scopes via MCP tools.

Scope-conflict matrix:

- `r`/`r` — compatible (concurrent reads).
- `r`/`rw` — compatible (RW with COW; readers see prior version).
- `rw`/`rw` — serialized.

Writes are infrequent (typically one per analysis); contention is minimal
in practice.

## Entry shape

Each knowledge entry is a JSON document with a canonical envelope plus
arbitrary payload:

```
{
  id: "...",
  scope: "knowledge/template-tag/failures",
  topic: "test-adapter-timeout",
  created_at: "...",
  created_by: { kind: "agent" | "operator" | "auto", identity: "..." },
  source_instances: ["instance-id-1", "instance-id-2"],
  confidence: 0.0..1.0,
  expires_at: "..." | null,
  superseded_by: "..." | null,
  content: { ... arbitrary payload ... }
}
```

Canonical fields support queries; `content` is free-form. Provenance via
`source_instances` links to content lineage — the lineage tool can walk
back from a knowledge entry to its originating events.

## Read patterns

Two shapes:

- **Bulk read at instance startup.** An `intake` node in the template
  reads the knowledge claim, gets all relevant entries, makes them
  available as substituted attributes for downstream nodes.
- **On-demand read by an agent during analysis.** An MCP tool exposes the
  knowledge store; the LLM agent calls `get_knowledge(scope, topic_filter)`
  mid-analysis to retrieve relevant prior lessons.

Both work. The second is more flexible (LLM decides what to look up); the
first is more efficient (one read per instance instead of one per
analysis).

## Write patterns

Three shapes:

- **Distilled lessons from an agent.** After analyzing a failure, the
  agent's `distill` node writes a new entry: "Failures of template T at
  node X with error pattern E are typically resolved by approach Y."
- **Operator-direct.** An operator writes via MCP tool call. The MCP
  server's `write_knowledge` tool fronts the producer.
- **Auto-distillation from a decision log.** A periodic distillation node
  reads the agent's decision-log asset, identifies patterns, writes
  summary entries. Higher-order learning.

Each shape: `rw` claim on the knowledge scope; write structured entry;
commit.

## MCP tool integration

The MCP server (Part 1) exposes knowledge as tools and resources:

- Tool: `get_knowledge(scope, topic_filter, limit)` — retrieves matching
  entries.
- Tool: `write_knowledge(scope, topic, content, confidence?)` — writes a
  new entry.
- Tool: `expire_knowledge(entry_id, reason)` — marks an entry expired.
- Resource: `rimsky://knowledge/{scope_path}` — references the scope as
  readable data.

Tool scoping matters: write tools should only be granted to credentials
authorized for the corresponding scope. The supervisor agent's credential
gets write access to scopes it's responsible for; operator credentials
get broader write access.

## Bootstrapping and lifecycle

**Empty start.** First instance of a new template — knowledge store is
empty. Agent uses defaults / system prompts. Knowledge grows organically.

**Migration / seeding.** Operators can seed the knowledge store with
curated entries before the first instance runs.

**Knowledge invalidation / expiration.** Entries can be marked expired
(out of date because upstream fixed a bug, schema changed) or superseded
(a newer entry replaces an older one). Agents reading consider these and
filter.

**Garbage collection.** Stale entries pruned via operator action or
retention policy.

## Open design questions

1. **Structured vs. free-form payloads.** Free-form `content` by default;
   consumers can introduce schemas per topic when wanted. Producer doesn't
   enforce shape.
2. **Versioning of entries vs. immutability.** Mark-superseded only;
   preserves the audit trail. Operators can hard-delete via direct
   substrate access if needed.
3. **Contradicting entries.** Producer surfaces what's stored; doesn't
   curate. Consuming agent reasons about contradictions (confidence
   scores, recency, source).

---

# Part 3: producer userdata validation

A small optional extension to the ClaimProducer protocol that recovers
template-registration-time validation **without rimsky learning about
producer-specific surfaces**.

Falls out of the discussion about MCP-tool bindings at the executor level
(Part 4). Has general utility — applies to any producer with a non-trivial
userdata convention, not just MCP-tool advertisements.

## The extension

```protobuf
service ClaimProducer {
  // existing: Open, Commit, Abandon, Release, Capabilities

  // new — optional, opt-in
  rpc ValidateClaimantUserdata(ValidateClaimantUserdataRequest)
    returns (ValidateClaimantUserdataResponse);
}

message ValidateClaimantUserdataRequest {
  bytes node_userdata = 1;          // opaque to rimsky
  repeated ClaimBinding bindings = 2;
}

message ClaimBinding {
  string alias = 1;
  string selector = 2;
  string intent = 3;                // r | rw
}

message ValidateClaimantUserdataResponse {
  bool valid = 1;
  repeated ValidationError errors = 2;
  repeated ValidationWarning warnings = 3;
}
```

## How rimsky uses it

At template registration:

1. Group claims by (producer, node).
2. For each (producer, node), call `ValidateClaimantUserdata` if the
   producer advertises it.
3. Pass the full opaque node userdata + the producer's bindings for that
   node.
4. Surface errors at registration; reject the template if any.

Total addition: one optional RPC. The existing template-registration
validation pipeline gets one more step; no other surface area changes.

## Why this preserves `@blessed-invariant 11`

The invariant says **rimsky** doesn't inspect, parse, substitute, or
validate userdata. The producer is a different service. Rimsky forwards
opaque bytes from the template to the producer and forwards the
producer's verdict back. Rimsky never looks inside the userdata; the
producer does, because the producer is the entity that knows what
conventions its consumers follow.

Same kind of pass-through as today's quality-rule evaluators — producer-
specific parsing, producer-implemented; rimsky orchestrates the gate
without learning the grammar.

## What gets sent

The producer sees the full opaque node userdata. The bindings tell it
which aliases concern it. The producer extracts the slice it cares about
(e.g., a `mcp_servers:` block, or whatever convention it documented) and
validates.

The full-userdata visibility lets producers cross-validate against other
parts (e.g., "if you have `materialization: append` declared elsewhere, my
claim must be `rw` and your idempotency-key field must be set"). Rimsky
doesn't have to know about these cross-validations.

## Multi-producer and multi-alias

A node with claims against multiple producers gets the RPC called once
per producer. Each producer sees the same full userdata; each validates
independently; any rejection fails registration.

A node with multiple claims against the same producer gets one call with
all bindings listed. The producer validates the full userdata in the
context of all its aliases.

## Failure modes

- **Strict mode**: registration fails. "Can't validate; can't register."
- **Permissive mode** (default): registration proceeds with a warning.
  "Validation skipped; the template's claim-userdata correctness is
  unchecked."

Configurable per-cluster. Permissive default because producers are out-
of-process services that can be temporarily unavailable, and refusing all
registrations during an outage is a worse experience than letting them
through with a flag.

## Result caching

Templates content-addressed; validation result is a function of
`(template_hash, producer_endpoint, producer_capabilities_hash)`. Same
triple → same result. Cache validation outcomes for repeated registration
attempts. Not needed v1; nice-to-have when validation is expensive.

## What this enables

Any producer-specific userdata convention earns registration-time
validation:

- **Knowledge store**: validates `mcp_servers:` blocks reference real
  tools, write tools have `rw` claims, etc.
- **Future blessed-substrate producers**: validate substrate-specific
  config (Parquet writer options, PostGIS spatial-index hints, etc.).
- **Consumer-specific producers**: validate domain conventions.

Generalizes well. Same protocol surface; producer-specific semantics.

## Discipline

Rimsky validates structure that's part of its own protocol surface
(template shape, attribute schemas, claim declarations, executor
userdata_schema). Producers validate userdata conventions their consumers
follow when claiming against them. Both kinds happen at template
registration; different layers enforce.

Keeps rimsky simple (no growing knowledge of MCP, tool surfaces, etc.)
while giving operators registration-time error detection.

---

# Part 4: executor-level MCP surfaces

The protocol for "an executor's LLM has MCP tool access to substrates the
node claimed." Not a new primitive; a convention layered over existing
ones.

## The principle

`@blessed-invariant 11` keeps rimsky out of executor-internal concerns.
The claim's address is the boundary; what's behind the address is the
producer's choice; what the consumer does with the address is the
consumer's responsibility.

Producers can expose any service surface — MCP server, gRPC, REST, raw
substrate handle. Rimsky doesn't know about any of them. The MCP-tool
case is one example of producers exposing service surfaces. The same
pattern applies generally.

## Mechanism

The producer's `Open` returns the address. Rimsky propagates the address
through standard claim-handle surface in `ExecuteRequest`. The executor
sees it two ways:

- **Via the claim handle in `ExecuteRequest`** — executor reads
  `claim_handles[i].address` directly. Standard rimsky machinery.
- **Via attribute substitution** — template declares an attribute with
  `source: "{{claim.agent_memory.address}}"`; rimsky substitutes
  (attribute substitution is the proper boundary; userdata is inert); the
  executor reads the substituted value.

The executor's userdata carries whatever shape the executor expects —
including templated MCP-tool configurations. The executor's runtime
substitutes from its own claim-handle context for itself. Rimsky doesn't
substitute into userdata; the executor's SDK can, because that's the
executor's own runtime concern.

```yaml
nodes:
  - type: analyst
    executor: claude-agent
    stores:
      - { name: knowledge-base, alias: agent_memory,
          selector: "knowledge/x", intent: rw }
    userdata:
      system_prompt: "..."
      mcp_servers:
        # opaque to rimsky; claude-agent's runtime substitutes from its own context
        - name: knowledge
          endpoint: "{{claim.agent_memory.address}}"
          tools_to_expose: [knowledge_search, knowledge_write, knowledge_get]
```

Rimsky reads `stores:`, acquires the claim, hands the address to the
executor in `ExecuteRequest`. The executor's SDK reads userdata, sees
`mcp_servers`, substitutes from its own claim context, sets up its MCP
client. Rimsky never looks at `mcp_servers`; substitution inside userdata
happens inside the executor's process; no inertness violation.

## Topology

Two valid topologies; producers choose per-tool:

**Topology A: producer hosts the MCP server.** Each store exposes an MCP
endpoint. Executors connect to the producer's MCP endpoint to call tools.
The producer's MCP server validates that calls correspond to active claims
this executor's supervisor has acquired on its behalf.

**Topology B: executor hosts the MCP surface; producer documents tool
shape.** The executor (via its SDK) hosts an embedded MCP server with stub
tools that translate MCP calls into standard claim-protocol operations
against the producer.

Topology B is the default for standard CRUD-ish tools — the SDK ships
shared implementations of `get`, `set`, `search`, `list`. Topology A is
for producer-specific tools with sophisticated semantics (vector search,
query planning) that can't be SDK-shimmed.

## Authorization

For Topology B: authorization is already gated by the claim machinery at
acquisition time. The executor's embedded MCP surface only contains tools
backed by claims this dispatch actually holds. No new runtime auth path.

For Topology A: rimsky issues a short-lived claim-bound token at dispatch;
the producer validates the token against its own record of issued claims.
This is the runtime auth surface required for Topology A; producers using
it implement the validation.

## Claim-scoped tool surface

The crucial property: **what an LLM can access via MCP is bounded by the
claims its node holds**. An LLM in a node with `stores: [{ ..., selector:
"knowledge/zoning-source", intent: rw }]` can call `knowledge_search`,
`knowledge_write` against `knowledge/zoning-source` — and only that scope.
It cannot call tools backed by scopes the node didn't claim.

This makes the claim machinery the authoritative access-control mechanism
for executor-level MCP. Templates' `stores:` declarations are visible
access boundaries; drift between intent and reality is impossible because
the surface is derived from the claims at dispatch.

## Validation

The MCP-tool-binding shape inside userdata gets validated at template
registration via `ValidateClaimantUserdata` (Part 3). Producers that
expose MCP tools implement the validation method to verify:

- Referenced tool names exist in the producer's surface.
- Write tools are bound only to `rw` claims.
- Argument templates match declared tool input schemas.

Rimsky doesn't learn what MCP is; the producer validates its own
convention; mismatches surface at registration time.

## Two MCP access paths summarized

- **Executor-level MCP** (this part): the LLM inside an executor has tools
  available during its execution. Tools front the substrates the node
  claimed. Claim is the authoritative access boundary.
- **Control-plane-level MCP** (Part 1): the Frame-A bundled MCP server.
  Operators and supervising agents have tools to the rimsky control-api
  and platform-wide resources. Operator credentials govern; not bound to a
  specific node's claims.

Both shapes have their place. Executor-level is what most "agent uses
store" patterns want. Control-plane is for platform-supervisor and
operator-dialogue cases that don't fit the node-claim model.

---

# Part 5: supervisor patterns

Worked examples for two topologies of "agent watching rimsky workloads."
Both use the primitives from Parts 1-4 plus existing rimsky machinery; no
new primitives.

## Topology 1: external lifecycle-agent

A rimsky template + instance that subscribes to lifecycle events of
another rimsky (or itself) and acts on them via MCP.

### Composition

```
┌─────────────────────────┐         ┌──────────────────────┐
│  watched rimsky         │ ──→     │  lifecycle-agent     │
│  control-api + events   │  events │  rimsky instance     │
└─────────────────────────┘         │                      │
                                    │  sensor → analyze    │
┌─────────────────────────┐         │     → execute        │
│  watched rimsky         │ ←──     │     → record         │
│  MCP server             │   MCP   │                      │
└─────────────────────────┘         └──────────────────────┘
```

The lifecycle-agent is **itself a rimsky template + instance**, running on
the same rimsky cluster it watches (or a separate supervisor cluster for
high-availability deployments). Uses existing primitives:

- **Sensor** that subscribes to the watched rimsky's lifecycle events
  (a bundled `sensor-rimsky-lifecycle` flavor; one of the bundled sensor
  set from the data-platform sketch).
- **Claude-agent executor** for analysis nodes.
- **MCP-call executor** (thin bundled wrapper, or just `http-node` against
  the MCP HTTP transport) for taking actions.
- **Durable asset** (`decision_log`) capturing every decision with
  append materialization.

### Reference template

```yaml
template: lifecycle-agent

assets:
  decision_log:
    type: table
    materialization: append
    lifetime: durable
    schema:
      columns:
        event_id, event_type, instance_id, llm_analysis,
        proposed_action, executed, action_result, timestamp

nodes:
  - type: watch-lifecycle
    executor: sensor-rimsky-lifecycle
    schedule: { cron: "*/1 * * * *" }
    userdata:
      target_rimsky: ${WATCHED_RIMSKY_ENDPOINT}
      subscribed_events: [OnInstanceTerminated, OnInstanceCreated]
      filter: { outcome: failed }
    on_event:
      event_observed:
        invalidate:
          targets: [analyze]
          fan_out_value: "{{event.payload}}"

  - type: analyze
    executor: claude-agent
    dependencies: []
    stores:
      - { name: knowledge-base, alias: agent_memory,
          selector: "knowledge/watched-templates", intent: rw }
    userdata:
      system_prompt: "You are a rimsky platform supervisor. ..."
      mcp_servers:
        - name: control_api
          endpoint: ${WATCHED_RIMSKY_MCP_ENDPOINT}
          tools_to_expose: [get_instance, get_events, get_node, get_lineage,
                            list_parked_nodes]
        - name: knowledge
          endpoint: "{{claim.agent_memory.address}}"
          tools_to_expose: [knowledge_search, knowledge_write]
      max_turns: 10
    on_executor_complete:
      invalidate: [execute, record]

  - type: execute
    executor: mcp-call
    dependencies: [analyze]
    userdata:
      action: "{{deps.analyze.value.proposed_action}}"

  - type: record
    executor: append-to-asset
    dependencies: [analyze, execute]
    outputs:
      writes_to: decision_log
    userdata:
      row:
        event_id: "{{deps.watch-lifecycle.event.event_observed.event_id}}"
        llm_analysis: "{{deps.analyze.value}}"
        executed: "{{deps.execute.value.success}}"
```

A real rimsky template doing the supervisor work. Loops via cascade-driven
re-firing as new events arrive. State is durable in the `decision_log`
asset; cross-event pattern detection becomes queries against that asset.

### Why this shape

**Dogfooding.** Rimsky operates rimsky with its own primitives. Major
credibility signal — "you can run a real production workload on this"
includes the running of the watcher of the production workload.

**Uniform observability.** The agent's decisions visible in the same
dashboards, lineage queries, audit logs as any other workload. Operators
don't learn two systems.

**Versioning via templates.** "Update the agent's strategy" is a template-
tag movement; same workflow as updating any other template.

**Composition with the wishlist.** Benefits from partitions (decision log
partitioned by date), verifiers (quality checks on the agent's own
outputs), content lineage (every decision recorded with full provenance),
sensors, fan-out, knowledge store — without bespoke work.

### Bootstrap consideration

If rimsky-control-api fails, the agent watching it also fails. Three
deployment shapes address this:

- **Single cluster, agent-as-template** (default). Agent runs on the same
  rimsky cluster. Acceptable for most consumers — if control-api is down,
  you have a paging-grade incident regardless.
- **Two-cluster, mutual watch.** Production cluster + supervisor cluster;
  each runs an agent template watching the other. HA-sensitive deployments.
- **External peer service** (binary instead of template). Bootstraps
  independently. Loses dogfooding wins; gains hard isolation. For
  consumers who can't accept any bootstrap circularity.

The bundled reference is the template. The other shapes are documented;
the external binary can be derived from the template if a consumer needs
it.

## Topology 2: in-template supervisor

A node within a template that supervises the template's own instance.
Same primitives, different scope.

### Pattern

A supervisor node:

- Has `dependencies: []` (no spine dependency).
- Reachable only via `on_executor_errored: { invalidate: [supervisor] }`
  from other nodes in the template.
- Has `on_executor_complete: { resolve: never_propagate }` so it doesn't
  cascade downstream.
- Holds MCP tool bindings for control-api tools (`get_node`,
  `get_attribute_value`, `get_events`) scoped to its own instance.
- Holds knowledge-store claims for distilled lessons.

### Use case

For per-instance localized concerns that don't generalize across
instances. Repair, recovery, escalation that's specific to the template's
shape.

### Reference template

```yaml
template: data-pipeline-with-supervisor

nodes:
  - type: load
    executor: http-node
    on_executor_errored:
      resolve: error
      invalidate: [supervisor]
    # ... rest of node spec

  - type: transform
    executor: http-node
    dependencies: [load]
    on_executor_errored:
      invalidate: [supervisor]
    # ...

  - type: supervisor
    executor: claude-agent
    dependencies: []
    stores:
      - { name: knowledge-base, alias: instance_lessons,
          selector: "knowledge/{{template-tag}}", intent: rw }
    userdata:
      system_prompt: "You are a supervisor for this data-pipeline instance. ..."
      mcp_servers:
        - name: control_api
          endpoint: ${RIMSKY_MCP_ENDPOINT}
          scope: this_instance_only
          tools_to_expose: [get_node, get_attribute_value, get_events,
                            invalidate_node]
        - name: knowledge
          endpoint: "{{claim.instance_lessons.address}}"
          tools_to_expose: [knowledge_search, knowledge_write]
    on_executor_complete:
      resolve: never_propagate
```

### When to use which topology

- **External lifecycle-agent** when you want one agent overseeing all
  instances, with cross-instance memory and pattern detection.
- **In-template supervisor** when each instance has localized concerns
  that don't generalize across instances, and the supervisor is part of
  the template's intended shape.
- **Both layered** when you want platform-wide oversight (external) plus
  instance-local repair (in-template).

---

# Part 6: safety patterns

The critical bit. An autonomous agent with control-api write access can do
harm. Both supervisor topologies (and any other agentic deployment using
the MCP server) need these disciplines.

## Scoped credentials

The agent service gets a credential allowing specific tools only. Default
scope: read everything; write only `invalidate_node`, `wake_parked_node`,
`force_fire_schedule`. Excluded by default: `terminate_instance`,
`create_instance` (except for declared repair templates),
`update_userdata_overrides` (when that surface exists).

## Rate limits

The agent cannot invalidate the same node more than N times in M minutes.
Cooldown after consecutive same-action decisions. Prevents loops where the
agent's intervention causes a new failure that the agent then interprets
as needing more intervention.

## Dry-run mode

New deployments default to "log decisions; do not execute." Operators
watch the decision log for a calibration period, then promote to "execute"
mode once decision patterns look right. Promoting per-action-class so
operators can enable `invalidate_node` confidently before enabling
`create_instance`.

## Confirmation gates

Some action classes route through human confirmation. The agent proposes
the action via the MCP server's `propose_action` tool (a bundled MCP tool
wrapping this); posts the proposal to a Slack / email / Discord channel;
an operator approves; only then does the agent execute.

## Audit

Every decision logged: what the LLM saw, what it decided, why (reasoning
extracted from the LLM response if available), what it did. Persistent.
Reviewable. Non-skippable.

## Escalation paths

When the LLM is uncertain (low-confidence, conflicting signals, novel
failure shape), it escalates by posting to an operator channel rather than
acting. The system prompt makes "escalation is a valid action; uncertainty
is not weakness" explicit.

## Configuration shape

```yaml
safety:
  per_instance_write_limit:
    invalidate_node: 5 per hour
    wake_parked_node: 3 per hour
  cooldowns:
    same_node_consecutive_invalidate: 15m
  confirmation_required:
    - create_instance
  escalation_channel: slack://incidents
  mode: dry_run                                # log decisions; don't execute
audit:
  decision_log_path: /var/log/rimsky-supervisor/decisions.jsonl
```

Configuration opinionated for safety. Defaults make the agent safe at the
cost of being passive; operators progressively unlock more autonomous
behavior.

## Open design questions

1. **Replay / dry-run testing.** Operators want to replay historical
   failures through a new agent version (or new prompt) without affecting
   production. Replay mode reads a recorded event, runs the LLM, surfaces
   the decision, doesn't execute.
2. **Model versioning + reproducibility.** Decision log records model
   name + version + prompt. Reproducing the same decision requires that
   combination plus the input state.
3. **Cross-agent coordination.** Multiple agent services for the same
   rimsky deployment (triage agent + investigation agent). Each subscribes
   to a distinct event subset; coordination is configuration, not protocol.

---

# Phasing across the suite

The agentic work stages roughly:

**Stage 1 — design lockdown.** Pre-implementation alignment.

- Bundled MCP server tool / resource surface and protocol.
- Knowledge-store entry shape, scope conventions.
- `ValidateClaimantUserdata` protocol extension.
- Executor-level MCP surface convention; userdata-templating-by-executor
  pattern.
- Supervisor template patterns; safety configuration shape.

**Stage 2 — MCP server first ship.**

- Bundled server binary at `mcp-servers/rimsky-control-api/`.
- The curated 13-tool set.
- The five resource URIs with subscription support.
- HTTP/SSE + stdio transports.
- Auth + tool-scoped credentials.
- Audit-log integration.
- Worked example: `docs/agents/examples/mcp-operator-dialogue.md`.

**Stage 3 — `ValidateClaimantUserdata` extension.**

- Proto change, capabilities update.
- Rimsky-side template registration integration.
- Documentation of producer-side implementation patterns.

**Stage 4 — knowledge store first ship.**

- `stores/knowledge-fs` bundled producer.
- Standard entry shape.
- Scope conventions.
- MCP tool integration for read/write/expire.
- Conformance via `cmd:rimsky-claim-producer-conformance`.

**Stage 5 — supervisor templates worked example.**

- `sensor-rimsky-lifecycle` bundled executor (part of bundled sensor set
  from the data-platform sketch).
- `mcp-call` bundled executor (thin wrapper; may just be `http-node` if
  ergonomics work).
- `append-to-asset` bundled executor (falls out of materialization
  strategies).
- Reference template at `templates/agents/lifecycle-agent.yaml`.
- Worked example: `docs/agents/examples/lifecycle-supervisor.md` covering
  both external-template and in-template-supervisor topologies.
- Safety pattern documentation.

**Stage 6 — `stores/knowledge-pg`.** Postgres variant for production
scenarios.

**Stage 7 — executor-level MCP surface integration.**

- Per-language SDK support for `mcp_servers:` userdata templating with
  claim-address substitution.
- `claude-agent` refactor to use the SDK's MCP integration.
- Documentation of executor-level MCP authoring patterns.

**Deferred:**

- Pre-built MCP prompts (Stage 2+ ergonomic improvement).
- Multi-cluster routing in one MCP server.
- `stores/knowledge-vector` (vector-embedding-backed).
- Auto-distillation primitives.
- Multi-tenant scoping for knowledge.
- Self-tuning / RL-shaped learning over decision history.

Each stage is reviewable independently. Stages 1-5 form the credible-
agentic-baseline (MCP server + knowledge store + worked supervisor
patterns). Stages 6-7 round out and harden.

---

# Open design questions across the suite

1. **Tool granularity.** The 13 control-api MCP tools cover diagnostic
   dialogue and corrective action. Real consumer experience may show gaps
   (or surplus). Adjust per usage data after Stage 2 ships.
2. **MCP server multi-cluster.** One server, one cluster is the v1 shape.
   Aggregating across clusters comes if real consumer demand emerges.
3. **Knowledge-store substrate evolution.** Filesystem and Postgres
   variants share semantics; consumer experience may reveal needs for
   vector or graph variants. Defer; let the bundled set earn its growth.
4. **Producer-userdata-validation cache invalidation.** Templates content-
   addressed; producer capabilities change less frequently than templates.
   Cache strategy needs thought when validation becomes a perceptible
   registration cost.
5. **Executor-level MCP topology preference.** Most v1 use cases fit
   Topology B (executor-embedded). Topology A (producer-hosted) earns its
   place when producer-specific tools have sophisticated semantics.
   Decide per-tool when each is appropriate.
6. **Safety-pattern adoption curve.** Dry-run-default is conservative.
   Operators may push back wanting more aggressive defaults. The
   conservative posture earns trust; loosening comes from consumer
   experience.
7. **Cross-instance pattern detection.** The supervisor template's
   `decision_log` asset enables this in principle; specific patterns
   (recurring failure shapes; template-wide drift) become consumer-
   specific analysis nodes. Bundle as a pattern; don't bake into the
   reference supervisor.
8. **LLM context budget management.** Each MCP-aware agent call has cost +
   latency + context-window limits. Agent service has to decide how much
   of an instance's history to load before making a decision. Adaptive
   context (start small, expand on demand) is the right pattern; specific
   tuning is consumer-specific.
