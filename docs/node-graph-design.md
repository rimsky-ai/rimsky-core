# Node Graph Design

Conceptual reference for rimsky v1 (Go). Rimsky is a project-agnostic reactive node-graph orchestration platform: a graph of nodes that communicate via two message types (`invalidate`, `recalculate`), operate on versioned resources, and execute work through external executors speaking a well-defined protocol.

This document is a design reference, not a spec or plan. It captures the conceptual model, the contracts, and the reasoning. Implementation shape (package layout, process model, storage) lives in `architecture.md`; wire-level contract details live in `protocol.md`.

An appendix at the end (§13) notes the relationship to rimsky's TypeScript predecessor — the rename there was mechanical, the concept did not change.

---

## 1. Motivation

Orchestrators in the common market are one of two shapes: forward-only pipelines (Airflow, Argo, Dagster) that handle failure by retry-or-abort, or workflow DSLs (Temporal, LangGraph) that let you write arbitrary control flow at the cost of giving up a declarative shape. Neither has a good story for the pipeline that drifts: a pipeline whose upstreams change shape without notice, whose downstream observations reveal problems that should invalidate something three steps back, whose recovery is not "retry the failing step" but "re-run the upstream that produced the bad configuration."

Rimsky's observation is that the forward-only abstraction is wrong for this class of problem. The domain has drift, recovery, rollback, and probing as first-class concerns. The control plane should too.

Three observations shape the model:

1. **The dependency graph is acyclic; execution is not.** Who-depends-on-whose-data is a DAG. But execution can move backward: a downstream failure can invalidate an upstream, causing it to re-run. Forward execution is data flow; backward execution is repair cascade.
2. **Everything that does work is a node.** Data-producing work is a node. Scheduled fan-out is a node. Agentic work with LLMs is a node. External-event bridges are nodes. There is no privileged controller class ("the scheduler," "the repair agent") sitting above the graph. The scheduler is infrastructure; any reasoning lives in a node like any other.
3. **Data is a resource, not node internals.** A node that produces data *owns* one or more resources — it is the sole writer — but the resource has its own identity, schema, and versioning, and can be read by other nodes, external services, or the API. This separates work logic from data schema and makes consumption discoverable without coupling consumer to producer.

The behavioral vocabulary is small:

- **2 message types**: `invalidate`, `recalculate`.
- **3 error actions**: `retry`, `invalidate(targets)`, `give_up` — chainable as an ordered fallback.
- **4 node states**: `fresh`, `stale`, `running`, `failed`.
- **Per-node error classes**: each node defines its own taxonomy; repair policies match against it.

This vocabulary is enough to model declarative pipelines, reactive cascades, agentic reasoning loops, and scheduled fan-out patterns without enlarging.

---

## 2. Core model

The system is a **graph of nodes** that communicate by **messages**, operating on **resources**, executing work through **executors**.

- **Node** — a graph-vertex declaration. State + declared dependencies + message handlers + per-error-class repair policies + (if resource-owning) the set of resources it writes + (if executor-backed) the executor it dispatches to.
- **Message** — `invalidate` or `recalculate`. The only two types.
- **Resource** — the unit of data. A node owns it; many readers consume it.
- **Executor** — a peer service that speaks the node-executor protocol. The orchestrator dispatches node work to an executor; the executor returns a result, reports blocked, errors, or hands off asynchronously.

Rimsky is built from three architectural collections (each developed, versioned, and deployed independently once separated):

- **Orchestrator.** The node-graph runtime: state machine, scheduler, supervisor, control API, dispatch queue, storage. Knows nothing about LLMs, HTTP, or any specific work domain.
- **Resource library.** Implementations of the `Resource` interface. Each implementation decides how to store versions, how to roll back, how to evaluate quality rules.
- **Executor library.** Reference executor services that speak the protocol. Executors run as peer services; the orchestrator calls them over the wire.

The orchestrator consumes resources through an in-process Go interface and executors through the wire protocol. Swapping either is a configuration or code-at-main boundary concern, not an orchestrator change.

---

## 3. Nodes

### 3.1 Properties, not classes

A node is described by a set of **properties**, any combination of which may apply. There is no fixed taxonomy of node kinds; a node has whatever properties it needs.

The properties are:

- `executor` — named external executor this node dispatches to. Absent → the node does not do work externally.
- `userdata` — opaque JSON block passed verbatim to the executor. Rimsky does not interpret it.
- `schedule` — cron expression (UTC). The scheduler emits `invalidate` to the node when the cron fires.
- `dependencies` — list of sibling node types. Gates execution order; the default `recalculate` handler requires all listed dependencies `fresh` before the node runs.
- `concurrency_tags` — tags the scheduler uses for per-tag limits at dispatch time.
- `owns_resources` — list of resources this node writes.
- `reads_resources` — resources the node reads outside its dependency chain.
- `error_types` — per-node error taxonomy with ordered policy chains.

