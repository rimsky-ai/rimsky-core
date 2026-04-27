# Node Graph Design

Conceptual reference for rimsky v1 (Go). Rimsky is a project-agnostic reactive node-graph orchestration platform: a graph of nodes that communicate via two message types (`invalidate`, `recalculate`), operate on operator-configured stores via lock-and-commit semantics, and execute work through external executors speaking a well-defined protocol.

This document is a design reference, not a spec or plan. It captures the conceptual model, the contracts, and the reasoning. Implementation shape (package layout, process model, storage) lives in `architecture.md`; wire-level contract details live in `protocol.md`.

An appendix at the end (§13) notes the relationship to rimsky's TypeScript predecessor — the rename there was mechanical, the concept did not change.

---

## 1. Motivation

Orchestrators in the common market are one of two shapes: forward-only pipelines (Airflow, Argo, Dagster) that handle failure by retry-or-abort, or workflow DSLs (Temporal, LangGraph) that let you write arbitrary control flow at the cost of giving up a declarative shape. Neither has a good story for the pipeline that drifts: a pipeline whose upstreams change shape without notice, whose downstream observations reveal problems that should invalidate something three steps back, whose recovery is not "retry the failing step" but "re-run the upstream that produced the bad configuration."

Rimsky's observation is that the forward-only abstraction is wrong for this class of problem. The domain has drift, recovery, rollback, and probing as first-class concerns. The control plane should too.

Three observations shape the model:

1. **The dependency graph is acyclic; execution is not.** Who-depends-on-whose-data is a DAG. But execution can move backward: a downstream failure can invalidate an upstream, causing it to re-run. Forward execution is data flow; backward execution is repair cascade.
2. **Everything that does work is a node.** Data-producing work is a node. Scheduled fan-out is a node. Agentic work with LLMs is a node. External-event bridges are nodes. There is no privileged controller class ("the scheduler," "the repair agent") sitting above the graph. The scheduler is infrastructure; any reasoning lives in a node like any other.
3. **Data lives in stores, not node internals.** A store is an operator-configured backend (filesystem, claim-store, future database/S3/git). Nodes acquire locks on regions of a store, read and write through native handles, and commit on success. Lock acquisition is the unit of "exclusive access"; locks unify what older designs split into resource ownership, dispatch claims, and concurrency tags. This separates work logic from data backend and makes consumption discoverable without coupling consumer to producer.

The behavioral vocabulary is small:

- **2 message types**: `invalidate`, `recalculate`.
- **3 error actions**: `retry`, `invalidate(targets)`, `give_up` — chainable as an ordered fallback.
- **4 node states**: `fresh`, `stale`, `running`, `failed`.
- **Per-node error classes**: each node defines its own taxonomy; repair policies match against it.

This vocabulary is enough to model declarative pipelines, reactive cascades, agentic reasoning loops, and scheduled fan-out patterns without enlarging.

---

## 2. Core model

The system is a **graph of nodes** that communicate by **messages**, operating on **stores**, executing work through **executors**.

- **Node** — a graph-vertex declaration. State + declared dependencies + message handlers + per-error-class repair policies + the stores it touches (and the regions it reads/writes on each) + the locks it acquires + the attributes shape it produces and consumes + (if executor-backed) the executor it dispatches to.
- **Message** — `invalidate` or `recalculate`. The only two types.
- **Store** — an operator-configured data backend (filesystem, claim-store, future database/S3/git). Has regions, locks, and commit semantics. Templates reference stores by name; resolution mirrors executor name resolution.
- **Lock** — a node's exclusivity claim on a named scope, a region of a store, or a claimed item. Held for the duration of one execution.
- **Attributes** — a node's per-run typed data: source-driven properties pre-populated at dispatch (from upstream attributes, claim payloads, or instance params) plus executor-populated properties written during the run.
- **Executor** — a peer service that speaks the node-executor protocol. The orchestrator dispatches node work to an executor; the executor returns a result, reports blocked, errors, or hands off asynchronously.

Rimsky is built from three architectural collections (each developed, versioned, and deployed independently once separated):

- **Orchestrator.** The node-graph runtime: state machine, scheduler, supervisor, control API, dispatch queue, lock-holder table, attributes table, storage. Knows nothing about LLMs, HTTP, or any specific work domain.
- **Store library.** Implementations of the `Store` interface. Each implementation decides what its regions look like, how locks are acquired, how data is committed, and which capabilities it advertises.
- **Executor library.** Reference executor services that speak the protocol. Executors run as peer services; the orchestrator calls them over the wire.

The orchestrator consumes stores through an in-process Go interface and executors through the wire protocol. Swapping either is a configuration or code-at-main boundary concern, not an orchestrator change.

---

## 3. Nodes

### 3.1 Properties, not classes

A node is described by a set of **properties**, any combination of which may apply. There is no fixed taxonomy of node kinds; a node has whatever properties it needs.

The properties are:

- `executor` — named external executor this node dispatches to. Absent → the node does not do work externally.
- `userdata` — opaque JSON block passed verbatim to the executor. Rimsky does not interpret it (§3.4).
- `schedule` — cron expression (UTC). The scheduler emits `invalidate` to the node when the cron fires.
- `dependencies` — list of sibling node types. Gates execution order; the default `recalculate` handler requires all listed dependencies `fresh` before the node runs.
- `stores` — list of operator-configured stores this node touches, with the regions it `read`s and `write`s on each (or a `claim: true` entry for store-picks-region acquisition).
- `locks` — exclusivity declarations. Named locks (mutex or counting semaphore) are listed here alongside the region/claim locks implied by `stores`.
- `attributes` — per-run typed data shape (JSON Schema). Source-driven properties draw from upstreams, claim payloads, or instance params at dispatch; sourceless properties are populated by the executor (§7, §8).
- `claim_resolutions` — at terminal-leaves of a held-claim subgraph, declares how the held claim is resolved (delete / release-to-back / release-to-head) on this node's terminal outcome.
- `error_types` — per-node error taxonomy with ordered policy chains.

Every combination of these is valid (modulo one constraint, §3.3). In practice, nodes fall into a few recognizable shapes, but these shapes are emergent from the properties a template author chose, not enumerated by a `kind` field.

### 3.2 Node shapes in practice

- **Executor node.** Has `executor` and probably `userdata`. Runs when dispatched; commits attributes (and any store-side writes through write-region locks) on success.
- **Pure-cascade node.** No `executor`. When invalidated and dependencies are fresh, the scheduler instantly transitions `stale → fresh` inline and emits `recalculate` to dependents. Its purpose is to propagate cascade without doing work itself — a join point, a debounce, a graph-shape device.
- **Scheduled node.** Any node with a `schedule`. The scheduler emits `invalidate` to it when the cron fires. Otherwise indistinguishable from any other node; scheduled executor nodes, scheduled pure-cascade nodes, and scheduled nodes with dependencies all compose naturally.
- **Fan-out.** A pure-cascade node with a `schedule`, no dependencies, and many downstream dependents. When the cron fires, the invalidate ripples to every dependent. This is how periodic full-graph refresh is expressed.
- **Agentic node.** A node whose executor happens to run an LLM. "Agentic" is a description of the executor's behavior, not a distinct node property.

The distinction that matters at the contract level is whether the node holds write-region locks on a store. Nodes that write have quality rules and commit-or-reject semantics on those writes; nodes that don't write emit messages (and may still commit attributes) and nothing else.

### 3.3 Constraints

Only one property-combination is invalid: a pure-cascade node (no `executor`) cannot declare `stores[*].write` regions. Pure-cascade nodes emit no data; declaring write authority on a region without a mechanism to produce content for it is ambiguous. Template validation rejects the combination at `POST /templates`.

`userdata` on a node with no `executor` produces a warning, not an error, at template deploy. Opaque fields are not inherently wrong; the warning flags likely authoring mistakes.

