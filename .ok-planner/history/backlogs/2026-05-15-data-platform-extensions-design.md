# Data platform extensions for rimsky

**Date:** 2026-05-15
**Status:** spec
**Supersedes (within rimsky's evolution):** the data-engineering material in `sketch:2026-05-14-data-platform-extensions` and the architectural framings preceding it. The sketch remains as historical record; this spec is the consolidated design.

## What this covers

The platform extensions that make rimsky credible as a data-processing platform for data-engineering workloads. Five architectural threads, designed to compose:

1. A new `DataProcessing` service protocol mixed in alongside `ClaimProducer`, with a basket of bundled DataProcessing-capable stores. Substrate is handled out-of-process by stores; rimsky stays substrate-agnostic.
2. A `rimsky_node_runs` extension to a recursive run-tree, supporting fan-out parents, sub-graph invocations, and arbitrary tree depth, with rule-based state aggregation.
3. Recursive scope partitioning at the `ClaimProducer` layer, enabling fan-out over sub-scopes with producer-defined partitioning and producer-aware conflict detection.
4. A unified **message** layer for boundary-crossing dispatch — operator API, sensors (including the bundled `sensor-cron` which replaces the per-node `schedule:` field), and (V2) cross-instance triggers all go through one mechanism, with receivers matching via the existing `subscribes:` infrastructure.
5. Sub-graphs as a first-class graph construct, exercising the run-tree's general-tree shape. The entry node is absorbed into the calling node (same `rimsky_nodes` row, same executor, same dispatched run); the exit node remains a child of the calling node's parent run with a writeback carry-rule.

Plus a `Validation` cross-cutting protocol, a `Sensor` service kind, content lineage, the asset pattern as a documented compound (no new primitive), claim co-holdership via `holds:`, claim lifetime, atomic-staging worked example, parked-state taxonomy, backfills as a message convention, and bundled deliverables (stores, executors, sensors, lifecycle-subscribers).

## What this does NOT cover

- **Per-language SDKs.** Tracked separately by `sketch:2026-05-14-rimsky-development-kit`, which will produce its own spec.
- **Implementation phasing.** Phasing belongs in the corresponding plan, not the spec.
- **Operator deployment topology** for bundled stores / sensors / executors. Specified in their own deployment docs.
- **Column-level lineage**, deferred to V2.
- **Cross-instance messaging**, deferred to V2 (the message envelope shape accommodates it; the control-api endpoint shape accommodates it; only the addressing rules are unspecified for V1).
- **Generator-style fan-out** (`fan_out.over` from an executor-emitted iterable), deferred to V2.
- **Dynamic and multi-dimensional partition types**, deferred to V2.
- **Cancellation-of-in-flight-frame primitives**, deferred to V2 (backfill cancellation in V1 only blocks future-enqueued work; in-flight frames complete normally).
- **Cross-template sub-graph reuse**, deferred to V2 (sub-graphs are inline in V1; not separately content-addressed).

## Vocabulary

### New nouns

- **Graph** — unit of node connectivity. Templates declare one or more graphs uniformly. The reserved name `main` is the top-level graph.
- **Sub-graph** — a graph with declared `entry` and `exit` nodes; invocable from another node via `delegate:`.
- **Delegation** — the relationship between a calling node and a sub-graph. The calling node and the sub-graph's entry node share runtime identity (same `rimsky_nodes` row, same executor); the exit node remains a separate child of the calling node's parent run, with its writeback flowing back to the calling node via the carry-rule.
- **Asset** — a claim against a `DataProcessing`-capable store with `lifetime: durable`. A documented compound, not a new primitive.
- **Claim co-holdership** — multiple node-runs holding the same claim_handle via the `holds:` template directive. Distinct from acquiring a claim (`claims:`).
- **Claim lifetime** — per-claim property determining when the claim auto-terminals: `subgraph` (default) releases at holding-subgraph completion; `durable` persists past completion.
- **Fan-out** — a node-level decision to partition a held claim into sub-claims, dispatching one work unit per sub-claim. Always tied to claim partitioning.
- **Sub-claim** — a claim whose scope is a sub-scope of a parent claim's scope. Held by a child run.
- **Run-tree** — the parent-child tree structure on `rimsky_node_runs`, with `parent_run_id` and `child_key` columns. Supports fan-out and sub-graph invocation.
- **Aggregation** — state-and-output combination of children's outcomes into the parent run's state and writeback. State aggregation is rule-based; output aggregation is producer-handled at `Commit`.
- **Message** — boundary-crossing dispatch unit with envelope (kind, sender, target, payload). Persisted in `rimsky_messages`; matched by subscribers.
- **Sensor** — first-class in-instance service that runs continuously, monitors external state, and pushes messages to its instance.
- **Lineage record** — append-only record in `rimsky_lineage`; two kinds (`leaf_run`, `claim_commit`) capturing computational and data-promotion units.

### Updated nouns

- **Attribute** — unchanged in semantics; expanded in usage to coexist with claims and assets in templates.
- **Claim** — gains lifetime, may have parent (sub-claim chain), may have co-holders.
- **Claim handle** — may carry `held_durable: true` to persist past holding-subgraph completion. Gains `parent_claim_handle_id` for sub-claims.
- **Claim holders** — `rimsky_claim_holders` extended; holders are runs, not nodes.
- **Claim producer** — gains three optional methods: `SplitScope`, `ScopesConflict`, plus the new `Validation` mix-in available alongside.
- **Cascade** — same mechanism; walks subscription edges; descends through delegation (calling node) but stops at sub-graph boundary from outside.
- **Frame** — same mechanism; messages-delivered-at-boundary is a new frame creation site.
- **Invalidate** — same mechanism; now one `kind` of message envelope, the V1 kind.
- **Node-run** — extended into a tree; leaf runs dispatch executors, parent runs aggregate over children, may run executors (sub-graph entry's executor) per the delegation rule.
- **Parked state** — gains `parked_reason` taxonomy (`TIME_WAIT | CALLBACK_WAIT | RETRY_BACKOFF | OTHER`) + freeform label.
- **Service** — adds the `sensor` service kind alongside `executor`, `claim_producer`, `lifecycle_subscriber`.
- **Subscription** — adds a fourth topic kind alongside the three existing kinds (`state`, `attribute`, `event`): the new kind `message`, with filter fields (`kind`, `sender`, `sender_kind`, `target`).

### Retired nouns

- **`node-state` concept** — state lives entirely on `rimsky_node_runs` now; `rimsky_nodes.state` column is removed. State-machine semantics described under `concept:node-run`.
- **`quality-rule` concept** — replaced by the verifier-executor pattern (bundled executors that co-hold claims and run checks). The `graph/qualityrule/` package is deleted; AGPL constraint dissolves with it.
- **Aggregator vocabulary as a rimsky concept** — aggregators (`map_partitioned`, `union`, `merge`, etc.) are producer-internal vocabulary, declared in claim `data:` blocks and interpreted at `Commit`. Rimsky doesn't enforce a shared aggregator vocabulary.

## Architectural shape

Rimsky stays substrate-agnostic. Data semantics — versioning, partitioning, materialization, schema, retention — live in `ClaimProducer` services that opt into the `DataProcessing` mix-in protocol. Rimsky's role is orchestration: dispatch, cascade, frames, claim lifecycle, state aggregation, message delivery, lineage.

Three orthogonal mix-ins layer on top of the base `ClaimProducer` protocol, advertised in the existing `Capabilities` handshake:

- **`DataProcessing`** — control-plane methods for typed-data version lifecycle (candidates, partitions, version metadata). Data motion stays substrate-direct via the `ClaimResult` address (B from the substrate-direct-data-access decision); the protocol carries only control-plane.
- **`Validation`** — registration-time validation of node userdata against producer or executor surface, per a `role` discriminator.
- **Producer-side scope partitioning** (`SplitScope`, `ScopesConflict`) — optional methods on `ClaimProducer` itself; producers that support partitioning advertise.

A new service kind, **`Sensor`**, exists alongside executor / claim-producer / lifecycle-subscriber. Sensors are deployed externally, configured per-instance via the template's `sensors:` block, run continuously, and push messages to rimsky's control-api when they observe external state changes.

Orchestration changes inside rimsky:

- The run-tree extension to `rimsky_node_runs` (parent / child columns).
- The aggregation engine (rule-based, error-policy-aware).
- The unified message queue (`rimsky_messages` + delivery at frame boundary).
- The recursive claim resolution (sub-claims form a tree parallel to the run-tree; auto-terminal walks bottom-up).
- The lineage append (`rimsky_lineage`, projected from events + claim lifecycle).
- Sub-graphs (template-DSL + entry-node absorption + exit-node writeback carry-rule + run-tree composition).

## Persistence schema

All schema changes apply atomically per pre-v1 baseline; no compatibility migrations.

### `rimsky_node_runs`

New columns:

- `parent_run_id UUID NULL` — FK self. NULL for top-level (root) runs.
- `child_key TEXT NULL` — child identity within parent's namespace. For fan-out children: the partition / iteration value. For sub-graph internal nodes: the internal node's alias. For root runs: NULL.
- `aggregation_policy JSONB NULL` — snapshotted from the template-node spec at run creation time. Encodes the failure policy (`strict.cancel_siblings`, `threshold`, `best_effort`, `first`) for this parent run's aggregation. NULL for leaf runs.

Columns lifted from `rimsky_nodes` (now state lives entirely here):

- `state TEXT NOT NULL` — `fresh | stale | running | failed | parked`.
- `last_outcome TEXT NOT NULL` — `fresh_changed | fresh_unchanged | passed | pure_cascade | failed`.
- `parked_reason TEXT NULL` — one of `TIME_WAIT | CALLBACK_WAIT | RETRY_BACKOFF | OTHER`; NULL when state ≠ parked.
- `parked_reason_label TEXT NULL` — freeform; required when `parked_reason = OTHER`.
- `parked_resume_at TIMESTAMPTZ NULL`.

Indexes: `(parent_run_id)`, `(node_id, frame_id)`, `(claimed_by) WHERE claimed_by IS NOT NULL`.

### `rimsky_nodes`

Columns removed:

- `state`, `last_outcome`, `parked_reason`, `frame_id`, `parked_resume_at` (whichever existed; all state-bearing columns).

Stays declarative: `(instance_id, template_node_alias)`, template-spec reference, created_at.

**Frame-end predicate update.** The existing predicate "no `rimsky_nodes` rows in state `stale` or `running` for this instance" was rooted on `rimsky_nodes`. State now lives on `rimsky_node_runs`, so the predicate re-roots: "no `rimsky_node_runs` rows for this `frame_id` with `state` in (`stale`, `running`)." The cascade-frame resolution code (`foundation/cascade/` and `runtime/frame_end.go` or equivalent) updates accordingly.

### `rimsky_claim_handles`

New columns:

- `parent_claim_handle_id UUID NULL` — FK self. Sub-claim parent; NULL for root acquisitions.
- `lifetime TEXT NOT NULL` — `subgraph | durable`. Default `subgraph`.
- `held_durable BOOLEAN NOT NULL DEFAULT FALSE` — set TRUE for `lifetime: durable` claims after promotion. Such claim handles persist past holding-subgraph completion; only released by explicit operator deletion or instance termination.
- `version_id TEXT NULL` — opaque, returned by `ClaimProducer.Commit` for DataProcessing-capable producers. Recorded for lineage.
- `producer_candidate_handle BYTEA NULL` — opaque-to-rimsky candidate identifier returned by `DataProcessing.BeginCandidate`. Populated only on sub-claim rows (i.e., rows with `parent_claim_handle_id NOT NULL`) for DataProcessing-capable claims. Used by the supervisor when calling `CommitCandidate` / `AbandonCandidate` and surfaced in lineage's `claim_commit` records (`candidate_handle_ids` field). The bytes are inert in rimsky per the inertness discipline — stored, passed verbatim to the producer, never inspected.

`node_run_id` FK remains; semantics unchanged (`ON DELETE SET NULL` so held claims outlive their parent run's active terminal).

### `rimsky_claim_holders`

Schema change: `holder_node` → `holder_run_id` (FK → `rimsky_node_runs`). All other semantics preserved.

### `rimsky_wait_set`

Schema change: per-frame ledger now at run-level granularity:

- `sender_run_id UUID NOT NULL` (FK → `rimsky_node_runs`).
- `receiver_run_id UUID NOT NULL` (FK → `rimsky_node_runs`).
- `frame_id UUID NOT NULL`.

Eligibility predicate ("receiver eligible when no wait-set rows reference it") preserved; granularity is now run-level.

### `rimsky_messages` (new)

Append-only audit + delivery surface:

| column | type | notes |
|---|---|---|
| `id` | UUID | primary key |
| `instance_id` | UUID NOT NULL | which instance |
| `kind` | TEXT NOT NULL | V1: `invalidate` only |
| `sender` | TEXT NOT NULL | `operator`, sensor name (including `sensor-cron`), `instance:<id>` (V2) |
| `sender_kind` | TEXT NOT NULL | `operator | sensor | instance` |
| `target` | TEXT NULL | optional target node alias |
| `payload` | BYTEA | opaque to rimsky |
| `backfill_operation_id` | UUID NULL | indexed; links messages of a single backfill |
| `received_at` | TIMESTAMPTZ NOT NULL | |
| `delivered_at` | TIMESTAMPTZ NULL | NULL while pending |
| `frame_id` | UUID NULL | the frame this message's delivery triggered |

Indexes: `(instance_id, received_at)`, `(backfill_operation_id) WHERE backfill_operation_id IS NOT NULL`, `(instance_id, delivered_at) WHERE delivered_at IS NULL`.

### `rimsky_lineage` (new)

Append-only; rebuildable from `rimsky_events` + `rimsky_claim_handles` history:

| column | type | notes |
|---|---|---|
| `id` | UUID | primary key |
| `record_kind` | TEXT NOT NULL | `leaf_run | claim_commit` |
| `instance_id` | UUID NOT NULL | |
| `frame_id` | UUID NOT NULL | |
| `observed_at` | TIMESTAMPTZ NOT NULL | |
| `record` | JSONB NOT NULL | record-kind-specific shape, see below |

Indexes:

- `(record_kind, (record->>'run_id'))` for forward leaf-run lookups.
- `(record_kind, (record->>'claim_handle_id'))` for forward claim_commit lookups.
- GIN index on `record->'substitution_refs'` to enable reverse walks. Reverse queries match the `source_node_alias` and `source_version_or_id` fields of substitution-ref entries (the field names declared in the leaf-run record schema below). Equivalent reverse navigation for held claims is reachable via the `(record->>'claim_handle_id')` index on claim_commit records joined to leaf-run records' `held_claims` entries (also covered by the GIN index on `record`).

Leaf-run record shape (`record_kind = 'leaf_run'`):

```json
{
  "run_id": "...",
  "node_alias": "...",
  "child_key": "...",
  "parent_run_id": "...",
  "frame_trigger_kind": "operator_message | sensor_message | cascade_walk",
  "trigger_message_id": "...",
  "substitution_refs": [
    {"source_node_alias": "...", "source_kind": "attribute|event|message",
     "source_version_or_id": "...", "substitution_path": "..."}
  ],
  "held_claims": [
    {"claim_handle_id": "...", "role": "acquire|hold",
     "producer_name": "...", "scope_data_hash": "..."}
  ],
  "executor_name": "...",
  "executor_version": "...",
  "template_hash": "...",
  "template_node_alias": "...",
  "params_snapshot_hash": "...",
  "userdata_hash": "...",
  "changed": true,
  "last_outcome": "fresh_changed",
  "terminal_kind": "complete|error|parked"
}
```

Claim-commit record shape (`record_kind = 'claim_commit'`):

```json
{
  "claim_handle_id": "...",
  "version_id": "...",
  "producer_name": "...",
  "scope_data_hash": "...",
  "parent_run_id": "...",
  "frame_id": "...",
  "sub_claim_handle_ids": ["..."],
  "committed_at": "..."
}
```

`sub_claim_handle_ids` lists the rimsky-side identifiers of the sub-claim rows that contributed to this commit. Each sub-claim row's `producer_candidate_handle` (the opaque bytes the producer assigned during `BeginCandidate`) lives on the corresponding `rimsky_claim_handles` row, not in the lineage record itself — lineage references the sub-claim by its rimsky-side id and joins through `rimsky_claim_handles` to surface the candidate handle if needed for audit.

### `rimsky_node_runs.fan_out_key` and related

Already addressed by the run-tree extension above. The fan-out wave is just one shape of parent-children relationship in `rimsky_node_runs`.

### Retired table

- **`rimsky_schedules`** — retires entirely. Cron state moves to the bundled `sensor-cron`'s own state (or `rimsky_sensor_watches.resolved_config` for the per-instance watch identity; the next-fire-at clock lives sensor-side).

### Retention sweeps

Two new periodic sweeps in the watchdog:

- **Run-tree retention** — deletes runs from frames older than `retention.recent_frames_kept` (default `100` per instance), cascading via `parent_run_id` from root through descendants. Skips runs whose claim_handles are held-durable.
- **Lineage retention** — deletes lineage rows where corresponding artifacts (runs or claim_handles) have been swept, plus a trailing window (`retention.lineage_trailing` default `30d`).

Held-durable claim handles are released only by explicit operator action (`DELETE /instances/{id}/assets/{alias}`) or instance termination. At instance termination, rimsky walks all `held_durable: true` claim handles for that instance and calls `ClaimProducer.Release` on each (sequentially; failures are logged but don't block instance termination from completing — operator can re-issue release manually). The producer is responsible for GCing the underlying durable data per its own policy on `Release`.

## Protocol surfaces

### `ClaimProducer` (extensions)

Existing methods (unchanged): `Open`, `Commit`, `Abandon`, `Release`, `Capabilities`.

New optional methods:

- **`SplitScope(claim_handle_id, partition_request) → ([sub_scope_descriptor])`** — producer-defined; returns a list of sub-scope descriptors that rimsky uses to open child claim handles. Producer that doesn't implement does not support partitioning; templates with partitioning against such a producer are rejected at registration.
- **`ScopesConflict(scope_a, scope_b) → bool`** — producer determines overlap. Default for producers that don't implement: byte-equal scope comparison (today's behavior). Producers that need overlap detection (e.g., for sub-scope conflict) implement.

`Commit` response shape extended to optionally return `version_id` (TEXT) — the producer-assigned version identifier for DataProcessing-capable claims. Rimsky records in lineage.

`Capabilities` response extended to advertise:

- `supports_split_scope: bool`
- `supports_scopes_conflict: bool`

### `DataProcessing` (new, optional mix-in)

Advertised in `Capabilities` via `protocols: [claim_producer, data_processing]`. Methods:

- **`Capabilities() → {data_shapes, materializations, partition_kinds}`** — advertises what the store supports. Used by template registration to validate.
- **`BeginCandidate(claim_handle_id, sub_scope_descriptor, idempotency_key) → candidate_handle`** — opens a write context for one work unit. The supervisor calls this at sub-claim acquisition (in the same transaction that inserts the sub-claim's `rimsky_claim_handles` row), persists the returned opaque `candidate_handle` bytes to the sub-claim row's `producer_candidate_handle` column, and passes the candidate_handle to the leaf executor's `ExecuteRequest` so the executor knows which candidate to write against. The producer already knows the materialization mode from the claim's `data:` block (validated at registration); no need to pass it again at BeginCandidate time.
- **`CommitCandidate(candidate_handle) → candidate_metadata`** — finalizes one candidate write. The supervisor calls this at the corresponding leaf-run terminal (success path), reading the `producer_candidate_handle` from the sub-claim row.
- **`AbandonCandidate(candidate_handle)`** — discards a candidate. Called on failure paths (leaf-run failure, strict-cancel from a sibling failure, backfill abort). Supervisor reads the `producer_candidate_handle` from each affected sub-claim row.
- **`ListVersions(claim_handle_id) → [version_metadata]`** — for asset version history queries.
- **`ListPartitions(claim_handle_id, version_id) → [partition_descriptor]`** — for partition manifest queries.
- **`GetVersionSchema(claim_handle_id, version_id) → schema_metadata`** — for SDK adapters and presentation surfaces.

The supervisor's parent-run terminal flow for DataProcessing-capable claims:

1. Children complete; aggregation policy decides "promote" or "abandon."
2. `ClaimProducer.Commit(parent_claim_handle_id)` — producer internally aggregates the registered candidates per the aggregator declared in the claim's `data:` block; atomically promotes to a canonical version; returns version_id.
3. Rimsky records version_id in `rimsky_claim_handles` and `rimsky_lineage`.

For abandon paths: rimsky calls `AbandonCandidate` on each candidate, then `ClaimProducer.Abandon` on the parent claim handle.

### `Validation` (new, optional mix-in)

Cross-cutting protocol. Any service may advertise it via `protocols: [..., validation]`. One method:

**`Validate(request) → response`**

The method name is plain `Validate` (not `ValidateUserdata`) because the request carries more than userdata: claim bindings, attribute schemas, sensor config, etc. The request type is self-describing.

```
ValidateRequest {
  bytes node_userdata = 1;        // opaque
  string node_alias = 2;
  string role = 3;                // "executor" | "claim_producer" | "lifecycle_subscriber" | "sensor"
  oneof context {
    ExecutorContext executor = 4;
    ClaimProducerContext claim_producer = 5;
    LifecycleSubscriberContext lifecycle = 6;
    SensorContext sensor = 7;
  }
}

ClaimProducerContext {
  repeated ClaimBinding bindings = 1;
}

ClaimBinding {
  string alias = 1;
  bytes scope_data = 2;
  string write_semantics = 3;
  string intent = 4;          // "acquire" | "hold"
  bytes data = 5;             // the claim's `data:` block, opaque to rimsky
}

ExecutorContext {
  repeated Dependency dependencies = 1;
  repeated ClaimBinding claim_bindings = 2;
  repeated AttributeSchema attribute_schemas = 3;
}

SensorContext {
  string sensor_kind = 1;
  bytes sensor_config = 2;    // resolved per-instance config
}

LifecycleSubscriberContext {
  string template_hash = 1;
  repeated string subscribed_event_kinds = 2;
}

ValidateResponse {
  bool valid = 1;
  repeated ValidationError errors = 2;
  repeated ValidationWarning warnings = 3;
}
```

`Capabilities` of a Validation-supporting service advertises which roles it validates: `validation_supported_roles: [...]`.

Pipeline integration (ordered):

1. **`userdata_schema` static check first** (existing). For executors, rimsky validates the node's userdata against the JSON Schema advertised by the executor in its `Capabilities`. Pure rimsky-side check; no RPC. Cheap and fast; catches shape errors before any dynamic call goes out.
2. **`Validate` RPC second** (new). For each service the node references that advertises `Validation` for the relevant role, rimsky calls `Validate`. Only runs if the static-schema check passed for executor userdata.
3. Errors at either step reject the template registration; warnings surface to the operator (`rimsky-cli template register --warnings-as-errors` to escalate).
4. Failure mode for unreachable services at registration: `permissive_warn` default (registration succeeds with warning); operator-configurable to `strict` via `rimsky.yml`: `registration.unreachable_validator: strict | permissive_warn`.

Preserves `@blessed-invariant 11` (userdata inert in rimsky): rimsky forwards opaque bytes; receives a verdict; never inspects content.

### `Sensor` (new service protocol)

A new service kind. Lives alongside the three existing service kinds in `rimsky.yml`:

```yaml
sensors:
  - name: sensor-http
    endpoint: grpc://sensor-http.internal:9100
    protocols: [sensor, validation]
  - name: sensor-object-store
    endpoint: grpc://sensor-object-store.internal:9101
    protocols: [sensor]
```

Methods:

- **`Capabilities() → {supported_kinds, config_schemas}`** — advertises which `kind` of watching this sensor supports and the config-schema per kind.
- **`StartWatch(watch_id, instance_id, kind, config) → ack`** — begins a watch. The sensor binds this `watch_id` to its internal state for the watch.
- **`StopWatch(watch_id) → ack`** — stops the watch.
- **`ListWatches() → [watch_descriptor]`** — for rimsky-side resync after sensor restart. Rimsky compares against its expected state and re-issues `StartWatch` for any missing.

Observation delivery: sensor pushes to rimsky via the control-api `POST /sensors/{watch_id}/observations` endpoint with `{payload}`. Rimsky resolves `watch_id` to `(instance, target_node, message_kind)` from its sensor-state registry (populated at `StartWatch`), constructs a message envelope, and enqueues it in `rimsky_messages`.

Bundled sensors (V1): `sensor-http` (poll URL on schedule), `sensor-object-store` (S3/GCS/Azure prefix watcher), `sensor-webhook` (inbound webhook listener).

## Run-tree and aggregation

### Structure

`rimsky_node_runs` is a tree:

- A root run has `parent_run_id IS NULL` and `child_key IS NULL`. Represents the dispatch of one outer-graph node within a frame.
- A child run has `parent_run_id NOT NULL` and `child_key NOT NULL`. Represents a fan-out work unit or a sub-graph internal node's run.
- Trees may be arbitrarily deep: fan-out of fan-outs, sub-graphs containing fan-outs, fan-outs of sub-graphs.

Every node has at least one run per frame in which it's stale-marked. A non-fan-out, non-delegating node has exactly one run (no children). A fan-out node has one parent run + N children. A delegating node has one parent run + as many children as the sub-graph's non-entry internal nodes (including the exit node — see the sub-graphs section for why entry is absorbed into the calling node while exit remains a child with a writeback carry-rule).

### State machine

Existing five-state machine (`fresh | stale | running | failed | parked`) applies uniformly:

- **Leaf runs** (executor calls; `claimed_by` set at runtime; no children): state transitions per the existing executor-terminal-spine. `running → running` rejected; transition reasons audited.
- **Parent runs** (no `claimed_by` for orchestration-only parents; have children): state is *derived* from children's states via aggregation. Transitions happen reactively when a child transitions. State propagation walks from the transitioning child up to the root run, single transaction, ancestor row-locks taken in tree order (always upward).

Transitions allowed for parents that aren't allowed for leaves:

- `terminal → stale` (child re-fired by cascade).
- `terminal → running` (child re-dispatched).
- `running → running` (children churning beneath).

A new transition reason: `ReasonChildTransitioned`, carrying the transitioning child's id + new state for audit.

For sub-graph parents specifically: the parent run has an executor (the entry node's executor, absorbed at canonicalization). The parent's state-machine cycle is extended:

1. `running` while the entry's executor is running (entry IS the parent — same row, same dispatch).
2. Entry executor returns terminal payload. Supervisor processes:
   - Failure/parked → parent transitions to that terminal state immediately; internal cascade does not fire.
   - Success (`fresh_changed` / `fresh_unchanged`) → parent stays `running` with transition reason `ReasonSubGraphInternalCascadeFired`; supervisor fires internal cascade; non-entry children dispatch.
3. `running` while children are active (per standard run-tree aggregation).
4. Children all terminal → standard state-aggregation table evaluates; parent transitions to terminal. Exit's writeback was already carried up at exit's terminal (carry-rule).

### State aggregation rules

Single deterministic rule per scenario; only the error policy is user-configurable.

| Children state distribution | Parent state |
|---|---|
| All `fresh_changed` | `fresh_changed` |
| All `fresh_unchanged` | `fresh_unchanged` |
| Mix of `fresh_changed` and `fresh_unchanged` | `fresh_changed` (any-changed propagates) |
| Any `running` | `running` |
| Any `stale` and none `running` | `stale` (pending re-dispatch) |
| Any `parked` and none `running`/`stale` | `parked` |
| Any `failed` and none `running`/`stale`/`parked` | Per error policy |

Error policy (the user-configurable knob; declared in the template-node spec; snapshotted to `aggregation_policy` at run creation):

- **`strict`** (default) — any `failed` child → parent `failed`.
  - Sub-option: `strict.cancel_siblings: true` (default) — supervisor abandons in-flight siblings via `ClaimProducer.Abandon` on their claims; siblings transition to `failed` with `error_class: "sibling_failed"`. For siblings that have their own sub-claim chains (fan-out of fan-out, sub-graphs containing fan-out), cancellation walks recursively: each sibling's auto-terminal Abandon cascades through its descendants via the recursive claim-tree resolution mechanism. The recursion is bounded by the claim-tree depth, which equals the run-tree depth.
  - `strict.cancel_siblings: false` — siblings finish; parent is `failed` but siblings produce their own terminals (unaggregated into the parent's outcome).
- **`threshold(max_failures: N)`** — `running` until cumulative `failed` count > N, then `failed`; otherwise once all terminal, roll up successful children per the normal aggregation.
- **`best_effort`** — never fail parent for child failures alone. Once all children terminal, parent transitions to `fresh_changed` with failure details in writeback. If zero children succeeded, parent `failed`.
- **`first`** — race. First child to reach `fresh_changed`/`fresh_unchanged` wins; siblings cancelled. If all children fail, parent `failed`.

### Output aggregation

Producer-handled, implicit in `ClaimProducer.Commit` for DataProcessing-capable producers. The aggregator is declared in the claim's `data:` block; rimsky doesn't interpret it. At parent-run terminal (after children's candidates committed), `Commit` triggers the producer to coalesce candidates per the registered aggregator and return the canonical version_id.

For sub-graphs: the parent run's writeback IS the exit node's writeback (carry-rule). On exit-node terminal, the aggregation engine copies its writeback to the parent run's writeback row.

For fan-out nodes producing aggregated outputs: the parent's writeback comes from the producer's `Commit` response (carries version_id and producer-supplied metadata).

### State propagation transaction

When a child run terminates, the supervisor:

1. Locks the parent's row (`SELECT ... FOR UPDATE`).
2. Re-evaluates aggregation against all children's current states.
3. Computes the parent's new state.
4. If state changed, writes the parent's new state with a transition reason.
5. If the parent's new state is terminal, repeats steps 1-4 for the grandparent.
6. Continues up the tree until a non-terminal ancestor is reached or the root is updated.

Single transaction; ancestor locks taken in tree order (always upward) — avoids deadlock since the partial order is a tree.

## Fan-out template DSL

Fan-out is declared per-node in templates. The shape:

```yaml
nodes:
  - type: per-region-load
    executor: my-loader
    claims:
      data:
        producer: parquet-store
        scope: { dataset: parcels }
        lifetime: durable
        data: { ... }
    fan_out:
      claim: data                                  # the claim to partition
      partition_request: "{{...}}"                 # opaque to rimsky; passed verbatim to SplitScope
      parallelism: 8                               # optional; max concurrent leaf runs
      error_policy:
        kind: strict                               # or threshold | best_effort | first
        cancel_siblings: true                      # only meaningful for strict
        max_failures: 0                            # only meaningful for threshold
    userdata:
      partition_value: "{{child.partition_key}}"   # substitution into per-leaf userdata
```

Fields:

- **`claim`** — the alias of the claim (declared in this node's `claims:` or `holds:` block) that the producer will partition.
- **`partition_request`** — opaque bytes passed to `SplitScope`. The producer interprets this per its own surface (date ranges, region lists, dynamic queries, etc.). Typically a substitution path that resolves at dispatch time: `"{{trigger.message.payload.partition_request_override | default: <template-default>}}"` is the canonical pattern for nodes that can be triggered by both normal invocations and backfill messages.
- **`parallelism`** — optional; supervisor's dispatch concurrency cap for this fan-out wave. Default: unbounded (each leaf dispatches as soon as eligible).
- **`error_policy`** — selects which aggregation rule applies on child failures. The four shapes (`strict`, `threshold`, `best_effort`, `first`) per the state-aggregation section.

Substitution context inside the fan-out node's userdata: `{{child.partition_key}}` resolves to the per-leaf partition key for that work unit. The supervisor binds this at child dispatch.

**Mechanics at dispatch:**

1. Calling node's parent run is created.
2. Supervisor calls `ClaimProducer.SplitScope(parent_claim_handle, partition_request)` → list of sub-scope descriptors.
3. For each sub-scope, supervisor opens a sub-claim handle (in the parent-acquisition transaction):
   a. INSERT into `rimsky_claim_handles` with `parent_claim_handle_id` set.
   b. Call `ClaimProducer.Open` (for the sub-claim) and record the address.
   c. If the producer is `DataProcessing`-capable, call `DataProcessing.BeginCandidate` and persist `producer_candidate_handle`.
4. Supervisor dispatches one child leaf run per sub-claim, with `child_key = <partition_key>` and the sub-claim's address available via the leaf's `ExecuteRequest`.
5. Leaves run; supervisor calls `CommitCandidate` per leaf at success terminal, `AbandonCandidate` on failure or strict-cancel.
6. Parent's aggregation evaluates per the declared `error_policy`.
7. On promote: supervisor calls `ClaimProducer.Commit(parent_claim_handle)` — producer internally aggregates the candidates per the aggregator declared in the claim's `data:` block; returns `version_id`.

Validation at registration: the canonicalizer checks that the referenced `claim` is declared on the node; that the producer advertises `supports_split_scope: true` in `Capabilities`; that the producer's `Validation` (if advertised) doesn't reject the partition_request shape.

## Recursive scope partitioning

### Model

Partitioning is a node-level decision applied to a claim the node already holds. A node decides to fan-out by deciding to partition: "I'm going to split this scope into N sub-scopes." The producer is queried via `SplitScope`; rimsky opens one sub-claim per sub-scope; child runs are dispatched, each holding a sub-claim.

Sub-claims are themselves claims; they can be further partitioned by their holder, recursively. Resolution is recursive: a parent claim auto-terminals only after all its sub-claims have terminal.

Auto-terminal walks the claim-tree from leaves up: each sub-claim resolves on its own holding-subgraph completion; when all sub-claims of a parent are terminal, parent resolves; etc. The `ResolveClaimHandleTerminal` engine extends to recursive walks.

### Conflict detection

`@blessed-invariant 4b` rephrases: "single-writer-per-scope; overlap is producer-defined, byte-equal as the trivial default."

Producer-aware conflict via `ClaimProducer.ScopesConflict`. Acquisition transaction calls the method for each candidate-existing scope pair where the producer advertises support; producer returns yes/no. Producers that don't implement default to byte-equal comparison.

### Atomicity

`@blessed-invariant 10` rephrases for parent-claim acquisition: atomicity is between the rimsky-side bookkeeping (claim handle row + sub-claim handle rows + producer-`Open`-returned addresses) and the supervisor's `Open` call. Children's claims under a fan-out parent are opened in the supervisor's parent-run acquisition transaction. Producer-internal state mutations decoupled (in producer's own transaction).

### Persistence

`rimsky_claim_handles.parent_claim_handle_id` records the sub-claim parent. Auto-terminal queries: "find all sub-claim handles of this parent; check all non-active."

## Claim co-holdership (`holds:`)

A node-run can co-hold an existing claim acquired upstream, via the `holds:` template directive:

```yaml
nodes:
  - type: verify-zoning
    executor: verifier-shape-checks
    dependencies: [load-zoning]
    holds:
      zoning-data: { from: load-zoning }
    userdata: { ... }
```

Mechanics:

- At dispatch, the co-holder's `ExecuteRequest` carries the inherited claim's address (same `ClaimResult` the original acquirer received).
- Persistence: `rimsky_claim_holders` records the co-holder (now keyed by `holder_run_id`).
- Auto-terminal fires when all `rimsky_claim_holders` rows for a claim_handle are non-active. The holding subgraph extends to include the co-holder.
- Multiple co-holders are supported (`holds:` block can list many; multiple nodes can co-hold the same claim independently).

DAG constraint: a co-holdership `from:` pointer must reference an upstream dependency. The co-holdership graph is a subset of the cell graph; naturally acyclic.

Distinct from `claims:` (which acquires a new claim handle); `holds:` adds a row in `rimsky_claim_holders` against an existing handle.

## Lifetime and the asset pattern

### Claim lifetime

Per-claim property in the `claims:` block:

```yaml
claims:
  parcels:
    producer: parquet-store
    scope: { dataset: parcels }
    lifetime: durable        # or `subgraph` (default)
    write_semantics: staged_async
    data: { ... }
```

Semantics:

- **`subgraph`** (default) — auto-terminal fires `Commit` or `Abandon` at holding-subgraph completion; the claim handle is released and the row deleted.
- **`durable`** — auto-terminal still fires `Commit` (or `Abandon`); on success, the claim handle row persists with `held_durable: true`. The handle is available for future dispatches to co-hold (`holds:`) and for asset-presentation queries. Released only by explicit operator action (`DELETE /instances/{id}/assets/{alias}`) or instance termination.

### Asset

An asset is the compound: claim against a `DataProcessing`-capable producer + `lifetime: durable`. Anything satisfying both is an asset; anything else isn't (rimsky doesn't apply asset semantics to other claims). No new primitive — the asset presentation surface is a query alias over claims with these properties.

The asset-thinking surface exists in three places:

- **Control-api endpoints** (`/instances/{id}/assets/...`):
  - `GET /instances/{id}/assets` — list assets in this instance.
  - `GET /instances/{id}/assets/{alias}` — single asset's full state.
  - `GET /instances/{id}/assets/{alias}/versions` — version history (via `DataProcessing.ListVersions`).
  - `GET /instances/{id}/assets/{alias}/materialization-history` — when each version was promoted, by which run-tree.
  - `POST /instances/{id}/assets/{alias}/materialize` — alias for sending an invalidate-kind message targeting the asset's producer node.
  - `DELETE /instances/{id}/assets/{alias}` — explicit operator deletion of a `held_durable` asset. Calls `ClaimProducer.Release` on the claim handle; the producer GCs the underlying durable data per its own policy. Refuses if any in-flight run holds the claim.
- **CLI subcommands** — `rimsky-cli asset list/show/materialize/versions/delete`, plus `rimsky-cli asset lineage <id>:<alias> --version <v>` for the corresponding lineage walk.
- **Dashboard reframe** — asset-primary view alongside the rimsky-internals view.

All backed by joins over `rimsky_claim_handles` (filtered by `held_durable + DataProcessing producer`), `rimsky_lineage`, `rimsky_node_runs`, and DataProcessing protocol queries (`ListVersions`, `ListPartitions`, `GetVersionSchema`).

Per-instance namespacing for V1: `{instance_id}.{asset_alias}` is the asset's canonical identity.

### Substrate inertness

The asset's `data:` block in the template is producer-targeted and opaque to rimsky. Rimsky-aware fields outside `data:`: `producer`, `scope`, `lifetime`, `write_semantics`. The producer parses `data:` via `Validation` at registration; consults it at runtime per its own state.

## Messages

### Envelope

A message is a pushed envelope with these fields:

| Field | Required | Notes |
|---|---|---|
| `id` | yes | UUID; rimsky-assigned |
| `instance_id` | yes | target instance |
| `kind` | yes | V1: `invalidate` only |
| `sender` | yes | identity of the sender (`operator`; sensor name, including `sensor-cron`; future `instance:<id>`) |
| `sender_kind` | yes | `operator | sensor | instance` |
| `target` | optional | node alias in the receiving instance |
| `payload` | optional | opaque bytes; inert per discipline |
| `received_at` | yes | rimsky-assigned timestamp |

### Delivery

Messages persist in `rimsky_messages` on receipt. At each frame boundary, undelivered messages for the next frame are bundled per the per-instance `frame_delivery_mode` (`serial_queue | coalesce`; default `coalesce`):

- `serial_queue`: one frame per message (or per batch of messages per the queue rate).
- `coalesce` (default): pending messages join into the next frame; multiple targets are stale-marked simultaneously.

The mode is configured per-instance at creation time (`POST /instances {template, params, frame_delivery_mode: "coalesce"}`); default is `coalesce` for new instances.

For each undelivered message:

- Rimsky walks subscriptions in the target instance matching the envelope fields (kind, sender, sender_kind, target).
- Matched subscribers' nodes are stale-marked within the new frame.
- The message's `delivered_at` and `frame_id` are populated.
- Multiple matching subscribers all fire in the same frame.

If no matching subscriber: message is dead-lettered (audited in `rimsky_messages` with `delivered_at` set but no firings recorded). Visible via `rimsky-cli messages tail`.

### Subscriptions on messages

`subscribes:` topic kind `message`:

```yaml
subscribes:
  - { on: message, target: self }                                # any message targeting me
  - { on: message, sender: url-watcher }                         # any message from this sensor
  - { on: message, kind: invalidate, sender_kind: operator }     # any operator invalidate (broadcast)
```

Filter fields: `kind`, `sender`, `sender_kind`, `target`. Receivers can combine. `target: self` is the common pattern.

Substitution context for dispatched executor: `{{trigger.message.payload.X}}` reads payload fields.

### Emit sites for `invalidate` (the V1 kind)

| Source | Crosses boundary | Goes through message queue |
|---|---|---|
| Operator API (`POST /instances/{id}/messages`) | yes | yes |
| Sensor (`POST /sensors/{watch_id}/observations`) | yes | yes |
| Cascade walk (subscription-edge match within a frame) | no | no — in-frame, direct |

In-frame firings are not messages. Only boundary-crossing arrivals are.

Note: cron-style scheduled fires are not a separate emit site — they're sensor observations from the bundled `sensor-cron` (see Sensors section). Lifecycle-handler resolutions cause state-machine transitions that fire `on: state` subscriptions via the cascade-walk row above; they don't have their own direct emit slot post `tension:_resolved/send-vs-subscribe-asymmetry`.

### Control-api endpoints

- `POST /instances/{id}/messages` — operator sends a message. Body: `{kind, target?, payload?}`. Sender derived from caller identity.
- `GET /instances/{id}/messages` — list recent messages, filterable by `kind`, `sender_kind`, `target`, `delivered_at`.
- `GET /messages/{id}` — single message detail.
- `POST /sensors/{watch_id}/observations` — sensor pushes an observation. Sender = sensor name (resolved server-side from `watch_id`).

## Sub-graphs

### Template DSL

Top-level `graphs:` block (replaces `nodes:`):

```yaml
graphs:
  - name: main                    # reserved name = top-level graph at instance level
    nodes:
      - type: load-raw
        executor: http-node
        claims: { raw: { producer: filesystem-store, ... } }
      - type: process-parcels
        delegate: parcels-pipeline
        subscribes: [{ node: load-raw, on: state, when: terminal }]
        holds:
          raw: { from: load-raw }
        claims:
          output: { producer: parquet-store, lifetime: durable, data: {...} }
        userdata: { mode: standard }

  - name: parcels-pipeline
    entry: validate
    exit: finalize
    nodes:
      - type: validate
        executor: validator
        # The entry node. At canonicalization, this node is ABSORBED into the calling node:
        # the calling node's `rimsky_nodes` row gets `executor: validator` plus any
        # sub-graph-internal claims/holds/userdata declared here, merged with the
        # calling node's external declarations.
      - type: clean
        executor: cleaner
        subscribes: [{ node: validate, on: state, when: terminal }]   # 'validate' resolves to the calling node per-invocation
        holds: { raw: { from: validate } }
      - type: enrich
        executor: enricher
        subscribes: [{ node: clean, on: state, when: terminal }]
        holds: { raw: { from: clean } }
      - type: finalize
        executor: finalize-writer
        subscribes: [{ node: enrich, on: state, when: terminal }]
        # The exit node. Has its own `rimsky_nodes` row; is a child of the calling node's
        # parent run; its writeback flows to the parent's writeback at terminal (carry-rule).
```

Conventions:

- **Reserved name `main`** = the top-level graph. The instance state-machine is bound to `main`.
- **Sub-graphs** = graphs with both `entry` and `exit` declared.
- **Delegation** = `delegate: <graph-name>` on a node; references a sub-graph (must have entry+exit).
- **Mutual exclusivity** = a node has either `executor:` or `delegate:`, not both.

### Identity and absorption

The asymmetry is real and load-bearing for the model:

- **The entry node is absorbed into the calling node.** At canonicalization, the calling node's `rimsky_nodes` row inherits the entry node's executor and the entry node's sub-graph-internal declarations (claims/holds/userdata that the entry's template specified, merged with what the calling node declared externally). The entry node does NOT get its own `rimsky_nodes` row in the canonical instance — it IS the calling node. Subscription edges from internal nodes that reference the entry alias (e.g., `subscribes: [{ node: validate, ... }]`) resolve at canonicalization to the calling node's alias.
- **The exit node is NOT absorbed.** It has its own `rimsky_nodes` row (shared declaratively across invocations of this sub-graph in this instance). What's special about the exit is the carry-rule: at exit's leaf-run terminal, the supervisor copies exit's writeback to the calling node's parent-run writeback in the same transaction that records exit's terminal.

So "entry becomes the calling node" is structural (same row, same executor, same run); "exit becomes the calling node" is conceptual (the exit's terminal contributes the calling node's writeback via carry-rule; exit remains its own node mechanically).

### Invocation semantics

At dispatch:

1. Outer cascade fires the calling node per its `subscribes:`, dependencies.
2. Calling node dispatches → parent run created. The parent run has an executor — the entry node's executor (absorbed at canonicalization). Userdata, claims, holds for the parent come from the calling node's external declarations merged with the entry node's sub-graph-internal declarations.
3. Parent run's executor runs (this IS entry's executor; this IS the calling node's logical "step 1").
4. Executor returns a terminal payload. Supervisor processes:
   - **Failure / parked terminal**: parent transitions to `failed` / `parked` per the standard executor-terminal-spine. Internal cascade does NOT fire; non-entry internal nodes are not dispatched. If the entry's executor itself acquired sub-claims (via its absorbed-into-parent claims/holds declarations and SplitScope at parent-acquisition time), those sub-claims `Abandon` per the standard auto-terminal path.
   - **Success terminal** (`fresh_changed` / `fresh_unchanged`): parent stays in `running` state. Supervisor fires the sub-graph's internal cascade — non-entry internal nodes that subscribe to the entry alias are stale-marked within this frame. Subscription edges resolved at canonicalization map entry-alias references to the calling node's alias.
5. Internal child runs (one per non-entry internal node that is stale-marked in this frame) dispatch as children of the parent. Their `parent_run_id` is the parent's run id; their `child_key` is the internal node's alias.
6. Internal nodes execute per their own templates; subscribe to each other; eventually the exit node becomes eligible.
7. Exit node runs as a child (just like any other internal child). At its terminal, the carry-rule fires: exit's writeback is copied to the parent's writeback row in the same transaction.
8. All children terminate (or strict-cancel siblings on failure). Parent's aggregation evaluates per the standard rule table + error policy; parent transitions to terminal.
9. Outer cascade continues from the calling node downstream.

The parent run's state-machine path: `running` (entry executor) → `running` (children active) → `terminal`. The `running → running` self-transition during step 4→5 carries a new transition reason: `ReasonSubGraphInternalCascadeFired`.

### Aggregation for sub-graphs

Standard run-tree state-aggregation table from the state-machine section applies unchanged. The error policy on the calling node controls failure propagation (default `strict.cancel_siblings: true`).

Two additions specific to sub-graphs:

- **Entry-failure short-circuit**: if the entry executor (which IS the parent's executor) returns a failure/parked terminal, the parent transitions to that terminal directly. Internal children are not dispatched; the aggregation table doesn't apply because there are no children at terminal time.
- **Writeback carry-rule for exit**: at exit's leaf-run terminal (state transition recording its outcome and writeback bytes), the supervisor copies exit's writeback to the parent run's writeback row in the same transaction. The parent's writeback is whatever the exit produced. If exit never runs (e.g., an internal node failed and strict-cancel killed exit before it dispatched), the parent's writeback stays empty.

### Multiple invocations

Two callers delegating to the same sub-graph:

- Each caller has its own `rimsky_nodes` row in the outer graph, with the entry's executor absorbed into that row.
- Non-entry sub-graph internal nodes (the rest, including exit) have one `rimsky_nodes` row each per instance (shared across invocations — declarative).
- Each invocation produces a distinct run-tree, rooted at the caller's parent run; internal child runs distinguished by `parent_run_id` chain.
- Subscription edges from internal nodes to the entry alias are resolved per-invocation by the cascade walker at runtime: an internal child run's wait-set entry points to the specific parent run for this invocation (not the abstract "entry node").

### Encapsulation

Internal nodes (including exit) can only reference:

- Other internal nodes within the same sub-graph (via `subscribes:`, `dependencies:`, `holds:`).
- The entry alias — references resolve to the calling node per-invocation.

They cannot directly reference outer-graph nodes or other sub-graphs' internals. Such references are rejected at template registration.

Cascade walker behavior:

- From outside, cascade fires the calling node; doesn't descend into the sub-graph from outside (the sub-graph is opaque externally; outer subscriptions match against the calling node's state/events/attributes, which are populated from the parent run's lifecycle including the carried-up exit writeback).
- Within the sub-graph, cascade walks normally between internal nodes per their subscriptions, with entry-alias references resolved to the calling node.

### Edge-case rejections at registration

- **Recursive sub-graphs** — a sub-graph delegating to itself directly or via a cycle. Rejected with `subgraph_recursion_unsupported`.
- **Sub-graphs without entry or exit** — rejected.
- **Sub-graphs with disconnected internal nodes** — rejected if any internal node is unreachable from entry or doesn't feed exit.
- **`main` graph having entry/exit** — rejected; entry/exit have no meaning at instance level.
- **Internal nodes referencing outer-graph nodes** — rejected.
- **Entry node also marked as exit** (entry == exit) — rejected for V1; would collapse the sub-graph to a single absorbed node, which is just a regular node with no sub-graph benefit.

## Sensors as a service kind

### Service kind shape

Sensors are deployed externally as standalone services (separate processes), advertised in `rimsky.yml`. Same deployment model as ClaimProducer or Executor.

### Per-instance parameterization

Templates declare sensors:

```yaml
graphs:
  - name: main
    nodes: [...]

sensors:
  - name: url-watcher
    kind: sensor-http
    config:
      url: "{{params.target_url}}"
      poll_interval: 5m
    on_observation:
      target_node: process-update
      message_kind: invalidate
      payload_template: { content_hash: "{{observation.content_hash}}" }
```

At instance creation:

1. Rimsky resolves the sensor's config via substitution from instance params.
2. Rimsky calls `Sensor.StartWatch(watch_id, instance_id, kind, resolved_config)` on the configured sensor service.
3. The sensor binds the watch internally and begins monitoring.
4. The watch persists in `rimsky_sensor_watches` (new table; see below).

At instance termination: rimsky calls `Sensor.StopWatch(watch_id)` for each registered watch.

### `rimsky_sensor_watches` (new)

| column | type | notes |
|---|---|---|
| `id` | UUID | watch_id |
| `instance_id` | UUID NOT NULL | |
| `sensor_name` | TEXT NOT NULL | references `rimsky.yml` |
| `kind` | TEXT NOT NULL | sensor-advertised kind |
| `resolved_config` | JSONB NOT NULL | post-substitution config |
| `on_observation` | JSONB NOT NULL | observation → message mapping |
| `started_at` | TIMESTAMPTZ | |
| `last_observed_at` | TIMESTAMPTZ NULL | |
| `state` | TEXT NOT NULL | `active | failed | stopped` |

### Observation flow

1. Sensor service detects an arrival (S3 prefix change, webhook callback, HTTP poll change).
2. Sensor calls `POST /sensors/{watch_id}/observations` with body `{observation_payload}`.
3. Rimsky:
   - Resolves `watch_id` → `(instance_id, target_node, message_kind, payload_template)`.
   - Substitutes the `payload_template` against the observation.
   - Constructs a message envelope with `sender = sensor_name`, `sender_kind = sensor`, `target = target_node`, `kind = message_kind`.
   - Enqueues in `rimsky_messages` for next-frame delivery.

### Resync after sensor restart

If a sensor service restarts and loses internal state: at startup, rimsky calls `Sensor.ListWatches()` to enumerate current watches; for each expected watch missing from the sensor's view, rimsky re-issues `StartWatch`.

### Bundled sensors

- **`sensor-cron`** — fires observations on cron expressions. Replaces the previous per-node `schedule:` template field and the internal scheduler-tick cron path; cron becomes a regular sensor. Watch config: `{cron: "<expr>", missed_fires: "drop"}` (the "missed fires NOT backfilled" semantic preserved). Implementation maintains its own next-fire-at state; uses Postgres advisory lock if deployed multi-replica (operator's responsibility — default is single-replica). On fire, pushes an observation; rimsky converts to an invalidate-kind message targeting the configured node.
- **`sensor-http`** — polls an HTTP endpoint on a schedule; matches against declared conditions (status code, JSONPath on body). Watermark/cursor in observation payload.
- **`sensor-object-store`** — watches S3/GCS/Azure prefixes for new objects. High-watermark cursor; emits per-object observations.
- **`sensor-webhook`** — listens on a configured port for inbound webhooks. Routes each webhook to a `POST /sensors/{watch_id}/observations` call. Idempotency-key header support.

Bundled in `sensors/<kind>/` (new directory parallel to `executors/`, `stores/`).

Not bundled (deferred):

- `sensor-sql` — substrate/connection/query surface complex.
- `sensor-kafka` — heavy dependency.

### Retirement of per-node `schedule:` template field

The existing `schedule:` field on nodes retires. Templates that want cron-style firing declare a sensor of kind `cron` in the `sensors:` block targeting the node:

```yaml
graphs:
  - name: main
    nodes:
      - type: refresh-parcels
        executor: ...
        subscribes:
          - { on: message, target: self }   # fire when sensor-cron pushes
        # NO `schedule:` field

sensors:
  - name: nightly-refresh
    kind: cron
    config:
      cron: "0 2 * * *"
      missed_fires: drop
    on_observation:
      target_node: refresh-parcels
      message_kind: invalidate
```

`rimsky_schedules` table retires. The `rimsky-scheduler` process keeps its existing role as the periodic-sweep orchestrator (orphan reapers, parked-node timeout, frame-progress watchdog, etc.) but loses the cron-fire path; that code moves to the bundled `sensor-cron`.

Concept catalog: `concept:schedule` retires; cron is a sensor-kind, documented under `concept:sensor` + `concept:message`.

## Content lineage

### Records

Two kinds, both append-only:

- **Leaf-run record** (`record_kind: leaf_run`) — one per leaf-run terminal. Schema in the persistence section.
- **Claim-commit record** (`record_kind: claim_commit`) — one per `Commit` of a claim handle. Schema in the persistence section.

Source of truth is `rimsky_events` (the audit log) + `rimsky_claim_handles` lifecycle. `rimsky_lineage` is a materialized projection rebuildable from those.

### Query surface (control-api)

- `GET /lineage/runs/{run_id}` — single leaf-run record.
- `GET /lineage/runs/{run_id}/ancestors?depth=N` — recursive backward walk (substitution refs + held-claim writers).
- `GET /lineage/runs/{run_id}/descendants?depth=N` — recursive forward walk (downstream readers).
- `GET /lineage/claims/{claim_handle_id}` — single claim-commit record.
- `GET /lineage/claims/{claim_handle_id}/ancestors?depth=N` — backward through sub-claim manifest and the runs that wrote each sub-claim.
- `GET /lineage/by-source/{source_type}/{source_id}` — reverse lookup.
- `GET /lineage/by-producer/{executor_name}?version=...` — by-producer.

Walks bounded by `depth` parameter (max 50).

### OpenLineage emitter

Bundled lifecycle-subscriber at `subscribers/openlineage/`. New directory parallel to `executors/`, `stores/`, `sensors/`.

Maps rimsky records to OpenLineage:

- `instance_id + child_key` → `run.runId`.
- `template_node_alias` → `job.name`.
- Inputs/outputs (held claims + substitution refs) → `inputs[]/outputs[]` with namespace+name derived from `(producer_name, scope_data_hash)`.
- Producer metadata → `producer` URI.
- Frame triggers (messages, including cron-originated messages from `sensor-cron`) → custom facets (`triggerKind`, `triggerSource`, `triggerSenderKind`).
- Rimsky-specific custom facets carry: held-claim handle IDs, params snapshot hash, userdata hash, run-tree position.

Configured via `rimsky.yml` `lifecycle_subscribers:` block; runs against any OpenLineage-compatible backend (Marquez, DataHub).

### Retention

Operator-configurable. Default: retain as long as the corresponding artifact (run or claim handle) is retained, plus a trailing window (`retention.lineage_trailing: 30d`). Manual prune via `rimsky-cli lineage prune`.

## Atomic-staging worked example

Doc + reference impl illustrating producer-side stage-then-swap-on-Commit semantics. Composes naturally with subgraph-lifetime claims + co-holding verifier nodes + aggregation:

- Subgraph-lifetime claim's auto-terminal triggers Commit (atomic swap) on all-success, Abandon (drop staging) on any-failure.
- Verifier nodes co-hold the staging claim via `holds:`; their terminals contribute to the parent's aggregation.

Substrate caveats (documented per substrate):

- Postgres schema swap — atomic via transaction.
- Iceberg branch fast-forward — atomic via metadata pointer.
- POSIX filesystem `rename` — atomic within a filesystem.
- S3 copy+delete — windowed; not strictly atomic.
- Manifest pointer flip — atomic if the manifest write is.
- Kafka — incoherent for the pattern.

Reference impl: `examples/atomic-staging-fs-producer/` — Go binary implementing `ClaimProducer` (the four verbs + Capabilities) using POSIX directory rename. Demonstrates the pattern end-to-end. Sample template uses two verifier executors co-holding the staging claim.

Doc lands at `docs/agents/examples/atomic-staging.md`.

## Parked-state taxonomy

### Proto change

`Snooze` terminal extended:

```protobuf
message Snooze {
  optional google.protobuf.Timestamp resume_at = 1;
  optional bytes payload = 2;
  optional string session_token = 3;
  optional ParkReason reason = 4;
  optional string reason_label = 5;
}

enum ParkReason {
  PARK_REASON_UNSPECIFIED = 0;
  PARK_REASON_TIME_WAIT = 1;
  PARK_REASON_CALLBACK_WAIT = 2;
  PARK_REASON_RETRY_BACKOFF = 3;
  PARK_REASON_OTHER = 99;
}
```

### Persistence

`rimsky_node_runs` carries `parked_reason TEXT` and `parked_reason_label TEXT` columns (already enumerated in the persistence section).

### Per-reason `max_park_duration` config

```yaml
max_park_duration:
  time_wait: 1h
  callback_wait: 7d
  retry_backoff: 1h
  other: 1h
```

Watchdog's existing `SweepParkedNodes` consults the per-reason cap; timeout produces `failed{error_class: "park_timeout"}`.

### Control-api filter

`GET /diagnostics/parked?reason=callback_wait` filters by reason. `rimsky-cli parked list --reason=callback_wait`.

### Bundled emitter updates

- Long-running-job executors emit `CALLBACK_WAIT`.
- Time-based polling executors emit `TIME_WAIT`.
- Rate-limit-aware executors emit `RETRY_BACKOFF`.

## Backfills

### Pattern

A backfill is one invalidate-kind message with a `partition_request_override` payload field, targeting a fan-out node. The target's template uses substitution on its `fan_out.partition_request` field (see the "Fan-out template DSL" section) to accept the backfill's override:

```yaml
fan_out:
  claim: parcels
  partition_request: "{{trigger.message.payload.partition_request_override | default: <default-from-template>}}"
```

The default-clause is what runs for non-backfill invocations; the substitution-override is what runs when a backfill message provides one.

### Control-api

- `POST /instances/{id}/backfills` — body `{target_node, partition_request_override, reason}`. Rimsky validates that the target node has `fan_out.partition_request` referencing trigger substitution (warning if not). Constructs a single invalidate-kind message with payload `{partition_request_override, backfill_operation_id, reason}` and enqueues.
- `GET /instances/{id}/backfills` — list recent backfills (queries `rimsky_messages` filtered by `backfill_operation_id NOT NULL`).
- `GET /backfills/{op_id}` — single backfill: message + frame + parent run + children states (aggregated).
- `GET /backfills/{op_id}/partitions` — drill-down: per-child run state (one row per partition processed).
- `POST /backfills/{op_id}/cancel` — marks the operation cancelled. Pending undelivered messages skipped; in-flight frames complete normally (no preemption in V1).

### CLI

```sh
rimsky-cli backfill create --instance X --node parcels --range 2024-01-01..2024-09-30 --reason "..."
rimsky-cli backfill list --instance X
rimsky-cli backfill show <op-id>
rimsky-cli backfill cancel <op-id>
```

### Lineage integration

Lineage records' `trigger_message_id` chains back to the backfill's originating message; `rimsky_messages.backfill_operation_id` chains to the operation. Forward navigation via lineage queries.

## Verifier pattern documentation

No template-language sugar; no `concept:verifier` as a distinct concept. The verifier pattern is documentation:

- An executor that co-holds upstream claims (`holds:`).
- Reads via SDK adapters (or substrate-direct for non-DataProcessing).
- Runs checks.
- Returns terminal: `Complete{changed: false}` on pass; `Error{error_class: "verifier_failed"}` on fail; warnings via `Complete{changed: false}` with payload.
- Routes through `on_executor_errored` via the standard error-policy chain.

Composition with held subgraphs (the staging-then-swap pattern): verifiers' co-holdership extends the holding subgraph; their terminals contribute to the parent's aggregation; aggregation decides `Commit` vs `Abandon` on the staging claim.

### Bundled verifier executors

- **`verifier-shape-checks`** — Apache-licensed Go executor. Covers `no_nulls`, `nullable_fields_present`, `pk_unique`, `row_count_ratio`, `row_count_absolute`, `value_in_set`, `regex_match`, `numeric_range`.
- **`verifier-http`** — Go executor delegating to consumer-side HTTP endpoint. Body-template, expected status, timeout configurable in userdata.

Each at `executors/<name>/`, standalone Go binary.

### Deprecation of `graph/qualityrule/`

Pre-v1, clean break:

- `graph/qualityrule/eval/` builtins move to `executors/verifier-shape-checks/`.
- `graph/qualityrule/spec.go` removed; `Spec`/`Failure`/`EvalInput` removed from `foundation/spec`.
- `template_node.QualityRules` field removed.
- `eval.Register(name, ev)` removed.
- AGPL constraint on `graph/qualityrule/` dissolves with the package.
- `quality_rule_failed` event rolls into `executor_errored` with `error_class: "verifier_failed"`.

## Bundled deliverables (summary)

### Stores (in `stores/`)

- **`parquet-store`** — Parquet on S3 or local-fs. Implements `ClaimProducer` + `DataProcessing` + `Validation`. Backings: `s3-parquet`, `local-fs-parquet`.
- **`geo-parquet-store`** — GeoParquet variant. Implements `ClaimProducer` + `DataProcessing` + `Validation`. Substrate: S3 or local-fs.
- **`geo-postgis-store`** — PostGIS variant. Transactional; native spatial queries. Implements `ClaimProducer` + `DataProcessing` + `Validation`.

Existing bundled stores (`filesystem`, `postgres`, `stub`) stay as-is; do not advertise `DataProcessing`.

### Executors (in `executors/`)

- **`verifier-shape-checks`** — bundled verifier executor.
- **`verifier-http`** — HTTP-delegating verifier.

Existing bundled executors (`http-node`, `stub`, `claude-agent`) stay; no changes to their surface.

### Sensors (in `sensors/`, new directory)

- **`sensor-cron`** — cron-based scheduling. Replaces the retired per-node `schedule:` field.
- **`sensor-http`** — HTTP polling.
- **`sensor-object-store`** — S3/GCS/Azure prefix watching.
- **`sensor-webhook`** — inbound webhook listening.

Each implements `Sensor` protocol; may advertise `Validation`.

### Lifecycle-subscribers (in `subscribers/`, new directory)

- **`openlineage`** — emits OpenLineage events as nodes terminate.

### Examples (in `examples/`)

- **`atomic-staging-fs-producer/`** — reference impl of atomic-staging pattern on POSIX filesystem.

## Module layout summary

```
protocols/proto/v1/
  claim_producer.proto       (extended: SplitScope, ScopesConflict, Commit response version_id)
  data_processing.proto      (new)
  validation.proto           (new)
  sensor.proto               (new)
  executor.proto             (Snooze.reason extension)
  lifecycle.proto            (unchanged)

foundation/
  cascade/                   (state machine extension for parent runs; new transition reason)
  locks/                     (claim handle extensions: parent_claim_handle_id, lifetime, held_durable)
  spec/                      (template spec types: graphs:, sensors:, subgraphs, holds:, lifetime, data:)
  persistence/               (schema migrations; new tables; query support)
  shared/                    (Clock, Logger, UUID, DeepMergeJSON — unchanged)

graph/
  template/canonical/        (sub-graph parsing, validation, canonicalization; `schedule:` field removed; `sensors:` block added)
  attribute/                 (substitution — extended for {{trigger.message.payload.X}}, {{child.partition_key}})
  frame/                     (frame creation from message delivery)
  scheduler/                 (retains periodic-sweep orchestration; cron-fire path removed — moved to bundled sensor-cron)
  node/                      (run-tree validation algorithms)

runtime/
  runner_acquire.go          (extended: parent-claim acquisition, sub-claim atomicity)
  conductor.go               (extended: message delivery at frame boundary)
  auto_terminal.go           (recursive claim-tree resolution)
  terminal_decision.go       (unified terminal-decision engine)
  message_delivery.go        (new: dequeue messages, walk subscriptions, create frames)
  state_propagation.go       (new: run-tree state propagation transaction)
  validation_pipeline.go     (new: Validation protocol calls at template registration)

control/
  controlapi/
    instances.go             (POST /messages, POST /backfills, /assets, /lineage endpoints)
    sensors.go               (new: POST /sensors/{watch_id}/observations)
    diagnostics.go           (parked filter)
  cli/
    asset.go                 (new subcommand group)
    backfill.go              (new)
    messages.go              (new)
    lineage.go               (new)
  observability/             (dashboard reframe; asset-primary panel)

cmd/                         (existing reference binaries unchanged)

stores/
  parquet-store/             (new)
  geo-parquet-store/         (new)
  geo-postgis-store/         (new)
  filesystem/                (unchanged)
  postgres/                  (unchanged)
  stub/                      (unchanged)

executors/
  verifier-shape-checks/     (new)
  verifier-http/             (new)
  http-node/                 (unchanged)
  stub/                      (unchanged)
  claude-agent/              (unchanged for V1; refactor onto SDK is RDK-spec work)

sensors/                     (new directory)
  sensor-cron/                (replaces internal scheduler-tick cron path)
  sensor-http/
  sensor-object-store/
  sensor-webhook/

subscribers/                 (new directory)
  openlineage/

examples/                    (new directory)
  atomic-staging-fs-producer/

dashboards/                  (asset-primary view added)
```

## Concept catalog impacts

**Application timing.** The design-log entries below are NOT written when this spec is approved. They're applied during `execute-plan` in lockstep with the code that realizes each piece. This keeps the `.ok-planner/design/concepts/` catalog as a live record of what's actually in the codebase, rather than a forward-looking description of planned work. The spec is the durable design artifact; the catalog tracks code reality. Execute-plan derives full entry content from the spec body (which has detailed coverage of each concept) and writes/updates/retires the files alongside the corresponding implementation steps.

New concept entries:

- **`concept:graph`** — unit of node connectivity; uniform top-level declaration; `main` is the reserved top-level graph.
- **`concept:sub-graph`** — graph with entry/exit; invocable from another node via `delegate:`.
- **`concept:delegation`** — calling node ↔ sub-graph relationship; calling node absorbs the entry node (same row, same executor) and receives the exit node's writeback via carry-rule.
- **`concept:fan-out`** — node-level decision to partition a held claim into sub-claims.
- **`concept:asset`** — documented compound: claim against DataProcessing-capable producer + `lifetime: durable`.
- **`concept:claim-lifetime`** — `subgraph | durable`; governs auto-terminal behavior.
- **`concept:claim-co-holdership`** — `holds:` template directive; row in `rimsky_claim_holders`; extends holding subgraph.
- **`concept:data-processing`** — optional mix-in protocol on ClaimProducer; control-plane for typed-data versioning.
- **`concept:validation`** — cross-cutting protocol for registration-time userdata validation.
- **`concept:sensor`** — first-class in-instance service; declared in `sensors:` block; pushes messages.
- **`concept:message`** — boundary-crossing dispatch unit; pushed envelope matched via subscription.
- **`concept:lineage`** — projection of computational + data-promotion records.
- **`concept:lineage-record`** — `leaf_run` and `claim_commit` record kinds.
- **`concept:atomic-staging`** — producer-side stage-then-swap pattern.
- **`concept:backfill`** — invalidate message with partition-selector-override payload.

Updated concept entries:

- **`concept:attribute`** — clarifying note about its relationship to assets (assets are claims, not attributes).
- **`concept:claim`** — gains lifetime, may have parent (sub-claim), may have co-holders.
- **`concept:claim-handle`** — gains `parent_claim_handle_id`, `lifetime`, `held_durable`, `version_id`.
- **`concept:claim-holders`** — `holder_run_id` instead of `holder_node`.
- **`concept:claim-producer`** — gains three optional methods (SplitScope, ScopesConflict, Validation as mix-in).
- **`concept:cascade`** — clarifying note about sub-graph encapsulation.
- **`concept:node-run`** — expanded to run-tree structure; carries all state-bearing columns (lifted from `rimsky_nodes`).
- **`concept:frame`** — gains message-delivery as a frame-creation site.
- **`concept:parked-state`** — gains 4-reason taxonomy + freeform label.
- **`concept:invalidate`** — one `kind` of message (the V1 kind).
- **`concept:subscription`** — gains `message` topic kind with filter fields.
- **`concept:service`** — gains `sensor` service kind.
- **`concept:event`** (or `concept:named-event` if that's the existing slug) — clarifying note that events are internal-to-rimsky and frame-synchronous; distinct from `message` (external, frame-bounded). The retired `on_event:` map path is fully retired per `tension:_resolved/send-vs-subscribe-asymmetry`; consumption is via `subscribes: [{on: event, ...}]` only.
- **`concept:event-log`** — clarifying note that `rimsky_events` remains the audit log for events; messages have their own audit table `rimsky_messages`.

Retired concept entries:

- **`concept:node-state`** — state lives on runs; semantics described under `concept:node-run`.
- **`concept:quality-rule`** — replaced by verifier-executor pattern (no rimsky concept; just documentation).
- **`concept:schedule`** — replaced by bundled `sensor-cron`. Cron is a sensor kind, not a rimsky-core concept.

## Blessed-invariant updates

- **`@blessed-invariant 4b`** rephrases: "single-writer-per-scope; overlap is producer-defined, byte-equal as the trivial default."
- **`@blessed-invariant 10`** rephrases: "Lock acquisition is atomic with parent-run claim acquisition. The acquisition transaction either claims the parent run AND inserts the parent claim_handle row AND inserts all sub-claim handle rows for opted-into partitioning AND records the `Open`-returned addresses, or none of these."
- **New invariant: held-durable claim handles persist across instance dispatches.** A claim handle with `held_durable = true` is not deleted by holding-subgraph auto-terminal; only by explicit operator action or instance termination. The orphan-claim reaper skips `held_durable` rows.
- **New invariant: exit-node-writeback flows to parent run writeback.** For sub-graph parent runs, the exit node's terminal writeback is copied to the parent run's writeback row in the same transaction that records the exit's terminal.
- **New invariant: messages are inert in rimsky.** Message payload bytes are read by rimsky only via `walkPath` substitution against the trigger message and the persistence-layer fetch for control-api `GET /messages/{id}`. Rimsky never logs, formats with `%v`, validates beyond schema gates, transforms, or includes payload bytes in error messages. Same opacity discipline as `@blessed-invariant 11/20/21`.

## Testing strategy

### Conformance tests

New conformance binaries:

- **`rimsky-data-processing-conformance`** — exercises a DataProcessing-capable store against the protocol's expectations (Capabilities advertisement, BeginCandidate / CommitCandidate / AbandonCandidate semantics, ListVersions / ListPartitions / GetVersionSchema, idempotency under concurrent writes per materialization).
- **`rimsky-validation-conformance`** — exercises a Validation-advertising service against the protocol (request/response shapes, role-specific contexts, error/warning semantics).
- **`rimsky-sensor-conformance`** — exercises a Sensor service against the protocol (StartWatch / StopWatch / ListWatches; observation push integration; resync after restart).

Existing `rimsky-executor-conformance`, `rimsky-conformance-probe`, `rimsky-claim-producer-conformance` extended:

- ClaimProducer conformance adds optional-method tests for SplitScope, ScopesConflict.
- Executor conformance adds Snooze.reason emission tests.

### Scenario tests (`test/scenarios/`)

New scenario coverage:

- **Run-tree scenarios** — fan-out aggregation; state propagation; cancel-siblings; partial failures; error-policy variants; deep tree composition (sub-graph of fan-out, fan-out of sub-graphs).
- **Recursive scope scenarios** — sub-claim chains; producer-aware conflict; auto-terminal recursive resolution; mixed atomicity (failures at different depths).
- **Sub-graph scenarios** — entry-node absorption (calling node = parent run with entry's executor); exit-node writeback carry-rule; multiple invocations producing distinct run-trees; encapsulation rejection at registration; cascade-boundary opacity; aggregation rules over internal children including exit.
- **Message scenarios** — operator → invalidate → cascade; sensor → invalidate → cascade; backfill; multi-receiver matching; dead-lettering.
- **Sensor scenarios** — sensor-as-service lifecycle (StartWatch on instance creation; StopWatch on termination; resync after restart); observation routing; `sensor-cron` firing under instance parameterization including missed-fires-drop semantic; multi-instance cron sensors not interfering.
- **Asset-pattern scenarios** — durable lifetime persistence; held-durable across run completion; instance-termination cleanup; staging-then-swap with co-holders.
- **Lineage scenarios** — leaf-run record creation; claim-commit record creation; recursive ancestor walks; OpenLineage emission.
- **Atomic-staging scenarios** — Commit on all-success; Abandon on any-failure; concurrent staging; sub-stage verifier failures.
- **Backfill scenarios** — partition-selector override; status rollup; cancellation policy; lineage chain.
- **Verifier-pattern scenarios** — co-holding + aggregation drives staging promotion; cross-table verifier; mixed-pass-warn-error outcomes.

### Migration tests

Schema migrations exercise:

- `rimsky_nodes` state columns deletion (state lives only on runs after migration).
- `rimsky_node_runs` extended with parent_run_id, child_key, state, last_outcome, parked_reason columns.
- `rimsky_schedules` table removal (cron moves to `sensor-cron`).
- `rimsky_claim_handles` extended with parent_claim_handle_id, lifetime, held_durable, version_id.
- `rimsky_claim_holders` schema change to holder_run_id.
- `rimsky_wait_set` schema change to run-level granularity.
- `rimsky_messages` table creation.
- `rimsky_lineage` table creation.
- `rimsky_sensor_watches` table creation.

Pre-v1 baseline: migrations are NOT compatibility shims; they're table redefinitions. Dev-DB nuke is the recommended operator action; documented in migration notes.

### Bundled-component integration tests

- **`parquet-store` integration** — full Parquet write/read against S3 (via LocalStack) and local-fs; partition versioning; materialization variants; aggregator semantics (map_partitioned, union, merge, reduce, collect, first per producer's advertisement).
- **`geo-parquet-store` integration** — GeoParquet I/O; spatial query SDK adapter (smoke test, full coverage in SDK spec).
- **`geo-postgis-store` integration** — PostGIS query pushdown; transactional version promotion.
- **Sensor integration** — each bundled sensor exercised end-to-end (sensor-http polling a mock HTTP service; sensor-object-store against LocalStack S3; sensor-webhook receiving inbound POSTs).
- **OpenLineage emitter integration** — emission against a Marquez backend (smoke test).

### Smoke test extension

`test/smoke/setup.go` extended to boot:

- A DataProcessing-capable parquet-store.
- A bundled sensor (sensor-http).
- The OpenLineage emitter subscriber.

The smoke fixture's sequential force-fires exercise the run-tree + recursive-scope + messages + assets path end-to-end.

## Cross-references

- **Resolved tensions** consulted: `tension:_resolved/send-vs-subscribe-asymmetry` — subscription-only model preserved; messages adopt the same pattern.
- **Existing contracts** built on: `spec:2026-05-04-foundation-contract`, `spec:2026-05-04-modeling-layer-contract`, `spec:2026-05-04-service-protocol-contract`. Persistence-pluggable, frame-resolution, claim-producer redesign all assumed.
- **Sibling sketches** with surface interactions: `sketch:2026-05-14-rimsky-development-kit` (the future SDK + Python authoring layer; consumes DataProcessing protocol; would need its own design pass before implementation), `sketch:2026-05-14-agentic-platform` (consumer of this spec's `get_lineage`, `list_parked_nodes ?reason=`, `backfill`, asset endpoints; informs but doesn't change the spec).
- **Public docs successor homes** (for surface documentation; out of scope to write here): `docs/concepts/asset.md`, `docs/concepts/data-processing.md`, `docs/concepts/message.md`, `docs/concepts/sensor.md`, `docs/concepts/lineage.md`, `docs/concepts/sub-graph.md`, `docs/protocols/data-processing.md`, `docs/protocols/sensor.md`, `docs/protocols/validation.md`, `docs/agents/examples/atomic-staging.md`, `docs/agents/examples/table-pipeline.md`, `docs/agents/examples/geo-pipeline.md`.