Every combination of these is valid (modulo one constraint, §3.3). In practice, nodes fall into a few recognizable shapes, but these shapes are emergent from the properties a template author chose, not enumerated by a `kind` field.

### 3.2 Node shapes in practice

- **Executor node.** Has `executor` and probably `userdata`. Runs when dispatched; commits results (if it owns resources) or returns work-complete (if not).
- **Pure-cascade node.** No `executor`. When invalidated and dependencies are fresh, the scheduler instantly transitions `stale → fresh` inline and emits `recalculate` to dependents. Its purpose is to propagate cascade without doing work itself — a join point, a debounce, a graph-shape device.
- **Scheduled node.** Any node with a `schedule`. The scheduler emits `invalidate` to it when the cron fires. Otherwise indistinguishable from any other node; scheduled executor nodes, scheduled pure-cascade nodes, and scheduled nodes with dependencies all compose naturally.
- **Fan-out.** A pure-cascade node with a `schedule`, no dependencies, and many downstream dependents. When the cron fires, the invalidate ripples to every dependent. This is how periodic full-graph refresh is expressed.
- **Agentic node.** A node whose executor happens to run an LLM. "Agentic" is a description of the executor's behavior, not a distinct node property.

The distinction that matters at the contract level is whether the node owns resources. Nodes with resources have quality rules and commit-or-reject semantics on their writes; nodes without resources emit messages and nothing else.

### 3.3 Constraints

Only one property-combination is invalid: a pure-cascade node (no `executor`) cannot have `owns_resources`. Pure-cascade nodes emit no data; declaring ownership of a resource without a mechanism to produce versions for it is ambiguous. Template validation rejects the combination at `POST /templates`.

`userdata` on a node with no `executor` produces a warning, not an error, at template deploy. Opaque fields are not inherently wrong; the warning flags likely authoring mistakes.

### 3.4 Userdata: the opaque block

Every executor-invoking node carries a `userdata` field: an arbitrary JSON blob meaningful to its executor and opaque to rimsky. An HTTP-calling node's `userdata` specifies URL, method, headers, body. An LLM-calling node's `userdata` specifies model, prompt, tool list, result schema. A SQL-executing node's `userdata` specifies statement and bindings.

The orchestrator does not parse, validate, or template-substitute `userdata`. Its contents reach the executor byte-for-byte as supplied in the template (after placeholder substitution if configured — see §7.2). An executor with a schema for its `userdata` is free to validate on receipt and reject with a `Blocked` or `Errored` terminal event.

This opacity is load-bearing: it is what lets rimsky serve every domain without growing a per-domain vocabulary. The cost is that rimsky cannot catch a template author's typo in the userdata block; that's the executor's job.

### 3.5 Node state

A node is always in exactly one of four operational states:

- `fresh` — has completed successfully; no work pending. The steady state.
- `stale` — has been invalidated; scheduled for re-execution when dependencies are fresh. If the node owns resources, previous versions remain live for readers.
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
- any → `fresh` — `invalidate(restore_version)` with resource-supported rollback.

**"Finished" is not terminal.** A node that has completed successfully is `fresh`. It can return to `stale` on invalidation. The only truly terminal state is `failed`, exited only by external intervention.

**The state machine rejects illegal transitions.** In particular, `running → running` under reason `dispatch_claimed` is not silently idempotent — it raises an error. This is the load-bearing invariant against double-execute (see §11.1); any implementation that adds an idempotency short-circuit "for ergonomics" breaks the property.

### 3.6 Probes are resource-owning nodes, not guards

A probe is a node whose work exercises upstream resources in a realistic scenario. Examples: a `test-adapter` probe uses a config to fetch samples and produces a report; a `probe-ingestion` node exercises a full pipeline at small N before full ingestion commits.

Probes own their own resources — the probe's report is a resource consumed downstream. A probe does work that produces data; that work happens to validate upstream along the way. Its failure path uses the normal error-class → policy chain.

Convention: probe error classes should describe observable facts (`fetch_non_json`, `zero_records_returned`), not interpretations of upstream (`config_is_broken`). Observable facts compose — the same symptom can have multiple upstream causes, and the policy decides how to react.

---

## 4. Resources

Resources are the data layer. A resource has:

- An identity — a structured path tuple (`ResourceId`), e.g. `("production", "project-alpha", "items")`.
- A schema, declared by whatever is appropriate to its implementation.
- Versioning state (`current_version`, `previous_version`, optional deeper history).
- Quality rules with per-rule severity.
- An implementation — the backing store type and its configuration.
- Optional retention policy.

Exactly one node owns each resource (the sole writer). Any number of consumers may read it — other nodes, external services, the API.

### 4.1 Pluggable implementations