### 3.4 Userdata: the opaque block

Every executor-invoking node carries a `userdata` field: an arbitrary JSON blob meaningful to its executor and opaque to rimsky. An HTTP-calling node's `userdata` specifies URL, method, headers, body. An LLM-calling node's `userdata` specifies model, prompt, tool list, result schema. A SQL-executing node's `userdata` specifies statement and bindings.

The orchestrator does not parse, validate, or template-substitute `userdata` — at any depth, at any phase. Its contents reach the executor byte-for-byte as supplied in the template. Substitution syntax (`{params.x}`, `{{deps.x.y}}`) appearing inside a `userdata` value is treated as literal bytes by rimsky; if an executor wants to interpret it, that's the executor's choice. An executor with a schema for its `userdata` is free to validate on receipt and reject with a `Blocked` or `Errored` terminal event.

Per-run data does not live in `userdata`; it lives in `attributes` (§7, §8). `userdata` is purely executor configuration: model, system-prompt reference, tool list, prompt-construction strategy. The executor reads from the dispatch's `attributes` object to obtain per-run inputs (upstream values, claim payloads, instance params); how it composes attributes with `userdata` is the executor's concern (e.g., `claude-agent` exposes attributes as MCP tool inputs; `http-node` puts attributes in the request body).

This opacity is load-bearing: it is what lets rimsky serve every domain without growing a per-domain vocabulary. The cost is that rimsky cannot catch a template author's typo in the userdata block; that's the executor's job.

### 3.5 Node state

A node is always in exactly one of four operational states:

- `fresh` — has completed successfully; no work pending. The steady state.
- `stale` — has been invalidated; scheduled for re-execution when dependencies are fresh. Live regions (and last committed attributes) remain readable until the next successful commit.
- `running` — supervisor has claimed the dispatch and is executing (or waiting on an executor's response).
- `failed` — error policy exhausted with `give_up`. Requires external signal (operator reset or `invalidate`) to re-activate.

Transitions:

- `fresh → stale` — on `invalidate` message.
- `stale → running` — supervisor claims a dispatch row (executor nodes only).
- `stale → fresh` — scheduler inline transition (pure-cascade nodes with deps fresh).
- `running → fresh` — executor returned success.
- `running → stale` — error policy action `retry` or `invalidate(targets)`.
- `running → failed` — error policy action `give_up`.
- `failed → stale` — operator `reset` or `invalidate`.
- any → `fresh` — `invalidate(restore_version)` with store-supported restore (post-v1; v1 stores all advertise `SupportsRestore: false`).

**"Finished" is not terminal.** A node that has completed successfully is `fresh`. It can return to `stale` on invalidation. The only truly terminal state is `failed`, exited only by external intervention.

**The state machine rejects illegal transitions.** In particular, `running → running` under reason `dispatch_claimed` is not silently idempotent — it raises an error. This is the load-bearing invariant against double-execute (see §11.1); any implementation that adds an idempotency short-circuit "for ergonomics" breaks the property.

### 3.6 Probes are write-region-holding nodes, not guards

A probe is a node whose work exercises upstream regions in a realistic scenario. Examples: a `test-adapter` probe uses a config to fetch samples and produces a report; a `probe-ingestion` node exercises a full pipeline at small N before full ingestion commits.

Probes write their own regions — the probe's report is a write the probe holds in its `stores[*].write` declaration, consumed downstream by dependents that read the same region. A probe does work that produces data; that work happens to validate upstream along the way. Its failure path uses the normal error-class → policy chain.

Convention: probe error classes should describe observable facts (`fetch_non_json`, `zero_records_returned`), not interpretations of upstream (`config_is_broken`). Observable facts compose — the same symptom can have multiple upstream causes, and the policy decides how to react.

---

## 4. Stores

Stores are the data layer. A **store** is a deployment-level data backend, configured once by operators in YAML and loaded by control-api and each supervisor at startup. There is no `rimsky_stores` database table — stores are pure runtime objects built from process YAML config; each process has its own `Registry`. Templates reference stores by name; resolution mirrors today's executor name resolution. A supervisor pool's config lists the stores it has access to; dispatch eligibility filters out nodes whose required stores aren't in the local pool, analogous to executor `accepted_executors` filtering.

A store has:

- A name — operator-assigned, used by templates.
- A kind — `filesystem`, `claim_store`, future `database` / `s3` / `git` / etc.
- A region grammar appropriate to the kind (path globs for filesystems; per-claim for claim stores).
- A mode — `direct` (v1), `sidecar` (post-v1), or `versioned` (post-v1).
- A capability set — `SupportsRegionLock`, `SupportsClaim`, `SupportsDiscard`, `SupportsResume`, `SupportsRestore`.

A node's template declares the stores it touches and, on each, the regions it `read`s and `write`s — or asks the store to pick a region by setting `claim: true`.

### 4.1 Pluggable kinds

Rimsky does not specify a single storage model. Instead, each store declares its **kind** by name (e.g. `filesystem`, `claim_store`). A kind is a Go struct satisfying the `Store` interface; it decides what its regions look like, how locks are acquired, how data is committed, how (if at all) it supports resume and restore.

Kinds register themselves at process startup (the orchestrator deployer's `main()` wires them in). A `stores.yml` file referenced by `RIMSKY_STORES_CONFIG` lists each operator-named store with its kind and kind-specific config. Templates reference stores by name and supply per-node region declarations.

v1 ships two reference kinds:

- `filesystem` — bytes on a configured root directory; regions are path globs (`section-a/**`, `shared/glossary.md`); handle is an absolute directory path. Direct-mode only in v1.
- `claim_store` (backend `postgres`) — backlog of items in an operator-owned items table; regions are picked by the store at claim time; handle is the claimed item's read-only payload.

Database, S3, append-log, and git kinds are described in the discursive design doc and are deferred to post-v1.

### 4.2 Locks: the unifying primitive

A **lock** is a node's exclusivity claim on a named scope or a region within a store, held for the duration of one execution. Acquired before the node enters `running`; released when the node exits `running` (commit, give-up, or preserve-for-resume).

Locks unify three mechanisms older designs split apart:

- Region exclusive lock = "resource ownership." A node that writes a region holds an exclusive region lock there.
- Counting semaphore on a named lock = "concurrency tag with limit N."
- Mutex on a named lock = "concurrency tag with limit 1."

All lock state lives in postgres, in `rimsky_lock_holders`. Stores never persist lock state. Stores may persist *data* state (e.g. `claim-store-postgres` flips an items-table row to `in_progress` when claiming), but that is store data, not lock state. The "is anyone holding lock X" question is answered exclusively by `rimsky_lock_holders`.

### 4.3 Handles

A **handle** is a native-shape reference to the locked region(s) or claim payload, passed to the executor in the dispatch:

| Store kind                | Handle                                              |
|---------------------------|-----------------------------------------------------|
| `filesystem` (direct)     | absolute directory path; POSIX ops work unmodified  |
| `claim_store`             | the claimed item's payload (read-only JSON)         |

The executor sees the underlying system in its native form. There is no special "rimsky-store" API the executor must call. Lock and commit machinery sit entirely behind the handle.

### 4.4 Modes

A store declares one of three modes at deployment time. v1 ships only `direct`.

- **Direct (v1).** Lock acquisition + handle to the live region. Atomicity at the underlying primitive's granularity. Resumption is trivial: in-progress work is in the live region; the next dispatch picks up where the prior one left off. No sidecar to discard; failed work persists until overwritten. `discard_then_retry` is effectively `keep_then_retry` for direct-mode stores.
- **Sidecar (post-v1).** Lock + handle to a private working copy. On `Complete{changed: true}`: store atomically applies sidecar to live. On `discard_then_retry`: sidecar discarded.
- **Versioned (post-v1).** Sidecar mode + retained committed history + `Restore(target)`.

### 4.5 Claim stores

A `claim_store` holds a backlog of items and hands them out via claim. Each item has a store-assigned ID and a JSON payload. Order may be FIFO, priority-based, or store-policy.

Two claim flavors:

1. **Specified-region lock.** Caller declares the region; store locks or fails.
2. **Claim.** Caller asks the store to pick a region from its eligible pool, lock it, and report the choice.

Both produce the same downstream artifact: a lock handle with identical lifecycle. Only **who chose** differs.

Two things come back from claim acquisition:

- **Payload** — the data that was in the queue / pool item. User data; propagates freely once read.
- **Claim ref** — rimsky-internal bookkeeping (`rimsky_claim_holders` row) for held claims.

Multi-claim is supported: a node may have multiple `stores: [{name: X, claim: true}]` entries from different stores. Each store's claim payload is namespaced under that store's name in the node's attributes (§7).

By default, when the claiming node commits successfully the claim resolves immediately:

- Queues default to `on_commit: delete` (ack), `on_give_up: release_to_head`.
- Ring buffers default to `on_commit: release_to_back`, `on_give_up: release_to_back`.

For workflows where the claim should anchor a longer pipeline, the claim acquisition declares `hold: true`. Holding propagates implicitly through the dependency DAG; at least one terminal-leaf within the holding subgraph must declare a `claim_resolutions` entry that names the held source and store. Template-deploy validation enforces this.

Enqueue / append / item-creation are **not** in rimsky's vocabulary. They are store-external — a store's own HTTP/admin endpoint, used by operators or by external producers. Rimsky does not push items into claim stores. For `claim-store-postgres`, operators populate the items table via direct SQL or the admin-only `POST /admin/claim-stores/:name/items` endpoint exposed by the control API.

### 4.6 Commit verdict (the `changed` field)

When an executor returns `Complete`, it declares `changed: bool`:

- `changed: true` → the supervisor commits attributes to `rimsky_node_attributes`, applies any store-side commit (e.g. sidecar swap, future), emits `recalculate` to dependents.
- `changed: false` → attributes are persisted but no `recalculate` fans out. Dependents are not awakened.

A hash of output content would be fragile — agent output differs on cosmetic whitespace, database-backed regions differ on row order or timestamps. Instead, the producer declares whether its output differs meaningfully:

- **Deterministic executors** compute `changed` however is right for their domain.
- **Agentic executors** report the verdict via the protocol's `Complete` event with an optional `change_summary` string ("3 new zone codes added; 1 boundary refined").

The runtime does not hash content. Invalidation still cascades; `changed` governs whether the cascade continues *forward* from a given commit. A node that concludes "nothing meaningful changed" stops propagation at itself.

**Trade:** the system trusts producers to make honest `changed` calls. The claim is recorded on every commit event; operators can audit. Quality rules can assert minimum-change criteria where a `changed: false` claim is suspect.

### 4.7 Restore as a store concern

Restore (returning a region to a prior version) is a capability of the store, advertised through `Capabilities.SupportsRestore`. v1 stores all advertise `SupportsRestore: false`; the message-level `invalidate(restore_version)` exists for forward compatibility. When a store advertises restore (post-v1, in versioned mode), the supervisor asks the store: "can you roll back to this target?" The store decides; failure routes through the node's policy chain.

Implicit rollback on failure is the steady-state property: when a node fails, its lock is released without commit, and the previous live region (or item state) remains untouched. There is no separate "rollback" operation in v1.

### 4.8 Dependencies vs. cross-region reads

Dependencies and store reads are related but distinct:

- **Dependency.** "Don't run me until this node is fresh." Gates execution order. The default `recalculate` handler waits for all listed dependencies to be `fresh` before the node can run.
- **Store read.** "I can read this region of this store." Does not gate execution. A node can declare `stores: [{name: X, read: [...]}]` without depending on the node that writes that region — useful when the read is opportunistic.

Most nodes both depend on and read from their upstreams. The distinction matters for the few cases where a node needs to consult a region without being schedule-coupled to it.

---

## 5. Messages

Two message types carry all inter-node communication.

### 5.1 `invalidate`

"You are stale."

- Target node marks itself stale.
- If `restore_version` is set (post-v1; v1 stores all advertise `SupportsRestore: false`): the supervisor asks each store the node touches whether restore is supported; if all succeed, the target restores those regions, emits `recalculate` to dependents, returns to `fresh`, and does not re-execute. If any store cannot restore, the message proceeds as an ordinary invalidate.
- Otherwise the target propagates `invalidate` to all its dependents and schedules itself for re-execution.
- Live regions and items remain in place throughout; nothing is mutated until a re-run commits.
- Idempotent: invalidating an already-stale node is a no-op.

### 5.2 `recalculate`

"Your upstream has new data."

- Target node checks its declared dependencies.
- If all dependencies are `fresh`, the node is ready: if it has an executor, a dispatch row is enqueued; if it is pure-cascade, the scheduler transitions it inline on its next tick.
- If any dependency is still `stale`, the message is discarded. The node will be nudged again when another dependency completes.
- Idempotent: a `fresh` node receiving `recalculate` is a no-op.

### 5.3 What isn't a message

Failures are **not** cross-node messages. A node that fails logs a state transition and consults its own error policy, which produces messages (or self-state changes) as a result. Other nodes never subscribe to "A failed"; they subscribe to the `invalidate` or `recalculate` that A's policy emits as a consequence. This keeps the message set closed and avoids a global error-class vocabulary.

Completion is not a message type either. When a node finishes successfully, it emits `recalculate` to its dependents. "Completion" is the internal state change that precedes the outbound message.

Store reads are not messages. A node (or external service) that reads a region does so on demand; stores do not emit change notifications. Nodes that want to react to region updates should declare a dependency on the node that writes that region.

### 5.4 Message shape

```
{
  id: uuid,
  type: invalidate | recalculate,
  source_node_id: uuid,
  target_node_id: uuid,
  timestamp: iso8601,
  params: {
    # invalidate
    reason: string,
    restore_version: string | null,
    # recalculate
    new_version: string
  }
}
```

---

## 6. Error model

Errors are node-local. Each node defines its own error-class taxonomy and maps each class to an ordered fallback chain of actions.

### 6.1 Actions

- **`retry`** — re-execute the node. Parameters: `count` (max attempts), `backoff` (linear | exponential), `base_delay_ms`, `max_delay_ms`, `jitter` (none | plus_minus).
- **`invalidate(targets)`** — emit `invalidate` messages to one or more nodes. Optional `restore_version`. The node itself stays `stale`, awaiting re-execution after upstream refreshes.
- **`give_up`** — transition to `failed`. Optional `reason_template` for the event log.

(Retry variants — `resume_then_retry` and `discard_then_retry` — control whether executor-populated attribute fields are preserved or cleared on re-dispatch. See §7.)

### 6.2 Policy chains

Each error class in a node has an ordered list of actions:

```yaml
error_types:
  fetch_non_json:
    policy:
      - {action: invalidate, targets: [generate-adapter-config]}
      - {action: invalidate, targets: [discover-gis-endpoints]}
      - {action: give_up}
```

Chain semantics:

- On first occurrence of the error class, take action 0.
- On **recurrence** of the same class (another failed run of the node with the same class), advance to the next action.
- On success (node runs to `fresh`), the action index for that class resets.
- On a **different** error class, that class's chain takes over from its own position (previous class's index resets).

This is what lets a cascade climb the graph naturally. `test-adapter` first asks `generate-adapter-config` to re-run; if the same error recurs, it asks `discover-gis-endpoints`; if that still doesn't help, it gives up. The "escalation" is just sequential policy, not a separate subsystem.

### 6.3 Infrastructure errors

Transport failures (connection refused, executor timeout, supervisor crash, heartbeat loss) are routed through `on_error` with an `infra:` prefix (e.g. `infra:transport_timeout`, `infra:executor_unavailable`). Template authors who want application-level policy on infra errors declare them in `error_types`; templates that don't declare them fall through to the default (give up after the orchestrator's own retry).