Rimsky does not specify a single storage model. Instead, each resource declares an **implementation** by name (e.g. `inline-jsonb`, `external-sql`, `s3-object`). An implementation is a Go struct satisfying the `Resource` interface; it decides how to store versions, how to roll back, how to run quality rules, how to GC old versions.

Implementations register themselves at process startup (the orchestrator deployer's `main()` wires them in). A template references an implementation by name and supplies an implementation-specific `config` block. Template validation checks the config against the implementation's declared JSON schema at deploy time, so operators see errors up front rather than at instantiation.

v1 ships two reference implementations:

- `inline-jsonb` — data stored directly in rimsky's own `rimsky_resource_versions.data` column. Small, fast, schema-flexible. Fit for internal blobs, reports, intermediate artifacts.
- `external-sql` — data written to a consumer-owned SQL table via a declared connection. Uses a staging-table + atomic-swap pattern for double-buffering. Fit for tabular production data consumed by external services (APIs, MCP servers, dashboards).

A third implementation, `s3-object`, is deferred to post-v1. The framework is the same; only the binding is new.

### 4.2 Versioning and double-buffering

Versioning lives on the resource. Each version has a monotonic identifier and a committed-at timestamp. When the owning node completes a run:

1. The new output is validated against the resource's quality rules (the resource's implementation runs them — the orchestrator does not).
2. If any `severity: error` check fails, the new output is discarded. `current_version` remains the last good version. The node treats the quality failure as an error class and runs its repair policy.
3. Otherwise the node commits with an explicit `changed: bool` verdict (see §4.3):
   - `changed: true` → new output becomes `current_version`; prior moves to `previous_version`; older versions garbage-collected per retention policy. Node emits `recalculate` to dependents.
   - `changed: false` → `current_version` unchanged; a `no-op commit` event is logged; no `recalculate` fans out. Dependents are not awakened.

Rollback is implicit on quality failure. When a node produces bad output, previous good versions stay live for readers; there is no "rollback" operation to trigger. It falls out of the commit-or-reject flow on the resource.

Explicit rollback is a resource-level capability (§4.6).

### 4.3 Change verdict (the `changed` field)

A hash of output content would be fragile — agent output differs on cosmetic whitespace, database-backed resources differ on row order or timestamps. Instead, the producer declares whether its output differs meaningfully from the previous version:

- **Deterministic executors** compute `changed` however is right for their domain: row-count + sampled-row equality, a domain-specific diff, or a normalized canonical-form comparison.
- **Agentic executors** report the verdict via the protocol's `Complete` event: `{result, changed, change_summary}`. The agent declares whether its work materially changed anything; the orchestrator records the claim verbatim.

An optional `change_summary` string accompanies `changed: true` — a human-readable note like "3 new zone codes added; 1 boundary refined." This is often more useful to operators reading the event log than a fingerprint.

The runtime does not hash content on its own. Invalidation still cascades; `changed` simply governs whether the cascade continues *forward* from a given commit. A node that concludes "nothing meaningful changed" stops propagation at itself, avoiding wasted downstream re-runs.

**Trade:** the system trusts producers to make honest `changed` calls. Mitigations: the claim is recorded on every commit event; operators can audit; resources whose `changed: false` claims prove wrong can have quality rules asserting minimum-change criteria.

### 4.4 Access methods

A resource declares how it can be accessed. Three tiers, chosen per-resource based on who needs to consume it:

- **Internal blob.** Small, schema'd informally, consumed only by direct dependents. Addressed by a URL or key; no protocol layer. Cheap — no formalization overhead.
- **Named resource.** Formal schema, accessible by name from anywhere in the system. Consumed by multiple nodes, possibly cross-instance.
- **Published resource.** Named resource with external access methods (SQL table, HTTP endpoint, MCP server) for agentic nodes, external services, and downstream APIs.

Every resource-owning node produces at least an internal blob; "promote to named" and "publish via SQL / REST / MCP" are opt-ins. One node can own multiple resources at different tiers.

The access methods available depend on the implementation. `external-sql` inherently publishes via SQL. `inline-jsonb` is an internal blob by default but can be surfaced through the control API. Future implementations may add their own methods.

### 4.5 Ownership vs. access

Ownership is write authority. Access is read permission. These are independent:

- A resource has exactly one owning node. Only the owner writes new versions.
- A resource can be readable by many nodes and external consumers, via any access method the resource exposes.

External services that read resources do not participate in the node graph. A dashboard reading a published SQL resource queries the table directly; the node graph does not see it. Similarly, an agentic node can consume a published resource via MCP without being a declared dependent — it reads a value at the moment it asks, with no freshness guarantee relative to its own run.

### 4.6 Rollback as a resource concern

Rollback is a capability of the resource's implementation, not a feature of the message system. When a node receives an `invalidate` message with `restore_version` set, the supervisor asks the resource: "can you roll back to this target?" The resource decides.

Three states:

- **Supported.** The resource swaps `current` and `previous` atomically (or restores to the named version id). Node transitions to `fresh` without re-executing; `recalculate` fans out.
- **Supported with constraint.** The resource may reject specific rollback targets (e.g. one that has already been garbage-collected per retention policy).
- **Unsupported.** The resource returns `ErrRollbackUnsupported`. Policy chains that invoked rollback see this as an error; the chain proceeds to its next action.

This architecture — resources own their rollback semantics — is new vs. the early design sketches, which treated `restore_version` as a message-level feature the orchestrator interpreted. In v1, the message is unchanged (`invalidate` carries `restore_version`), but the supervisor routes it to the resource and *asks*; the resource decides. This makes rollback composable with implementations that don't inherently support it and prevents the orchestrator from having to know about every resource's storage model.

### 4.7 Dependencies vs. resource reads

Dependencies and resource reads are related but distinct:

- **Dependency.** "Don't run me until this node is fresh." Gates execution order. A node that depends on A reads A's resources as part of its work, and waits until A has completed.
- **Resource read.** "I can read this resource." Does not gate execution. A node can read a resource without declaring a dependency on its owner — useful for cross-instance lookups or discovery — in which case it simply reads the resource's current version at the moment it asks, with no freshness guarantee relative to its own run.

Most nodes both depend on and read from their upstreams. The distinction matters for the few cases where a node needs to consult a resource without being schedule-coupled to it.

---

## 5. Messages

Two message types carry all inter-node communication.

### 5.1 `invalidate`

"You are stale."

- Target node marks itself stale.
- If `restore_version` is set: the supervisor asks each owned resource whether rollback is supported; if all succeed, the target swaps resources to that version, emits `recalculate` to dependents, returns to `fresh`, and does not re-execute. (This is how explicit rollback is modeled.) If any resource cannot roll back, the message proceeds as an ordinary invalidate.
- Otherwise the target propagates `invalidate` to all its dependents and schedules itself for re-execution.
- Previous versions of owned resources stay live throughout.
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

Resource reads are not messages. A node (or external service) that reads a resource does so on demand; the resource does not emit change notifications. Nodes that want to react to resource updates should declare a dependency on the owning node.

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

Errors are node-local. Each resource-owning node defines its own error-class taxonomy and maps each class to an ordered fallback chain of actions.

### 6.1 Actions

- **`retry`** — re-execute the node. Parameters: `count` (max attempts), `backoff` (linear | exponential), `base_delay_ms`, `max_delay_ms`, `jitter` (none | plus_minus).
- **`invalidate(targets)`** — emit `invalidate` messages to one or more nodes. Optional `restore_version`. The node itself stays `stale`, awaiting re-execution after upstream refreshes.
- **`give_up`** — transition to `failed`. Optional `reason_template` for the event log.

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

---

## 7. Parameterization

Nodes consume three distinct kinds of input.

### 7.1 Instance params

Shared across all nodes of one template instance. Template authors declare `params_schema` (JSON Schema) at the template level; operators supply params at `POST /instances`. Params are typically stable identity information (source name, consumer id, region) or instance-specific hints. A `params_redact` list marks top-level keys to redact in control-API display (credentials, secrets); placeholder substitution reads the unredacted values.

### 7.2 Placeholders

String fields in template YAML can reference:

- `{instance_id}` — the rimsky-assigned instance UUID.
- `{consumer_key}` — the consumer-supplied key for this instance.
- `{params.<key>}` — a top-level value from the instance's params.

Scope of substitution, at instantiation time (not runtime):

- `concurrency_tags[*]`.
- `owns_resources[*].path[*]` — rejected if any segment fails to resolve (no silent empty-string fallback).
- `owns_resources[*].config` — recursively across string leaves, before the implementation's `Factory.Create` is called.
- `reads_resources[*].path[*]`.

`userdata` is **not** substituted by rimsky. The opaque-block promise (§3.4) means rimsky does not interpret or rewrite its contents. Executors are free to implement their own templating over `userdata` using data delivered in the `ExecuteRequest` (which includes `instance_params`).

Unresolved placeholders at instantiation return a 400 from `POST /instances` with the offending field path; nothing is committed.

### 7.3 Dependency and resource data at execution time

Outputs from upstream nodes are accessed at dispatch time by the supervisor: before calling the executor, the supervisor resolves the current version of each declared dependency's owned resources and each declared `reads_resources` entry, and passes them into the `ExecuteRequest` as `deps_data` and `reads_data` maps. Executors do not query resources; the orchestrator hands them over.

An executor that wants to take advantage of dependency data in `userdata` (say, an HTTP-calling node whose body references a config from upstream) templates over `userdata` itself, at execute time, using the delivered maps. The protocol supplies the raw material; the executor decides how to weave it in.