The scheduler handles supervisor-level failures transparently: a supervisor that has stopped heartbeating causes its node to be released and re-enqueued, producing a clean retry with no policy-chain interaction. This is separate from the policy chain — the policy chain covers errors the node's code (or its executor) produces.

**No hard per-node timeouts.** Agentic work has a heavy-tailed runtime distribution — any deadline short enough to catch hangs will kill legitimate long runs, and any long enough to be safe is effectively no deadline. The combination of heartbeat monitoring, protocol-level terminal events, async handoff with explicit callbacks, and supervisor-level orphan-claim recovery covers the failure modes without arbitrary cutoffs.

### 6.4 Per-node taxonomy, not global

There is no shared enum of error classes across the system. Each node names its own. Consequences:

- Downstream nodes never inspect upstream error classes; they only see `invalidate` or `recalculate`. Error class is internal to the node that raised it.
- The event log stores error classes as strings. Cross-node pattern analysis, if ever added, compares strings without requiring a central taxonomy.
- Adding a node with novel failures does not expand a global vocabulary.
- Naming consistency across nodes is a style-guide concern (review discipline), not a structural one.

### 6.5 Attributes-related error classes

Two built-in error classes are raised by the orchestrator on attributes-resolution failures (see §7). They route through the node's policy chain like any other class; templates may override the default.

- **`template_resolution_failed`** — raised at dispatch when substitution into an `attributes.schema.properties.*.source` directive, a `stores[*].read` / `stores[*].write` region pattern, or a `locks[*].name` cannot resolve a required component (or resolves to an empty string for a region pattern). Default policy: `[ {give_up} ]`.
- **`attributes_schema_failed`** — raised at commit, when the populated attributes object fails JSON Schema validation against the node's declared schema after the executor's writes are merged. Default policy: `[ {give_up} ]`.

Both are recorded in the event log with full context (offending field path, schema-validation message). Templates can declare them in `error_types` to route through retry / invalidate chains where appropriate.

---

## 7. Parameterization

Nodes consume three distinct kinds of input: stable instance-level params (baked in at instantiation), per-run typed `attributes` (resolved at dispatch from upstream attributes, claim payloads, or instance params), and opaque executor configuration in `userdata` (never substituted).

### 7.1 Instance params

Shared across all nodes of one template instance. Template authors declare `params_schema` (JSON Schema) at the template level; operators supply params at `POST /instances`. Params are typically stable identity information (source name, consumer id, region) or instance-specific hints. A `params_redact` list marks top-level keys to redact in control-API display (credentials, secrets); substitution reads the unredacted values.

### 7.2 Two phases of substitution

Rimsky owns all `{...}` and `{{...}}` substitution. Executors do no substitution. **Brace count is the disambiguator: single-brace = instantiation; double-brace = dispatch.**

| Syntax                  | Phase          | When                | Resolved against                                                                              |
|-------------------------|----------------|---------------------|-----------------------------------------------------------------------------------------------|
| `{params.<key>}`        | instantiation  | `POST /instances`   | `rimsky_instances.params` (single pass; baked into node config at instantiation time)         |
| `{{deps.<n>.<f>}}`      | dispatch       | each run            | `rimsky_node_attributes.data` of upstream `<n>`                                               |
| `{{claim.<store>.<f>}}` | dispatch       | each run            | claim payload of `<store>` for this node (the payload field path is `payload.<...>`)          |
| `{{params.<key>}}`      | dispatch       | each run            | `rimsky_instances.params` (re-read on each dispatch; same source as `{params.<key>}`)         |

Where rimsky parses and substitutes:

- `attributes.schema.properties.*.source` directives.
- `stores[*].read` and `stores[*].write` region declarations (each entry in the array).
- `locks[*].name`.
- Any field with `{params.<key>}` (single brace) — at instantiation.

Where rimsky does **not** substitute:

- `userdata` (any depth, any value, any phase). The opaque-block promise (§3.4) means rimsky does not interpret or rewrite its contents. `{{...}}` syntax inside a userdata value is literal bytes to rimsky; executors are free to interpret it themselves.
- `claim_resolutions[*].source` and `claim_resolutions[*].store` — these are raw node-name and store-name string-match references, not substitution targets.

Substitution rules:

- Single pass; no recursion. A substitution result containing `{{...}}` is treated as literal text.
- Required attribute schema fields (per JSON Schema `required`) whose `source` fails to resolve raise `template_resolution_failed` (§6.5).
- Optional attribute fields whose `source` fails to resolve are **omitted** from `data`; they remain absent unless the executor writes them.
- Region or lock-name substitution failure on any required component raises `template_resolution_failed`.
- An empty resolved value (`""` or `null`) for a region pattern is **rejected** with `template_resolution_failed` to avoid grant-everything globs by accident.

Unresolved single-brace placeholders at instantiation return a 400 from `POST /instances` with the offending field path; nothing is committed.

### 7.3 Attributes at dispatch

A node's per-run inputs and outputs live in a single typed `attributes` object, schema-declared in the template and persisted in `rimsky_node_attributes`. Each schema property may carry a `source:` directive:

- `source: "{{deps.<node>.<field>}}"` — populated at dispatch from the named upstream node's attributes.
- `source: "{{claim.<store>.<field>}}"` — populated at dispatch from this node's claim payload on `<store>` (path `payload.<...>`).
- `source: "{{params.<key>}}"` — populated at dispatch from instance params.
- *(no `source:`)* — populated by the executor (or supervisor for native nodes) during the run.

Two writeback patterns:

- **Terminal-final** (default for short-running executors): executor accumulates writes in-process; emits `Complete{ attributes_delta: {...} }` as the terminal event. Supervisor merges into `rimsky_node_attributes.data`, validates, persists.
- **Incremental-via-callback**: executor calls `POST {callback_url}/v1/attributes/{node_id}` per field-write (or batch). Supervisor merges and persists each call. Terminal `Complete` carries no `attributes_delta`. Survives executor death; partial state preserved automatically.