### 7.4 Mutability

Instance params are user intent and stay stable. Discovered or learned information lives in the node that discovered it (on resources it owns, versioned and double-buffered). Nodes that need such information read it as dependency data. This keeps params stable and clean; evolving state lives where the graph's state machine can manage it.

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

schedule: "<cron expr>"            # Optional. UTC. Scheduler emits invalidate on fire.

dependencies: [string]             # Sibling node types. Gates execution order.

concurrency_tags: [string]         # Scheduler enforces per-tag limits at dispatch time.

owns_resources:                    # Resources this node writes. Omitted for pure-cascade.
  - path: [string]                 # ResourceId segments. Placeholders allowed.
    implementation: string         # "inline-jsonb" | "external-sql" | ...
    config: {...}                  # Implementation-specific. Placeholder-resolved.
    retention:
      keep_versions: 2             # Default: current + previous
    quality_rules:
      - type: string               # Builtin or custom rule-type name
        config: {...}
        severity: error | warning  # error blocks commit; warning logs only

reads_resources:                   # Resources read outside dependency chain. Optional.
  - path: [string]
    via: string                    # "internal_blob" | "sql" | "rest" | ...

error_types:                       # Per-node error taxonomy with policy chains.
  <error_class>:
    policy:
      - action: retry
        count: int
        backoff: linear | exponential
        jitter: none | plus_minus
        base_delay_ms: int
        max_delay_ms: int
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

Resource state (`current_version`, `previous_version`) lives on the resource, not the node. A node with multiple owned resources has version state per resource.

### 8.2 Default handlers

Nodes do not declare message handlers; they inherit system defaults.

**on_invalidate(msg):**
1. If `msg.restore_version` is set: ask each owned resource whether that target is supported. If all accept, swap each to the target version, emit `recalculate` to dependents, return to `fresh`.
2. Else if already `stale` or `running`: no-op.
3. Else: transition to `stale`, keep previous versions of owned resources live, emit `invalidate` to all dependents, schedule self for re-execution.

**on_recalculate(msg):**
1. If `fresh`: no-op (may update which upstream version has been acknowledged).
2. If `stale`: check all `dependencies` — if any stale, no-op (node will be nudged again). If all fresh, enqueue a dispatch row (executor nodes) or transition inline on the next scheduler tick (pure-cascade).

**on_work_complete(result, changed, change_summary):**
1. For each owned resource: hand the result and verdict to the resource's implementation. The implementation runs quality rules, applies severity, returns accept-or-reject. `severity: error` failures → treat as `error(quality_rule_failed)`. `severity: warning` failures → logged internally, do not block.
2. If accept and `changed: true`: implementation commits, prior becomes `previous_version`, emit `recalculate` to dependents, log `commit` event with `change_summary`, transition to `fresh`.
3. If accept and `changed: false`: implementation records a `no_op_commit`, `current_version` unchanged, no `recalculate`, transition to `fresh`.
4. Non-resource-owning executor node: transition to `fresh`, no commit path.
5. Pure-cascade node: transition handled by scheduler sweep, not supervisor.

**on_error(error_class):**
1. Look up class in `error_types`. Missing → treat as `give_up` with unknown-class reason.
2. Take the action at `action_index` for that class.
3. `retry` exhausts → advance `action_index`. `invalidate` → emit, stay `stale`, let recurrence advance `action_index`. `give_up` → transition to `failed`.
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
   - Pure-cascade nodes do not own resources.
   - `userdata` on a node without `executor` warns.
   - `owns_resources[*].implementation` references a registered implementation.
   - `owns_resources[*].config` validates against the implementation's declared JSON schema.
   - `owns_resources[*].path` placeholders resolve from the declared placeholder set.
2. Valid templates are stored; validation failures return 400 with the offending field.

### 9.2 Instance registration

`POST /instances` with `{template_id, consumer_key, params}`:
1. Validate `consumer_key` unique within `template_id`.
2. Validate `params` against the template's `params_schema`.
3. Allocate instance UUID.
4. For each node: allocate node UUID; resolve `dependencies` to sibling node UUIDs; resolve placeholders across `concurrency_tags`, `owns_resources[*].path`, `owns_resources[*].config`, `reads_resources[*].path`; provision resources through their declared implementations (registry row plus any implementation-specific setup, e.g. creating target SQL tables).
5. For nodes with `schedule`: compute `next_fire_at` from cron and current clock; write to schedule table.
6. Log `state_transition` events for all nodes (initial state `stale`).
7. Enqueue dispatch rows for root executor nodes (no dependencies, has `executor`).
8. Root pure-cascade nodes will be transitioned inline by the scheduler on its next tick.

### 9.3 Execution

For each node the scheduler picks up:

1. **Pure-cascade nodes** (no `executor`): the scheduler transitions `stale → fresh` inline on its sweep, emits `recalculate` to dependents, logs a `pure_cascade_commit` event. No dispatch row, no supervisor, no executor RPC.
2. **Executor nodes**: a dispatch row sits in the queue. A supervisor whose config accepts the node's executor name claims the row under `FOR UPDATE SKIP LOCKED`.
3. After claim, the supervisor **re-reads `claimed_by`** (the verify-before-run invariant — see §11.1) before doing any work. If the claim has been released or re-claimed, the supervisor bails and the system cleans up.
4. The supervisor resolves instance params, dependency resource versions, and `reads_resources` values, then issues `Execute(node_id, instance_id, userdata, deps_data, reads_data, instance_params, callback_url, cancel_token)` to the executor's configured endpoint and transport.
5. The executor returns a stream of zero or more `Heartbeat` events followed by exactly one terminal event: `Complete` (with `result`, `changed`, `change_summary`), `Blocked`, `Errored`, or `AsyncAccepted` (async handoff).
6. For `Complete`: the supervisor hands the result to each owned resource's implementation for quality-rule evaluation and commit. Accept → commit, log `commit`, emit `recalculate`. Reject → route through `on_error(quality_rule_failed)`.
7. For `Blocked`: route through `on_error(executor_blocked)` unless the template declares a more specific class.
8. For `Errored`: route through `on_error(error_class)` with the executor-supplied class.
9. For `AsyncAccepted`: the supervisor holds the dispatch claim and keeps the node `running`; a callback POST from the executor (carrying the eventual `Complete` / `Blocked` / `Errored`) completes the dispatch later. See `protocol.md` for the callback contract.
10. On any failure path: the policy chain consults `error_types`; actions are taken; the node's state advances accordingly.

### 9.4 Pure-cascade execution

Pure-cascade nodes never enter the dispatch queue. The scheduler's pure-cascade sweep runs on every tick: for each `stale` node with no `executor` and all dependencies `fresh`, transition `stale → fresh` inline, emit `recalculate` to dependents, log `pure_cascade_commit`. No `work_started` / `work_completed` events. Commit verdict is always `changed: true`; propagation is the purpose. This path is what makes schedule-driven fan-out (one root node with a `schedule`, dozens of dependents) efficient: the root's "work" is a state transition and a fanout message wave, handled in-process to the scheduler.

### 9.5 Observability

The `rimsky_events` log is the single source of truth for what happened:

- Every message delivered (both types).
- Every state transition with cause (`state_transition`).
- Every error occurrence (`error`, with class and payload).
- Every work start / complete (`work_started`, `work_completed`).
- Every commit (`commit` with `change_summary`), no-op commit (`no_op_commit`), quality rule failure (`quality_rule_failed`).
- Every schedule fire (`schedule_fired`).
- Every operator override (`operator_override`).
- Every heartbeat loss (`heartbeat_lost`), orphaned claim release (`orphaned_claim_released`), orphaned claim lost-race (`orphaned_claim_lost_race`).
- Unresolved executor (`unresolved_executor`), work rejected (`work_rejected`) when executor output fails protocol-level validation.
- Pure-cascade commit (`pure_cascade_commit`).

Operators reading the log can reconstruct the full trajectory of any node without touching runtime state. Replay and debugging work by re-routing messages against a frozen graph and watching the state machine evolve.

---

## 10. Design principles

Principles that fell out of working through the model. Cross-references to the sections that embody each.

### 10.1 Declarative by default, agentic where irreducibly needed

Failures that are enumerable in advance are handled by declarative policy (§6). Only truly novel failures (error classes no policy matches) end in `give_up`, where a human or optional reasoner steps in. LLM cost is concentrated in nodes whose *work* is LLM-backed, not in overseeing the graph.

### 10.2 No privileged controllers

Every unit of work, including any maintenance or cross-instance reasoning, is a node subject to the same primitives (§3). There is no scheduler-node, no maintenance-agent sitting above the graph, no special class of "orchestrator." This keeps the surface area small and composition uniform.

### 10.3 Work and data are separate

Nodes are actors; resources are artifacts (§4). Node logic evolves independently from resource schema and access. This makes resources independently consumable — by other nodes, external services, agentic discovery — without coupling consumers to producers' internals.

### 10.4 Double-buffering is default

Resource writers never leave consumers with broken data (§4.2). A failed re-run leaves the previous good version live until a successor succeeds. Implicit rollback is the natural consequence.

### 10.5 Errors are node-local

A node's error taxonomy describes *its own* failure modes (§6.4). Downstream nodes see only the consequences (`invalidate`, `recalculate`), never the class. No shared error vocabulary needs to be maintained.

### 10.6 Human review as timeout, not gate