Validation runs at two points: at dispatch (after substitution — `template_resolution_failed` on required-source failure) and at commit (after merging the executor's writes — `attributes_schema_failed` on schema mismatch). See §6.5.

Attributes resumption interacts with retry actions:

- `resumable: true` + `resume_then_retry`: source-driven fields **repopulated** at dispatch (upstream may have changed); executor-populated fields **preserved** verbatim.
- `resumable: true` + `discard_then_retry`: source-driven fields repopulated; executor-populated fields **cleared**.
- `resumable: false` (default) + retry of any kind: source-driven fields repopulated; executor-populated fields cleared.

`run_attempt` is incremented on every retry and exposed to the executor in the dispatch (`ExecuteRequest.run_attempt`).

### 7.4 Mutability

Instance params are user intent and stay stable. Discovered or learned information lives in the attributes of the node that discovered it; nodes that need such information read it via `{{deps.<n>.<f>}}` source directives. This keeps params stable and clean; evolving state lives where the graph's state machine can manage it.

### 7.5 Conditional instantiation

Some parameters affect graph *shape*, not just node behavior. A template may want to strip entire nodes depending on an instance param (e.g. `include_optional_subsystem: false`). Cleanest treatment: templates declare conditions on nodes.

```yaml
nodes:
  - type: optional-subsystem-entry
    condition: params.include_optional_subsystem
    dependencies: [...]
```

At instantiation, nodes whose condition evaluates false are skipped; downstream dependency lists are adjusted automatically. (Condition support is tracked as a post-v1 elaboration; v1 templates express the same thing with two separate template variants.)

---

## 8. Node contract

The declarative shape of a node definition (within a template).

```yaml
type: string                       # Node type name (unique within template)
description: string                # Optional

executor: string                   # Optional. Named executor this node dispatches to.
                                   # Absent = pure-cascade node.

userdata: {...}                    # Optional. Opaque JSON; passed verbatim to executor.
                                   # Never substituted at any depth or phase.

schedule: "<cron expr>"            # Optional. UTC. Scheduler emits invalidate on fire.

dependencies: [string]             # Sibling node types. Gates execution order.

stores:                            # Stores this node touches. Omitted if none.
  - name: string                   # Operator-configured store name (resolved at dispatch).
    write: [string]                # Optional. Region patterns this node writes
                                   #   (kind-specific grammar; substituted at dispatch).
    read:  [string]                # Optional. Region patterns this node reads.
    claim: bool                    # Optional. true = store-picks-region acquisition.
    hold:  bool                    # Optional. With claim:true, anchor for held-claim
                                   #   resolution at terminal-leaves.
    on_commit: string              # Optional. Override store's claim-resolution default
                                   #   on commit (e.g. delete | release_to_back).
    on_give_up: string             # Optional. Override store's default on give-up.
    resumable: bool                # Optional. See §7.3.

locks:                             # Exclusivity declarations. Optional.
  - name: string                   # Lock identifier; substituted at dispatch.
    mode: mutex | counting         # mutex = limit 1; counting = semaphore.
    limit: int                     # Required for counting; ignored for mutex.

attributes:                        # Per-run typed data shape. Optional but recommended.
  schema:
    type: object
    properties:
      <field>:
        type: string | number | boolean | object | array
        source: string             # Optional substitution directive:
                                   #   "{{deps.<n>.<f>}}" |
                                   #   "{{claim.<store>.payload.<f>}}" |
                                   #   "{{params.<key>}}".
                                   # No source = executor-populated.
    required: [string]             # JSON Schema required list.

claim_resolutions:                 # At terminal-leaves of held-claim subgraphs. Optional.
  - source: string                 # Node type that originally claimed (raw match).
    store:  string                 # Store name (raw match).
    on_commit:  string             # Optional override.
    on_give_up: string             # Optional override.

quality_rules:                     # Optional. Validate writes before commit.
  - type: string                   # Builtin or custom rule-type name.
    target: write | <store-name>
    severity: error | warning      # error blocks commit; warning logs only.
    config: {...}

error_types:                       # Per-node error taxonomy with policy chains.
  <error_class>:
    policy:
      - action: retry              # Re-execute. Source-driven attribute fields are
                                   #   repopulated; executor-populated fields cleared.
        count: int
        backoff: linear | exponential
        jitter: none | plus_minus
        base_delay_ms: int
        max_delay_ms: int
      - action: resume_then_retry  # Re-execute. Executor-populated attribute fields
                                   #   preserved (requires resumable: true).
      - action: discard_then_retry # Re-execute. Executor-populated attribute fields
                                   #   cleared (requires resumable: true).
      - action: invalidate
        targets: [string]          # Sibling node types
        restore_version: previous | null
      - action: give_up
        reason_template: string
```

The template wrapping the nodes list additionally carries:

```yaml
name: string
version: string
description: string
nodes: [ ... as above ... ]
params_schema: { ... JSON Schema ... }
params_redact: [string]            # Top-level keys to redact in HTTP output
```

### 8.1 Runtime state

Each node instance carries runtime state, not declared in the template:

- `state` — `fresh | stale | running | failed`.
- `current_error_class` — set while the error policy is active; null otherwise.
- `retry_counter` — attempts within the current `retry` action.
- `action_index` — position in the current error class's policy chain.
- `run_attempt` — incremented on every retry; exposed to executors in `ExecuteRequest`.

Per-run attributes data lives in `rimsky_node_attributes` keyed by node id; lock state lives in `rimsky_lock_holders`. Held-claim bookkeeping (one row per held-claim × terminal-leaf) lives in `rimsky_claim_holders`. None of these are properties of the node template; they are runtime tables managed by the orchestrator.

### 8.2 Default handlers

Nodes do not declare message handlers; they inherit system defaults.

**on_invalidate(msg):**
1. If `msg.restore_version` is set (post-v1; v1 stores all advertise `SupportsRestore: false`): ask each store touched by the node whether that target is supported. If all accept, restore those regions, emit `recalculate` to dependents, return to `fresh`.
2. Else if already `stale` or `running`: no-op.
3. Else: transition to `stale`, leave live regions and items in place, emit `invalidate` to all dependents, schedule self for re-execution.

**on_recalculate(msg):**
1. If `fresh`: no-op (may update which upstream version has been acknowledged).
2. If `stale`: check all `dependencies` — if any stale, no-op (node will be nudged again). If all fresh, enqueue a dispatch row (executor nodes) or transition inline on the next scheduler tick (pure-cascade).

**on_work_complete(attributes_delta, changed, change_summary):**
1. Merge `attributes_delta` (or accumulated incremental writes) into `rimsky_node_attributes.data`. Validate against the declared schema; on failure raise `attributes_schema_failed` (§6.5).
2. Run any declared `quality_rules` against writes. `severity: error` failures → treat as `error(quality_rule_failed)`. `severity: warning` failures → logged, do not block.
3. If `changed: true`: persist attributes, apply any store-side commit (sidecar swap in post-v1 modes; nothing for direct mode), release locks, run claim-resolution algorithm in the same transaction (§4.5 / spec §5.6.4), emit `recalculate` to dependents, log `attributes_committed`, transition to `fresh`.
4. If `changed: false`: persist attributes, release locks, run claim-resolution algorithm, no `recalculate` fans out, transition to `fresh`.
5. Pure-cascade node: transition handled by scheduler sweep, not supervisor.

**on_error(error_class):**
1. Look up class in `error_types`. Missing → treat as `give_up` with unknown-class reason. Built-in classes `template_resolution_failed` and `attributes_schema_failed` default to `[ {give_up} ]` if not declared.
2. Take the action at `action_index` for that class.
3. `retry` / `resume_then_retry` / `discard_then_retry` exhausts → advance `action_index`. `invalidate` → emit, stay `stale`, let recurrence advance `action_index`. `give_up` → transition to `failed`.
4. Successful re-run resets `retry_counter` and `action_index` for that class.

---

## 9. Lifecycle

### 9.1 Template registration

`POST /templates`:
1. Template YAML is validated:
   - All `dependencies` reference declared nodes.
   - All `error_types.<class>.policy[*].invalidate.targets` reference declared nodes.
   - No dependency cycles.
   - `schedule` (if present) is a valid cron expression.
   - Pure-cascade nodes do not declare `stores[*].write` regions.
   - `userdata` on a node without `executor` warns.
   - All `stores[*].name` references resolve against a registered store kind in the local registry; the store kind accepts the declared `read` / `write` region grammar.
   - `attributes.schema` is a valid JSON Schema (draft-07).
   - All single-brace `{params.<key>}` placeholders reference keys present in `params_schema`.
   - All `{{deps.<n>.<f>}}` source directives reference declared upstream nodes and fields present in those nodes' attribute schemas.
   - All `{{claim.<store>.<f>}}` source directives reference a store the node has claimed.
   - Held-claim resolution: every terminal-leaf in a held-claim subgraph declares a matching `claim_resolutions` entry (algorithm in spec §11.4).
2. Valid templates are stored; validation failures return 400 with the offending field.

### 9.2 Instance registration

`POST /instances` with `{template_id, consumer_key, params}`:
1. Validate `consumer_key` unique within `template_id`.
2. Validate `params` against the template's `params_schema`.
3. Allocate instance UUID.
4. For each node: allocate node UUID; resolve `dependencies` to sibling node UUIDs; substitute single-brace `{params.<key>}` placeholders into any field that takes them (region patterns, lock names, attributes-schema source directives — all baked into per-instance node config so dispatch-time substitution operates on the resolved values).
5. For nodes with `schedule`: compute `next_fire_at` from cron and current clock; write to schedule table.
6. Log `state_transition` events for all nodes (initial state `stale`).
7. Enqueue dispatch rows for root executor nodes (no dependencies, has `executor`).
8. Root pure-cascade nodes will be transitioned inline by the scheduler on its next tick.

### 9.3 Execution

For each node the scheduler picks up:

1. **Pure-cascade nodes** (no `executor`): the scheduler transitions `stale → fresh` inline on its sweep, emits `recalculate` to dependents, and logs a `state_transition` event with cause `pure_cascade`. No dispatch row, no supervisor, no executor RPC.
2. **Executor nodes**: a dispatch row sits in the queue. A supervisor whose config accepts the node's executor name claims the row under `FOR UPDATE SKIP LOCKED`.
3. After claim, the supervisor **re-reads `claimed_by`** (the verify-before-run invariant — see §11.1) before doing any work. If the claim has been released or re-claimed, the supervisor bails and the system cleans up.
4. The supervisor acquires all declared locks (named, region, claim) atomically per the per-tag-sorted lock-acquisition invariant (§11.3 / spec §13.3). Region globs and lock names are substituted at this point against upstream attributes, claim payloads, and instance params; failure raises `template_resolution_failed` and unwinds the lock acquisition.
5. The supervisor builds the dispatch's `attributes` object: source-driven properties resolved from upstream attributes / claim payloads / params; sourceless properties left empty for the executor to populate. Executor invocation: `Execute(node_id, instance_id, node_type, userdata, attributes, attributes_schema, stores, callback_url, cancel_token, resumed, run_attempt)`. Each `StoreHandle` carries the store-kind-specific native handle (filesystem path, claimed-item payload).
6. The executor returns a stream of zero or more `Heartbeat` events followed by exactly one terminal event: `Complete` (with optional `attributes_delta`, `changed`, `change_summary`), `Blocked`, `Errored`, or `AsyncAccepted` (async handoff). For incremental writeback, the executor calls `POST {callback_url}/v1/attributes/{node_id}` per field-write; the supervisor merges and persists each call.
7. For `Complete`: the supervisor merges any final `attributes_delta`, validates against the schema (failure → `attributes_schema_failed`), runs declared `quality_rules` (failure → `quality_rule_failed`), then commits attributes and runs the claim-resolution algorithm (§4.5) inside the same transaction that releases the lock-holder rows. Success → log `attributes_committed`, emit `recalculate` if `changed: true`.
8. For `Blocked`: route through `on_error(executor_blocked)` unless the template declares a more specific class.
9. For `Errored`: route through `on_error(error_class)` with the executor-supplied class.
10. For `AsyncAccepted`: the supervisor holds the dispatch claim and keeps the node `running`; a callback POST from the executor (carrying the eventual `Complete` / `Blocked` / `Errored`) completes the dispatch later. See `protocol.md` for the callback contract.
11. On any failure path: the policy chain consults `error_types`; actions are taken; the node's state advances accordingly.

### 9.4 Pure-cascade execution

Pure-cascade nodes never enter the dispatch queue. The scheduler's pure-cascade sweep runs on every tick: for each `stale` node with no `executor` and all dependencies `fresh`, transition `stale → fresh` inline, emit `recalculate` to dependents, log a state-transition event with cause `pure_cascade`. No `work_started` / `work_completed` events. Commit verdict is always `changed: true`; propagation is the purpose. This path is what makes schedule-driven fan-out (one root node with a `schedule`, dozens of dependents) efficient: the root's "work" is a state transition and a fanout message wave, handled in-process to the scheduler.

### 9.5 Observability

The `rimsky_events` log is the single source of truth for what happened:

- Every message delivered (both types).
- Every state transition with cause (`state_transition`).
- Every error occurrence (`error`, with class and payload — including `template_resolution_failed` and `attributes_schema_failed`).
- Every work start / complete (`work_started`, `work_completed`).
- Every attributes substitution (`attributes_substituted`), commit (`attributes_committed` with `change_summary`), validation failure (`attributes_validation_failed`), and quality rule failure (`quality_rule_failed`).
- Every schedule fire (`schedule_fired`).
- Every operator override (`operator_override`).
- Every heartbeat loss (`heartbeat_lost`), orphaned claim release (`orphaned_claim_released`), orphaned claim lost-race (`orphaned_claim_lost_race`).
- Unresolved executor (`unresolved_executor`), work rejected (`work_rejected`) when executor output fails protocol-level validation.

Operators reading the log can reconstruct the full trajectory of any node without touching runtime state. Replay and debugging work by re-routing messages against a frozen graph and watching the state machine evolve.

---

## 10. Design principles

Principles that fell out of working through the model. Cross-references to the sections that embody each.

### 10.1 Declarative by default, agentic where irreducibly needed

Failures that are enumerable in advance are handled by declarative policy (§6). Only truly novel failures (error classes no policy matches) end in `give_up`, where a human or optional reasoner steps in. LLM cost is concentrated in nodes whose *work* is LLM-backed, not in overseeing the graph.

### 10.2 No privileged controllers

Every unit of work, including any maintenance or cross-instance reasoning, is a node subject to the same primitives (§3). There is no scheduler-node, no maintenance-agent sitting above the graph, no special class of "orchestrator." This keeps the surface area small and composition uniform.

### 10.3 Work and data are separate

Nodes are actors; stores are backends; regions and items are artifacts (§4). Node logic evolves independently from store kind and region grammar. This makes data independently consumable — by other nodes, external services, agentic discovery — without coupling consumers to producers' internals.

### 10.4 Implicit rollback on failure

A node that fails releases its locks without committing (§4.2, §4.7). The previous live region (or claimed item state) is untouched; consumers continue reading what was last good. Sidecar and versioned modes (post-v1) extend this with explicit restore; in v1 direct mode, "rollback" is what happens by default when commit doesn't.

### 10.5 Errors are node-local

A node's error taxonomy describes *its own* failure modes (§6.4). Downstream nodes see only the consequences (`invalidate`, `recalculate`), never the class. No shared error vocabulary needs to be maintained.

### 10.6 Human review as timeout, not gate

Review is the fallback when self-repair exhausts (`give_up`), not a ceremonial step on happy paths. Consequential operations that should require human sign-off are gated through quality rules plus probe nodes that verify against real data, not through blanket review flags.

### 10.7 Observability is structural

The event log is the system's truth, not a derived artifact (§9.5). Every state transition, message, error, attributes commit, and lock-holder change is a row. Dashboards and reasoners read the log rather than polling nodes.

### 10.8 Structured completion over inferred

Executors report completion through a typed protocol event, not through file artifacts or exit-code inference (see `protocol.md`). This localizes structure to the interface (the event's schema) rather than to a serialization convention, allows in-conversation correction when output is malformed (by executors that host their own in-process correction loops), and makes "I am done" an explicit act rather than an emergent property of a process ending.

### 10.9 Monitor, don't deadline

Agentic work has a heavy-tailed runtime distribution (§6.3). Hard deadlines trade legitimate long runs for catching hangs — a bad trade, because hangs can be detected by heartbeat monitoring and stream analysis without a deadline. Monitoring is primary; deadlines (if used at all) are soft warnings for observability.

### 10.10 Producer owns the change verdict

A content-hash over node output would be fragile (§4.6). Each producer declares on commit whether its output differs meaningfully from the previous version (`changed: bool`). The runtime does not hash content. An honest producer is the best judge of whether its output matters; the claim is recorded on every commit for audit.

### 10.11 Executors are peers, not subsystems

An executor is a separate service the orchestrator calls over the wire (see `protocol.md`). It does not run inside the orchestrator process; it does not register runtime state with the orchestrator; it is not a plugin loaded into the orchestrator's memory. This is deliberate: executors in different languages, with different runtime needs (GPU, subprocess spawning, long-lived internal state), are operationally peers. The orchestrator sees one interface — the protocol — and knows nothing of how the executor is implemented.

The practical consequence: authoring a new executor requires no orchestrator changes, no recompilation, no redeployment of the orchestrator. An executor can fail, upgrade, or restart independently of the orchestrator. This is the architecture property that makes rimsky domain-agnostic.

### 10.12 Stores own their restore semantics

Restore is a store capability, not a message-level feature (§4.7). The orchestrator does not interpret `restore_version`; it asks the store via `Capabilities.SupportsRestore` and a kind-specific implementation. Stores that cannot restore advertise `SupportsRestore: false`, and `invalidate(restore_version)` falls through to ordinary invalidation. This makes restore safe to declare on any node — stores that support it act; stores that don't, don't. No orchestrator knowledge of store internals.

---

## 11. Invariants

Load-bearing properties the implementation must preserve. Each is annotated `@blessed-invariant` in the Go source and exercised by a scenario test.

### 11.1 State machine rejects illegal transitions

`UpdateState(node, to, reason)` never short-circuits when `to == from`. In particular `running → running` under reason `dispatch_claimed` raises an error. Double-execute detection depends on it: a slow supervisor that got as far as `UpdateState(node, "running", dispatch_claimed)` while another supervisor was already running the same node would otherwise silently succeed, bypassing the claim-ownership re-check.

### 11.2 Dispatch claim brackets run

Tag-limit counts come from `rimsky_dispatch.claimed_by IS NOT NULL`, not from node state. The claim window exactly brackets the node's `running` window. Refactoring the claim/complete flow must preserve this property; counts drawn from any other source can diverge under races.

### 11.3 Per-tag locks acquired in sorted order

When multiple concurrency tags apply to one dispatch, advisory-lock acquisition sorts the tag names lexicographically. This prevents deadlocks between concurrent claims sharing a tag subset.

### 11.4 Claimant-guarded release

`releaseClaim(id, expected_claimed_by)` is a no-op if mismatch. Prevents stale orphan sweeps from nulling live claims that have been re-claimed by another supervisor.

### 11.5 Verify-before-run

Supervisors re-read `claimed_by` immediately after claim, before any expensive work (executor RPC, subprocess spawn). Hard backstop against the orphan-claim race: if the sweep released the claim and another supervisor re-claimed in the interval, verify-before-run detects the lost claim and bails.

### 11.6 Generous orphan-claim cutoff

Default `5 × heartbeat_timeout`. Lower values become a double-execute vector: a slow supervisor that is still alive but has not yet heartbeated could have its claim released prematurely while still holding it in its own runner.

### 11.7 Advisory lock on scheduler tick

`pg_try_advisory_lock(SCHEDULER_TICK_KEY)` at the top of each scheduler tick. Prevents multi-replica double-ticks (each replica drops its tick if another holds the lock).

### 11.8 Session advisory lock on migrations

The migration runner holds a session-level advisory lock for the duration of a migration batch. Prevents concurrent-runner migration-table corruption.

---

## 12. Deferred

Intentionally out of scope for v1; to be revisited.

### 12.1 Freshness policies

A two-level SLO on a node's commit (`warn_after`, `fail_after`). `warn_after` elapses without new commit → observation logged. `fail_after` elapses → node invalidated. Complements invalidation rather than replacing it. Deferred post-v1.

### 12.2 Observer nodes

A node whose job is to report the version of something external we don't control (an ArcGIS ETag, a website's last-modified header). Emits `invalidate` when the external thing changes. Deferred post-v1.

### 12.3 External trigger nodes

A node that emits `invalidate` in response to an external webhook or event. Deferred post-v1.

### 12.4 Priority on nodes/messages

Integer priority on the dispatch queue — human-triggered runs preempt scheduled runs. Deferred post-v1.

### 12.5 Additional store kinds

Database (direct-mode SQL with attribute mapping), `s3` (bytes in S3/MinIO, manifest in Postgres), `git` (versioned commits with restore), and `append-log` (structured event streams) are the most immediate post-v1 candidates. Sidecar and versioned modes for filesystem and database are also deferred.

### 12.6 Additional executor references

`sql-node` (Go, executes SQL against a named connection) and `langgraph-node` (Python, bridges rimsky to LangGraph graphs) are the next reference executors planned.

### 12.7 gRPC control API

v1 ships the control API as HTTP+JSON only. A gRPC surface for programmatic consumers is deferred.

### 12.8 OpenTelemetry traces

v1 ships Prometheus metrics and structured logs. OpenTelemetry trace support is deferred.

### 12.9 Web UI

v1 is CLI + API only. A web UI for operators is deferred.

### 12.10 Content-hash staleness

Any hash-based change detection is deliberately absent. Cost of fragility outweighs benefit; `changed: bool` from the producer is the model.

### 12.11 Schema evolution on attributes / regions

Coordinated migration of a node's attribute schema (new shape published, readers updated, old deprecated) is unspecified. To be designed before any consumed attribute shape has many external consumers.

### 12.12 Region-level subscriptions

Readers of a region currently do not receive change notifications; they read on demand. A future enhancement could allow a node to subscribe to region changes rather than to a specific writer node (decoupling reader from writer identity). Adds machinery; defer until a concrete use case demands it.

### 12.13 Dynamic dependencies

Some nodes may eventually need runtime-computed dependency lists ("depend on all matching nodes discovered at runtime"). Static lists cover today's cases; dynamic dependencies are deferred.

### 12.14 Cross-instance reasoning

A hypothetical maintenance reasoner that notices patterns across many instances. Not required by the core model; if added, it would be a node like any other, whose inputs are event-log queries and whose outputs are `invalidate` messages. Defer until concrete need surfaces.

### 12.15 Per-instance param edits

Operators adjusting a specific instance's tuning knobs post-instantiation. Expected to work; implementation is a control-API endpoint that writes params and emits `invalidate` to affected nodes. Detail deferred.

### 12.16 Ad-hoc graph construction

Instances today are always created from a template. Eventually the model may support user-defined graphs or runtime-edited graph shapes. Runtime-editable shape requires versioning, diffing, and rollback of the graph itself; scope carefully when it comes up.

### 12.17 pypi package publishing

Python-shaped rimsky client libraries / executor helpers published to pypi. Deferred post-v1.

---

## 13. Glossary

- **Node** — The unit of work. Properties: may have an executor, userdata, schedule, dependencies, stores, locks, attributes, claim resolutions, error taxonomy.
- **Pure-cascade node** — A node with no `executor`. Transitions `stale → fresh` inline when dependencies settle; exists to propagate cascade without doing work itself.
- **Executor node** — A node with an `executor`. Dispatched via the protocol; work is done externally.
- **Scheduled node** — Any node with a `schedule`. Scheduler emits `invalidate` when the cron fires.
- **Fan-out node** — A pure-cascade node with a `schedule` and many dependents; invalidate wave fans out on cron fire.
- **Executor** — A peer service that implements the node-executor protocol and does work on behalf of nodes that reference it by name.
- **Executor name** — The string that appears in `nodes[*].executor` and is resolved by supervisor config to an endpoint.
- **Store** — Operator-configured deployment-level data backend (filesystem, claim_store, future database/S3/git). Has regions, locks, modes, and capabilities. Referenced by name from templates.
- **Store kind** — The Go implementation of the `Store` interface backing a store (e.g. `filesystem`, `claim_store`). Registered at process startup.
- **Region** — A portion of a store's namespace, addressed by a kind-specific grammar (path globs for filesystem; per-claim for claim stores).
- **Mode** — A store's commit semantics: `direct` (v1), `sidecar` (post-v1), `versioned` (post-v1).
- **Lock** — A node's exclusivity claim on a named scope, a region within a store, or a claimed item; held for the duration of one execution. State persisted in `rimsky_lock_holders`.
- **Named lock** — A lock keyed by a string name; mode is `mutex` (limit 1) or `counting` (semaphore with `limit: N`). Replaces the older "concurrency tag" concept.
- **Region lock** — A lock on one or more regions of a store, derived from a node's `stores[*].write` declaration.
- **Claim lock** — Store-picks-region lock acquisition; the store selects an eligible region and reports the choice in the handle.
- **Claim payload** — User-data portion of a claimed item (e.g. queue message body), exposed to the executor through the store handle and to attributes via `{{claim.<store>.payload.<f>}}`.
- **Claim ref** — Rimsky-internal bookkeeping for a held claim (`rimsky_claim_holders` row).
- **Held claim** — A claim acquired with `hold: true`, anchoring a downstream subgraph; resolved at terminal-leaves via `claim_resolutions`.
- **Handle** — Native-shape reference to a locked region or claimed item, passed to the executor: an absolute filesystem path, a claimed-item payload, etc.
- **Sidecar** — A store's per-lock private workspace (post-v1 sidecar/versioned modes).
- **Attributes** — Per-run typed data shape declared on the node (JSON Schema). Source-driven properties pre-populated at dispatch from upstreams, claim payloads, or instance params; sourceless properties populated by the executor. Persisted in `rimsky_node_attributes`.
- **Source directive** — An `attributes.schema.properties.<f>.source` value of the form `{{deps.<n>.<f>}}` / `{{claim.<store>.payload.<f>}}` / `{{params.<key>}}`, substituted by rimsky at dispatch.
- **Claim resolution** — A terminal-leaf node's declared action (delete / release_to_back / release_to_head) for a held claim it inherited.
- **Userdata** — An opaque JSON block on a node template, passed verbatim to the executor on `Execute`. Rimsky never parses, validates, or substitutes it.
- **Schedule** — Optional cron expression on any node. Scheduler emits `invalidate` when it fires.
- **Probe** — A node whose work writes its own region while exercising upstream regions realistically. Not a guard; its output is consumed downstream.
- **Dependency** — Declared node-to-node edge: "I wait for you to be fresh before I run."
- **Store read** — A node's declared ability to read a region of a store (`stores[*].read`). Separate from dependency — reads don't gate execution.
- **Message** — `invalidate` or `recalculate`. The only two types.
- **Error class** — A per-node label for a failure mode. Key in the node's repair-policy map.
- **Policy chain** — Ordered list of actions for one error class. Advances on recurrence; resets on success.
- **`template_resolution_failed`** — Built-in error class raised when dispatch-time substitution into a required source directive, region pattern, or lock name fails.
- **`attributes_schema_failed`** — Built-in error class raised when the populated attributes object fails JSON Schema validation at commit.
- **Quality rule** — A check on a node's writes before commit.
- **Instance params** — User-supplied configuration per template instance. Stable; no rewriting from within the graph.
- **Placeholder** — Substitution syntax in string fields. Single-brace (`{params.<key>}`) substituted at instantiation; double-brace (`{{deps.<n>.<f>}}`, `{{claim.<store>.<f>}}`, `{{params.<key>}}`) substituted at dispatch. Never applied to `userdata`.
- **`fresh` / `stale` / `running` / `failed`** — The four node states.
- **`run_attempt`** — Counter incremented on each retry, exposed to the executor in `ExecuteRequest`.
- **`rimsky_events`** — The append-only log of messages, state transitions, errors, attributes commits, lock-holder changes, and other observable events. The system's source of truth.
- **`changed` (commit verdict)** — Boolean declared by the producer on every commit: true if the new output differs meaningfully from the previous version, false otherwise. Governs whether `recalculate` fans out.
- **`change_summary`** — Optional human-readable note accompanying a `changed: true` commit. Appears in the event log.
- **Check severity** — `error` (blocks commit, triggers policy) or `warning` (logs, does not block).
- **Capabilities** — Per-store advertised feature flags: `SupportsRegionLock`, `SupportsClaim`, `SupportsDiscard`, `SupportsResume`, `SupportsRestore`.
- **Scheduler** — Long-running process that reads node state and dispatches work by enqueueing dispatch rows, sweeping orphan claims and lock-holders, and transitioning pure-cascade nodes inline.
- **Supervisor** — Worker process that claims dispatch rows, acquires locks, calls executors over the protocol, and persists outcomes.
- **Control API** — HTTP+JSON interface for deploying templates, creating instances, applying operator overrides, reading events.
- **Three collections** — The architectural separation of orchestrator, store library, and executor library. Versioned together in v1, separable indefinitely.
- **Async handoff** — Protocol pattern where an executor returns `AsyncAccepted` immediately and later posts the terminal outcome to a callback URL. Lets executors with long-running internal work avoid holding the `Execute` stream open.
- **Conformance suite** — The `rimsky-conformance` binary that validates a given executor endpoint against the protocol contract.
- **`@blessed-invariant`** — A source annotation marking a load-bearing property that implementation changes must preserve.

---

## Frames as the unit of resolution

Reactive node-graph systems — game engines, build systems, spreadsheets, modern UI frameworks — operate under an implicit invariant: **the graph resolves to a consistent state between invalidations**. In synchronous engines (e.g., game-engine render loops, spreadsheet recalc cycles, React Concurrent Mode's batched renders) the invariant is structural: a render frame fully evaluates the scene before the next can begin. Without this guarantee, downstream observers can read mid-recomputation state from upstream — which in an asynchronous engine like rimsky, where each node-execution is a unit of supervisor-scheduled work that takes seconds-to-minutes, becomes observable as inconsistent or dropped work.

Rimsky enforces the invariant at the graph level via a **frame**: a complete pass of the cascade engine over the reachable subgraph from one or more invalidation sources. Frames are queued and rendered serially per instance — at most one frame in `running` state at any time. Two modes select how multiple invalidations interact:

- **`serial_queue`** — every invalidation produces a distinct frame; the queue runs FIFO. Suited to event-driven workloads where each invalidation is a discrete unit of work.
- **`coalesce`** — invalidations during an in-flight render collapse into a single trailing frame whose source set is the union of all coalesced sources. Suited to data-freshness pipelines where rate of state-change >> rate of meaningful recomputation.

`frame_resolution` is a required template field; control-api rejects template uploads without it. The full spec is at `docs/specs/2026-04-26-frame-resolution-design.md`.

Operator invalidates and scheduled fires are both frame-producing events: they call into `frame.EnqueueOrCoalesce` and either queue or coalesce per the template's mode. There is no preemption of running work — the `kill_requested` mechanism that existed pre-frame-resolution was removed.

## Appendix: Relationship to the TypeScript predecessor

Rimsky v1 (Go) is preceded by a TypeScript implementation, kept in the originating monorepo and used through its lifecycle as a proving ground. The TypeScript project called the unit of work a "cell"; the Go v1 calls it a "node." The rename was mechanical: every identifier, table name, proto message, and doc sentence changed — `cell → node`, `cell_events → rimsky_events`, `CellExecutor → NodeExecutor`. The concept did not change; the state machine, message semantics, policy-chain evaluator, event-kind taxonomy, and commit flow are identical.

The rename was done for three reasons. First, state left the node: resources own versioning, rollback, and data. The "stateful self-contained cell" metaphor no longer applies — nodes are graph-vertex declarations referencing executors and resources. Second, execution left the node: executors run the work as peer services; a node holds no execution logic. Third, native vocabulary: in conversation, designers and reviewers consistently drifted to "node"; vocabulary that requires discipline to sustain is the wrong vocabulary. "Node" also aligns with the orchestration landscape (Airflow, Temporal, Argo, Dagster, LangGraph all use "node," "task," "step," or "activity").

This is the only appearance of "cell" anywhere in this document. From here forward, rimsky uses "node."