Review is the fallback when self-repair exhausts (`give_up`), not a ceremonial step on happy paths. Consequential operations that should require human sign-off are gated through quality rules plus probe nodes that verify against real data, not through blanket review flags.

### 10.7 Observability is structural

The event log is the system's truth, not a derived artifact (§9.5). Every state transition, message, error, and resource version change is a row. Dashboards and reasoners read the log rather than polling nodes.

### 10.8 Structured completion over inferred

Executors report completion through a typed protocol event, not through file artifacts or exit-code inference (see `protocol.md`). This localizes structure to the interface (the event's schema) rather than to a serialization convention, allows in-conversation correction when output is malformed (by executors that host their own in-process correction loops), and makes "I am done" an explicit act rather than an emergent property of a process ending.

### 10.9 Monitor, don't deadline

Agentic work has a heavy-tailed runtime distribution (§6.3). Hard deadlines trade legitimate long runs for catching hangs — a bad trade, because hangs can be detected by heartbeat monitoring and stream analysis without a deadline. Monitoring is primary; deadlines (if used at all) are soft warnings for observability.

### 10.10 Producer owns the change verdict

A content-hash over node output would be fragile (§4.3). Each producer declares on commit whether its output differs meaningfully from the previous version (`changed: bool`). The runtime does not hash content. An honest producer is the best judge of whether its output matters; the claim is recorded on every commit for audit.

### 10.11 Executors are peers, not subsystems

An executor is a separate service the orchestrator calls over the wire (see `protocol.md`). It does not run inside the orchestrator process; it does not register runtime state with the orchestrator; it is not a plugin loaded into the orchestrator's memory. This is deliberate: executors in different languages, with different runtime needs (GPU, subprocess spawning, long-lived internal state), are operationally peers. The orchestrator sees one interface — the protocol — and knows nothing of how the executor is implemented.

The practical consequence: authoring a new executor requires no orchestrator changes, no recompilation, no redeployment of the orchestrator. An executor can fail, upgrade, or restart independently of the orchestrator. This is the architecture property that makes rimsky domain-agnostic.

### 10.12 Resources own their rollback semantics

Rollback is a resource capability, not a message-level feature (§4.6). The orchestrator does not interpret `restore_version`; it asks the resource. Resources that cannot roll back return `ErrRollbackUnsupported`, and the node's policy chain proceeds. This makes `invalidate(restore_version)` safe to declare on any node — resources that support it act; resources that don't, don't. No orchestrator knowledge of implementation internals.

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

A two-level SLO on a resource (`warn_after`, `fail_after`). `warn_after` elapses without new commit → observation logged. `fail_after` elapses → owning node invalidated. Complements invalidation rather than replacing it. Deferred post-v1.

### 12.2 Observer nodes

A node whose job is to report the version of something external we don't control (an ArcGIS ETag, a website's last-modified header). Emits `invalidate` when the external thing changes. Deferred post-v1.

### 12.3 External trigger nodes

A node that emits `invalidate` in response to an external webhook or event. Deferred post-v1.

### 12.4 Priority on nodes/messages

Integer priority on the dispatch queue — human-triggered runs preempt scheduled runs. Deferred post-v1.

### 12.5 Additional resource implementations

`s3-object` (bytes in S3/MinIO, manifest in Postgres) is the most immediate post-v1 candidate. Others — `redis-stream`, per-tenant filesystem — as demand emerges.

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

### 12.11 Schema evolution on resources

Coordinated migration of a resource's schema (new version published, readers updated, old deprecated) is unspecified. To be designed before any published resource has many external consumers.

### 12.12 Resource-level subscriptions

Readers of a resource currently do not receive change notifications; they read current versions on demand. A future enhancement could allow a node to subscribe to resource changes rather than to a specific writer node (decoupling reader from writer identity). Adds machinery; defer until a concrete use case demands it.

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

- **Node** — The unit of work. Properties: may have an executor, userdata, schedule, dependencies, concurrency tags, owned resources, read-only resources, error taxonomy.
- **Pure-cascade node** — A node with no `executor`. Transitions `stale → fresh` inline when dependencies settle; exists to propagate cascade without doing work itself.
- **Executor node** — A node with an `executor`. Dispatched via the protocol; work is done externally.
- **Scheduled node** — Any node with a `schedule`. Scheduler emits `invalidate` when the cron fires.
- **Fan-out node** — A pure-cascade node with a `schedule` and many dependents; invalidate wave fans out on cron fire.
- **Executor** — A peer service that implements the node-executor protocol and does work on behalf of nodes that reference it by name.
- **Executor name** — The string that appears in `nodes[*].executor` and is resolved by supervisor config to an endpoint.
- **Resource** — The unit of data. Has schema, versioning, access methods, retention, and quality rules. Owned by exactly one node; readable by many.
- **Resource implementation** — A Go implementation of the `Resource` interface (e.g. `inline-jsonb`, `external-sql`). Registered by name, referenced by templates.
- **Userdata** — An opaque JSON block on a node template, passed verbatim to the executor on `Execute`. Rimsky does not interpret it.
- **Schedule** — Optional cron expression on any node. Scheduler emits `invalidate` when it fires.
- **Probe** — A resource-owning node whose work exercises upstream resources realistically. Not a guard; its output is consumed downstream.
- **Access method** — How a resource is read: internal blob, named, or published (SQL / REST / MCP).
- **Internal blob** — A resource consumed only by direct dependents; no protocol layer.
- **Named resource** — A resource with a formal schema, addressable by name from anywhere.
- **Published resource** — A named resource with external access methods.
- **Dependency** — Declared node-to-node edge: "I wait for you to be fresh before I run."
- **Resource read** — A node's declared ability to read a resource. Separate from dependency — reads don't gate execution.
- **Ownership** — Write authority on a resource. Independent of read access.
- **Message** — `invalidate` or `recalculate`. The only two types.
- **Error class** — A per-node label for a failure mode. Key in the node's repair-policy map.
- **Policy chain** — Ordered list of actions for one error class. Advances on recurrence; resets on success.
- **Quality rule** — A check on a node's new output before it becomes a resource's current version.
- **Instance params** — User-supplied configuration per template instance. Stable; no rewriting from within the graph.
- **Placeholder** — `{instance_id}`, `{consumer_key}`, or `{params.<key>}` in string fields. Substituted at instantiation, not runtime. Not applied to `userdata`.
- **`fresh` / `stale` / `running` / `failed`** — The four node states.
- **`current_version` / `previous_version`** — The double-buffered version pointers on a resource.
- **`rimsky_events`** — The append-only log of messages, state transitions, errors, and resource version changes. The system's source of truth.
- **ResourceId** — Structured tuple of strings identifying a resource (e.g. `("production", "project-alpha", "items")`), rendered colon-separated for display.
- **`changed` (commit verdict)** — Boolean declared by the producer on every commit: true if the new output differs meaningfully from the previous version, false otherwise. Governs whether `recalculate` fans out.
- **`change_summary`** — Optional human-readable note accompanying a `changed: true` commit. Appears in the event log.
- **Check severity** — `error` (blocks commit, triggers policy) or `warning` (logs, does not block).
- **Observation** — Event type: node inspected a resource without producing a new version. (Deferred as a first-class event in v1; pattern recorded via general event log.)
- **Concurrency tags** — Tags on nodes used by the scheduler to enforce per-tag concurrency limits (e.g. `per-instance:{id}` limit 1).
- **Scheduler** — Long-running process that reads node and resource state and dispatches work by enqueueing dispatch rows and transitioning pure-cascade nodes inline.
- **Supervisor** — Worker process that claims dispatch rows, calls executors over the protocol, and persists outcomes.
- **Control API** — HTTP+JSON interface for deploying templates, creating instances, applying operator overrides, reading events.
- **Three collections** — The architectural separation of orchestrator, resource library, and executor library. Versioned together in v1, separable indefinitely.
- **Async handoff** — Protocol pattern where an executor returns `AsyncAccepted` immediately and later posts the terminal outcome to a callback URL. Lets executors with long-running internal work avoid holding the `Execute` stream open.
- **Conformance suite** — The `rimsky-conformance` binary that validates a given executor endpoint against the protocol contract.
- **`@blessed-invariant`** — A source annotation marking a load-bearing property that implementation changes must preserve.

---

## Appendix: Relationship to the TypeScript predecessor

Rimsky v1 (Go) is preceded by a TypeScript implementation, kept in the originating monorepo and used through its lifecycle as a proving ground. The TypeScript project called the unit of work a "cell"; the Go v1 calls it a "node." The rename was mechanical: every identifier, table name, proto message, and doc sentence changed — `cell → node`, `cell_events → rimsky_events`, `CellExecutor → NodeExecutor`. The concept did not change; the state machine, message semantics, policy-chain evaluator, event-kind taxonomy, and commit flow are identical.

The rename was done for three reasons. First, state left the node: resources own versioning, rollback, and data. The "stateful self-contained cell" metaphor no longer applies — nodes are graph-vertex declarations referencing executors and resources. Second, execution left the node: executors run the work as peer services; a node holds no execution logic. Third, native vocabulary: in conversation, designers and reviewers consistently drifted to "node"; vocabulary that requires discipline to sustain is the wrong vocabulary. "Node" also aligns with the orchestration landscape (Airflow, Temporal, Argo, Dagster, LangGraph all use "node," "task," "step," or "activity").

This is the only appearance of "cell" anywhere in this document. From here forward, rimsky uses "node."
