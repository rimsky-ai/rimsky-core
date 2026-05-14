# Data platform extensions for rimsky

**Date:** 2026-05-14
**Status:** sketch / forward-looking design after detailed discussion
**Supersedes:** the 2026-05-13 sketches `blessed-typed-attributes`,
`per-language-executor-sdks`, `verifier-executor-convention`,
`atomic-staging-pattern`, `fan-in-conditional-subgraphs`,
`parked-state-dashboard-surface`. Those remain as historical record of how
the thinking developed; this sketch is the consolidated canonical version
incorporating the deep discussion that followed.

## What this covers

The set of platform extensions that make rimsky credible as a data-processing
platform. Not independent — these compose into a coherent expansion of
rimsky's surface for data-engineering workloads.

Conceptual centerpiece: blessed typed attributes with native partitioning,
materialization strategies, and fan-out machinery. Surrounding it: per-
language SDKs, verifier-executor convention, sensors, content lineage,
asset-thinking vocabulary, and smaller additions (atomic-staging worked
example, fan-in barriers, parked-state dashboard surface, backfills as a
control-api operation).

The shape of the work is roughly four parts:

- Part 1: the typed-attribute stdlib (the conceptual centerpiece).
- Part 2: surrounding machinery that uses it.
- Part 3: smaller additions that fall out.
- Phasing across the suite.

---

# Part 1: typed-attribute stdlib

## Why a stdlib

Today rimsky has two surfaces for "the thing a node produces or consumes":
attributes (typed JSON values; substituted by path; blob backend spills
oversized values) and claims (declared assertions against scoped state in
out-of-process producers; opaque address). These cover different shapes
reasonably, but the data-engineering shape of work sits between them —
intermediate values that can be quite large but aren't substrate-specific
enough to deserve a custom claim producer.

The stdlib of opinionated types — **blob, table, geo** — fills that gap by
giving rimsky a small bounded set of attribute types where rimsky picks the
substrate, owns the implementation, and predetermines the locking and
lifetime semantics. Substrate-aware under the hood; substrate-erased above.

This is not an attempt at full substrate abstraction. Failed attempts at
that exist (Beam IO, Calcite SQL dialects). The bounded discipline is the
load-bearing observation: each blessed type IS its substrate; there's no
negotiation across substrates the user chose. The escape hatch (claims +
producers) remains for everything the blessed types don't cover.

## Discipline for blessing

A type earns blessing by being **excellent**, not just present. The bar:

- Rimsky implements it well enough that a consumer prefers it over rolling
  their own via claims.
- Its concurrency, lifetime, partition, and materialization semantics are
  predetermined and documented; the consumer doesn't argue with them.
- The backing substrate is operator-configurable per type (and per
  cluster); the consumer doesn't see substrate choice at template-authoring
  time.
- The escape hatch (claims + producers) is always available.

Half-baked blessed types are worse than no blessed type — consumers will
route around them to substrate-specific claim producers and the blessed
surface accumulates as dead weight. Bless cautiously; ship few.

## The initial type set

Three types. Each earns its place via concrete data-engineering use cases.

### `blob` (evolve)

- **Shape**: opaque bytes.
- **Concurrency**: immutable post-write. No concurrent-writer semantics.
- **Lifetime**: holding-subgraph by default; promotable via `lifetime:
  durable`.
- **Backing**: operator-configured (`inline | pg-largeobject | filesystem |
  s3 | gcs | azure`).
- **Substitution**: read via path-walk (same opacity discipline as
  attribute values per `@blessed-invariant 21`); written via executor
  writeback.
- **SDK adapters**: `bytes`, `stream`, `iter_chunks`.

Mostly already there. The work is surfacing it as a first-class blessed
type rather than "what happens when attributes spill."

### `table`

Row-oriented tabular dataset.

- **Shape**: typed rows. Columns declared in attribute schema, mapped to
  Arrow/Parquet types.
- **Concurrency**: RW with COW versioning. Writes produce a new version;
  readers see a stable prior version. Holding-subgraph aggregate outcome
  picks the canonical version (all-success → promote new; any-failure →
  drop new; prior remains).
- **Lifetime**: holding-subgraph by default; durable via opt-in.
- **Backing**: rimsky-managed Parquet on operator-configured object storage
  (local-fs / s3 / gcs / azure). Versioning via prefix or manifest.
- **Wire**: handle reference passed via substitution; SDK adapter resolves
  to substrate-native reader.
- **SDK adapters**: `to_arrow`, `to_pandas`, `to_polars`, `iter_rows`,
  `to_records`; symmetric writer interface.
- **Schema evolution**: additive permitted with reader tolerance for absent
  columns; non-additive produces a new major version with no compatibility
  claim.

`table` is the type that opens rimsky to general data-engineering
consumers. Workloads that today require ad-hoc Spark / dbt / Airflow
plumbing become expressible directly here.

### `geo`

Geospatial features — polygons, lines, points, multipolygons — with native
geometric semantics.

- **Shape**: typed feature collection. Each feature has a geometry
  (GeoJSON-compatible types), a CRS declaration, and named property
  fields.
- **Concurrency**: RW with COW versioning, same as `table`.
- **Lifetime**: holding-subgraph by default; durable via opt-in.
- **Backing**: rimsky-managed. Operator picks: PostGIS-backed
  (transactional, indexed, spatial query support) or GeoParquet-backed
  (cheaper for batch).
- **SDK adapters**: `to_geopandas`, `to_geoarrow`, `iter_features`,
  `spatial_query(predicate, geometry)`.
- **Spatial operations**: a curated subset surfaced via the SDK
  (intersection, area, bbox, buffer, simplify). Substrate-aware execution
  pushes these to the backing store when possible (PostGIS `ST_Intersect`
  rather than client-side).

More ambitious than `table`. Designed in parallel with `table` so the type
registry surface is general; implementations sequenced (`table` first,
`geo` after).

### Future candidates (not first ship)

- **`stream`** — append-only event log. Multi-writer fan-in, multi-reader
  fan-out, bounded retention. Heavy.
- **`kv`** — key-value map. Per-key serializable; CRDT-flavored merge for
  distributed cases.
- **`graph`** — graph data (nodes + edges).

Skip unless and until a real consumer makes the case.

## Partitions as attribute property

The biggest single addition to rimsky's data-platform credibility.

**Critical framing:** partitions are a property of **stored state**, not of
work. An executor is stateless code; "which partition am I processing" is a
parameter, not an executor concept. The thing that has partitions is the
attribute (or the claim-fronted substrate). Nodes work on data; data has
partitions; nodes see them via parameters or attribute reads.

The implementation strategy is **partitions-on-attribute + fan-out as
orchestration**, not "partitions on node." Each partition has its own
version, lifetime, last-outcome. The state machine doesn't extend to
(node, partition) tuples; the persistence schema doesn't explode for
high-cardinality partitions.

### Partition types

V1 ships time-based and static-set:

- **Time-based** — `resolution: hourly | daily | weekly | monthly |
  custom_cron` with a `start` timestamp. Bounded by start; partitions
  accrue forward; retention policy caps practical cardinality.
- **Static set** — declared finite list, inline or resolved at instance
  creation from `params`. Bounded; cardinality known at registration.

V2 ships dynamic and multi-dimensional:

- **Dynamic** — partitions appear at runtime via discovery (sensor,
  executor-emit, or control-api manual). Needs the discovery mechanism
  settled.
- **Multi-dimensional** — compositions (region × date). Cardinality
  multiplicative; observability burden multiplicative.

### Per-partition versioning

Each partition has its own version chain. Materialized partitions are
immutable until invalidated; invalidate produces a new version of that
partition. Retention is operator policy, per attribute:

```yaml
typed_attributes:
  table:
    backing: s3-parquet
    s3-parquet:
      bucket: rimsky-tables
      retention:
        keep_versions_per_partition: 3
        keep_partitions_for_age: 90d   # time-based partitions only
```

Substrate driver runs a background sweep honoring the policy. Operator can
manually prune via control-api.

### Partition-mode on dependencies

How a node declares which partitions of an upstream it reads. Shapes
cascade walks.

V1 ships three modes:

- **`whole_attribute`** (default for non-fan-out consumers): read all
  partitions as a unit. Cascade fires on any partition change.
- **`per_partition`** (default for fan-out consumers whose `fan_out.over`
  is the upstream's partition keys): work unit X reads partition X.
  Cascade per-partition.
- **`partition_keys_only`**: read just the metadata (which partitions
  exist), not the contents. Used by fan-out nodes that iterate the
  partition space.

V1 explicitly does NOT ship `partition_range`, `sliding_window`,
`aggregate` (the last is equivalent to `whole_attribute`). Defer until a
real case forces them.

### Cascade reframe under partitions

Cascade walks **attribute-version edges**, not (node, partition) tuples.
A partitioned attribute's version metadata records which partitions
changed since the previous version. Downstream nodes' `dependencies:`
declarations carry the partition mode. The cascade walker, at each edge,
consults the mode:

- `whole_attribute` → fires unconditionally on any change.
- `per_partition` → fires only the affected work units (the ones whose
  `fan_out.value` matches a changed partition).
- `partition_keys_only` → fires only if the partition-key set changed,
  not on existing-partition content changes.

Modest cascade-engine surface change. No state-machine restructuring.

### Cardinality bounds

Soft cap at 100K partitions per attribute (warn at template registration).
Hard cap at 1M (reject; raise per-operator if a real consumer has the
case).

Walk-bounded behaviors:

- "Invalidate all partitions" marks the attribute as `all-partitions-stale`
  metadata; doesn't enumerate every partition synchronously. Per-partition
  reads resolve lazily.
- Per-partition invalidates enumerate explicitly; bounded by the targeted
  set.
- Backfill operations bounded by their declared range.
- Cascade walks consult per-partition change sets in version metadata, not
  the full partition space.

Foundation never iterates a million partitions in a single transaction or
single tick.

## Materialization strategies

How an attribute's writeback translates to physical substrate operations.
Declared at the attribute level, executed by the substrate driver.

### V1 strategies

- **Full replacement** (default) — every writeback produces a new version
  of the whole attribute. COW versioning. Today's behavior.
- **Append-only** — substrate driver implements "existing data stays;
  writeback appends." Each version captures the delta. `changed:bool`
  fires when rows added > 0.
- **Partition overwrite** — falls out of partitions + `map_partitioned`
  aggregator. Each work unit replaces its partition; other partitions
  stay at prior versions. Implementation is mostly per-partition full
  replacement.

### V2 / deferred

- **Merge / upsert** — requires primary-key declaration and conflict
  resolution semantics. Defer until a real consumer pushes; the shape
  composes with v1 cleanly.

### Excluded permanently

- **Snapshot / SCD-2** — too domain-specific.
- **View / ephemeral** — doesn't fit rimsky's executor-produces-writeback
  model.

### Declaration shape

```yaml
attributes:
  events:
    type: table
    materialization: append
    schema: { ... }
  parcels:
    type: table
    materialization: full
  per_region_data:
    type: table
    materialization: partition_overwrite
    partitions:
      kind: static
      keys: ["west", "east", "central"]
```

The executor sees writeback shape appropriate to the materialization. The
per-language SDK handles the mapping:

- `materialization: full` → `output.events.write_table(df)` writes full
  content.
- `materialization: append` → `output.events.append(rows)` adds rows.
- `materialization: partition_overwrite` (with fan-out) →
  `output.events.write_table(df)` replaces the work unit's partition.

The wire protocol doesn't need new fields; the materialization travels
with the attribute schema in `ExecuteRequest`.

### Idempotency

Critical for append-only.

- **Full replacement**: idempotent by construction.
- **Partition overwrite**: idempotent per partition.
- **Append-only**: NOT idempotent without explicit idempotency keys. The
  substrate driver requires an idempotency key per writeback (typically
  the work unit's identity). The driver records "writeback W from work
  unit U has been applied" and rejects re-application. Retries that
  produce the same writeback no-op.

This is a load-bearing piece of the substrate driver design.

### Backfill semantics

- Full replacement: backfilling a partition re-runs and produces new
  contents. Cascade fires if content differs.
- Append-only: backfilling is **ill-defined by default** — you can't
  deterministically re-append. V1 rejects backfills on append-only
  attributes; operators can manually invalidate to reproduce from
  scratch.
- Partition overwrite: backfilling a partition replaces it; same as full
  at the partition level.

## Fan-out machinery

The orchestration primitive that lets nodes (and eventually subgraphs)
exploit partitioned attributes. Designed agnostic to single-node vs.
subgraph work units so subgraph composition drops in cleanly when it
arrives.

### Template surface

```yaml
nodes:
  - type: per-region-load
    fan_out:
      over: "{{deps.upstream.partitions}}"   # or literal list, or substituted iterable
      parallelism: 8
      aggregator: map_partitioned
      failure_policy: { strict: true }
    executor: claude-agent
    userdata:
      prompt: "Load data for {{fan_out.value}}"
```

At dispatch, the foundation expands into **work units** — each with its
own state, claim handles, frame membership. The "node" stays a single
template-time entity; work units are runtime entities scoped to one
dispatch wave. Persistence schema implication: `rimsky_node_runs` gains
a nullable `fan_out_key` column; one `rimsky_nodes` row per (instance,
template node); per-work-unit state in `rimsky_node_runs`. The node's
externally-visible state is the aggregate roll-up.

Cardinality scales: 1M-partition fan-out doesn't create 1M `rimsky_nodes`
rows. One `rimsky_nodes` row; up to active-wave's-worth of
`rimsky_node_runs` rows; completed rows prune per retention.

### `fan_out.over` source variants

V1 ships three sources:

- **Literal list** — `fan_out.over: ["west", "east", "central"]`.
- **Iterable from substituted attribute or params** —
  `fan_out.over: "{{params.regions}}"` or
  `fan_out.over: "{{deps.upstream.value.applicable_regions}}"`.
- **Partition-key set of upstream attribute** —
  `fan_out.over: "{{deps.upstream.partitions}}"`. Natural pairing with
  `map_partitioned` + `per_partition` dependency mode.

V2 ships generator:

- **Generator** (executor emits keys via named events) —
  `fan_out.over: { generator: my-emitter-node }`. Interacts with dynamic
  partition discovery.

### Aggregator vocabulary

Bundled aggregators cover common cases without inviting a Cambrian
explosion:

- **`map_partitioned`** — each work unit writes one partition of a
  partitioned output attribute. The wrapper's output is the union of those
  partitions. Default when fan-out source is partition-shaped.
- **`union`** — work units produce rows; aggregator concatenates into a
  single non-partitioned output. Default when work-unit outputs are
  disjoint.
- **`merge`** — work units may produce overlapping rows; aggregator
  upserts by primary key. Conflict resolution:
  `last_writer_wins | error_on_conflict | custom`.
- **`reduce`** — declarative reducer (sum, min, max, count, avg). Custom
  reducers are bundled-executor or consumer-side.
- **`collect`** — pass through as an array attribute value.
- **`first`** — race semantics. The first successful work unit
  determines output; the rest are cancelled.

Custom aggregators ARE executors. `aggregator: { kind: custom, executor:
my-aggregator }` dispatches after work units complete; receives all
succeeded work-unit outputs; produces wrapper output. The escape hatch.

### Failure policy

Uniform knob across aggregators: `failure_policy:`

- **`strict`** (default) — any work-unit failure fails the wrapper. Routes
  through wrapper's `on_executor_errored`.
- **`threshold: { max_failures: N }`** or **`{ min_successes: N }`** —
  tolerate up to N failures; aggregate proceeds over what succeeded.
- **`best_effort`** — proceed with whatever succeeded; wrapper completes
  with details about which work units failed in its output writeback.

Per-aggregator sensible defaults: `map_partitioned` default is `strict` but
`best_effort` is meaningful for production partitioned-pipeline shapes;
`union` / `merge` default is `strict` (partial unions are dangerous);
`reduce` default is `strict` (most reducers meaningless over partial data);
`first` is `strict` only in the "≥1 success required" sense.

### Held-claim resolution under partial failure

When `map_partitioned` runs `best_effort` and N of M partitions fail, the
held claim on the output substrate auto-terminal **Commits** (the
aggregator chose to accept partial success). Each failed partition stays at
its prior version; the rest update. Spec carefully; this is the load-
bearing piece of the failure-policy machinery.

### Retry per-work-unit vs per-wrapper

Per-work-unit retry is the default within `failure_policy` machinery —
each work unit retries per its own `error_types` config before counting
against the threshold. Per-wrapper retry kicks in only after the failure
policy has been violated.

## Implementation surface

Adding blessed types touches the foundation:

- **A blessed-type registry** in foundation. Each type declares lifecycle
  hooks, concurrency model, substitution behavior, backing-store contract,
  partition-spec support, materialization options.
- **Per-type substrate drivers** in `foundation/typed_attributes/<type>/`.
  Each driver is the rimsky-owned implementation of the type against its
  configured backing store. For `table`: Parquet reader/writer, version
  manifest, per-partition version chains, GC. For `geo`: PostGIS or
  GeoParquet drivers. For `blob`: refactor of existing blob backend.
- **`rimsky.yml` config surface** declares per-type backing-store policy
  per cluster.
- **Per-language SDK adapters** (see Part 2).
- **Introspection** via control-api and `rimsky-cli` — current version,
  substrate, size, retained versions, partition state, holding-subgraph
  membership.

Cascade engine changes: per-attribute-version partition-change metadata;
per-dependency partition-mode dispatch at edge traversal. State machine
stays whole-node.

Persistence schema:

- `rimsky_attributes` (or equivalent) gains partition manifest.
- `rimsky_attribute_versions` gains partition-change metadata.
- `rimsky_node_runs` gains `fan_out_key` column (nullable).

## Open design questions for Part 1

1. **Substrate flexibility within a type.** Should `table` support multiple
   backing stores simultaneously (one cluster, some tables on S3, some on
   local FS based on size or hint)? Start with one-per-type; add
   multi-backing if a real case demands it.
2. **Version retention policy defaults.** "Keep latest 3 versions per
   partition" is a starting point; tune via consumer experience.
3. **Rimsky-owned platform footprint.** Today rimsky is orchestration-only;
   data durability lives in consumer-managed substrates. Blessing `table`
   and `geo` makes rimsky responsible for durability, replication,
   recovery. Real platform commitment; needs operator-side runbooks for
   backup, restore, DR.
4. **Naming.** Stay with "attribute" as the umbrella term; "blessed
   attribute" or "typed attribute" qualify when needed. `concept:attributes`
   doc evolves to cover both JSON-Schema attributes and blessed-typed.
5. **Concurrency model honesty.** `rw_async_cow_subgraph_picks_one` is
   verbose but honest. Document the "concurrent writers fork; subgraph
   resolution picks one" semantics prominently. State-machine workloads
   need the claim escape hatch.

---

# Part 2: surrounding machinery

The pieces that use the typed-attribute stdlib and complete the
data-platform story.

## Per-language executor SDKs

The ergonomic gap: writing a rimsky executor today means standing up a
gRPC service, implementing `proto:executor.proto::Execute`, handling
capabilities handshake, callback URL, terminal events, named events,
attribute writeback, address-to-handle resolution. For a data engineer
who wants to write a Python function, this is substantial ceremony.

Per-language SDKs close it without changing the protocol. Two SDKs first:
**Python** (data-engineering default) and **TypeScript** (`executors/
claude-agent` is already TS; patterns can share).

### Surface shape

```python
from rimsky import executor, Reads, Writes, Table

@executor(name="zone-normalize", version="1.0")
def normalize(
    inputs: Reads[("raw_zoning", Table)],
    outputs: Writes[("normalized_zoning", Table)],
    params: dict,
):
    df = inputs.raw_zoning.to_polars()
    df = df.with_columns([
        pl.col("zone_code").str.to_uppercase(),
        pl.col("geometry").apply(make_valid),
    ])
    outputs.normalized_zoning.write_polars(df)

if __name__ == "__main__":
    executor.serve(port=9090)
```

The SDK handles:

- gRPC service hosting.
- Capabilities advertisement (`userdata_schema`, `declared_events`).
- Attribute resolution via substrate adapters.
- Writeback semantics per materialization.
- Terminal events, named events, park/resume context.
- Error / retry / blocked / give-up idiomatic exceptions.

### Substrate adapter registry

Per-type, per-language:

- `blob` → `bytes`, `stream`, `iter_chunks`.
- `table` → `to_arrow`, `to_pandas`, `to_polars`, `iter_rows`, `to_records`;
  symmetric writers.
- `geo` → `to_geopandas`, `to_geoarrow`, `iter_features`,
  `spatial_query`.

Adapter resolution is operator-policy-aware. Same logical API across
backing stores; substrate adapter dispatches.

Third-party substrate adapters: register handlers for consumer-specific
substrates. Optional; not first-cut.

### Fan-out integration

The SDK exposes `fan_out.value` in the executor context so user functions
can reference the dispatched partition / param value. The
`Reads[("upstream", Table, partition_mode=PerPartition)]` declaration
binds the work unit to its corresponding partition automatically.

### TypeScript SDK

Same surface, idiomatic for TS:

```typescript
import { executor, Reads, Writes, Table } from "@rimsky/sdk";

export const handler = executor({
  name: "zone-normalize",
  version: "1.0",
  inputs: { raw_zoning: Table },
  outputs: { normalized_zoning: Table },
  async run({ inputs, outputs, params, ctx }) {
    const df = await inputs.raw_zoning.toArrow();
    const normalized = await normalize(df);
    await outputs.normalized_zoning.writeArrow(normalized);
  },
});

executor.serve({ port: 9090 });
```

Validates that `executors/claude-agent` refactors onto the SDK cleanly —
the maintenance cost of two protocol implementations in one repo is real.

### Coexistence with `executors/claude-agent`

The existing TS `claude-agent` executor refactors onto the SDK once it
exists. The claude-agent-specific logic (CLI wrapping, MCP tools, session
handling) stays where it is; protocol plumbing moves to the SDK.

### Distribution

- Python SDK on PyPI (`pip install rimsky`).
- TypeScript SDK on npm (`@rimsky/sdk`).
- Versioned alongside rimsky core; published from CI on tag.
- SDK detects protocol-version mismatch at startup and fails loud.

### Bundling vs separate repos

In-tree (`sdks/python/`, `sdks/typescript/`) for v1 — operational
simplicity for version coordination. Move to sibling repos if cadence
pressure shows.

## Verifier-executor convention

Replaces today's `concept:quality-rule` with the executor-shaped pattern.
Quality checks become verifier nodes; in-process Go evaluators get
deprecated; bundled verifier executors cover common cases.

### Why the collapse

Today's `concept:quality-rule` is under-engineered for the data-platform
shape:

1. **Data location**: `EvalInput.NewData` is `[]map[string]any` — JSON-
   shaped writeback. For dataset-sized data the data isn't in the
   attribute; it's in a substrate-backed handle. Evaluator either resolves
   the handle itself (recreating the executor model) or operates only on
   metadata.
2. **Process boundary**: `eval.Register(name, ev)` is in-process Go. Custom
   evaluators have to be linked into the supervisor binary. AGPL-licensed
   package. Rules out non-Go, proprietary, runtime-heavy, independently-
   scaling evaluators.
3. **Conceptual surface**: "evaluator" is just "executor that returns
   pass/fail/details for a writeback." No architectural reason it deserves
   a separate protocol, registration, registry, or failure-routing
   convention.

The cleaner shape: **verifier nodes**. Full executor protocol. Full
deployment flexibility. Language- and runtime-agnostic. Regular error-
routing through `on_executor_errored`. Held-subgraph semantics preserve
the "bad data never reaches production" guarantee.

### Shape

A verifier is an executor with conventional userdata:

```yaml
nodes:
  - type: verify-zoning
    executor: verifier-shape-checks
    dependencies: [load-zoning]
    inherits:
      - { claim: zoning-data }
    userdata:
      checks:
        - { type: no_nulls, fields: [zone_code, geometry] }
        - { type: pk_unique, fields: [district_id] }
        - { type: row_count_ratio, min_ratio: 0.5 }
      severity: error
      scope: materialized   # or delta for incremental modes
```

The verifier executor receives userdata, resolves the inherited claim
(reads upstream's staged data), runs checks, returns terminal:

- All pass → `Complete{changed: false}` with details in writeback.
- Any fail with `severity: error` → `Error{error_class:
  "verifier_failed"}` with details.
- Any fail with `severity: warn` → `Complete{changed: false}` with
  warnings in details.

Routing through `on_executor_errored`. Standard machinery.

### Bundled verifier executors

- **`verifier-shape-checks`**: today's three builtins plus reasonable
  extensions. Covers `no_nulls`, `nullable_fields_present`, `pk_unique`,
  `row_count_ratio`, `row_count_absolute`, `value_in_set`, `regex_match`,
  `numeric_range`. Operates against blessed typed attributes and
  substrate-backed claim outputs. Apache-licensed Go executor.
- **`verifier-http`**: delegates the check to a consumer-side HTTP
  endpoint. Sidesteps the AGPL in-process-registration wrinkle and lets
  consumers ship domain-specific checks at their preferred license.

```yaml
userdata:
  url: "http://my-checks.internal:8080/verify-zoning"
  method: POST
  body_template:
    attribute_handle: "{{claim.zoning-data.address}}"
    check: "geometry_validity"
  expected_status: [200]
  timeout_ms: 30000
```

Response shape: `{passed: bool, details: string, warnings: [], errors:
[]}`.

### Future bundled wrappers

If consumer demand emerges:

- `verifier-great-expectations` — Python service wrapping a GE suite.
- `verifier-soda` — SodaCL.
- `verifier-deequ` — JVM, PyDeequ as Python variant.
- `verifier-pandera` — Python.
- `verifier-frictionless` — language-agnostic Table Schema.

Each is a small standalone service. Ship as ecosystem demand justifies.

### Template authoring sugar

For ergonomics, a `verifiers:` block adjacent to a producing node
desugars to verifier nodes at template-canonicalization time:

```yaml
nodes:
  - type: load-zoning
    executor: http-node
    userdata: { ... }
    verifiers:
      - shape:
          no_nulls: [zone_code, geometry]
          pk_unique: [district_id]
          row_count_ratio: { min_ratio: 0.5 }
      - http:
          url: "http://my-checks.internal:8080/verify-geometry"
          checks: [validity, srid]
```

Canonicalizer expands into verifier nodes with `dependencies: [load-zoning]`,
claim inheritance, appropriate userdata. Canonical template hash computed
over expanded form.

### Producer-side userdata validation

A small optional extension to the ClaimProducer protocol that recovers
template-registration-time validation **without rimsky learning about
producer-specific surfaces**:

```protobuf
service ClaimProducer {
  // existing: Open, Commit, Abandon, Release, Capabilities

  rpc ValidateClaimantUserdata(ValidateClaimantUserdataRequest)
    returns (ValidateClaimantUserdataResponse);
}

message ValidateClaimantUserdataRequest {
  bytes node_userdata = 1;            // opaque to rimsky
  repeated ClaimBinding bindings = 2; // all claims this node holds against this producer
}

message ClaimBinding {
  string alias = 1;
  string selector = 2;
  string intent = 3;
}

message ValidateClaimantUserdataResponse {
  bool valid = 1;
  repeated ValidationError errors = 2;
  repeated ValidationWarning warnings = 3;
}
```

Rimsky's template canonicalizer, on registration: for each (producer,
node) with claims, call `ValidateClaimantUserdata` if the producer
advertises it. Pass the full opaque node userdata + the bindings; receive
yes/no + structured errors; reject template if any error.

**Doesn't violate `@blessed-invariant 11`**: rimsky forwards opaque bytes;
producer parses; rimsky receives a verdict. Rimsky never inspects.

Optional — producers that don't implement skip. Failure mode at unreachable
producer: permissive-with-warning by default; strict mode configurable.

Same gate as today's `userdata_schema` validation; one more step in the
existing registration pipeline.

### Cross-node verifiers

A verifier can read from multiple upstream claims and check invariants
across them — declare two inheritances; userdata references both
addresses; cross-table coverage / referential-integrity checks. Standard
pattern; no new machinery.

### Deprecation of in-process Go evaluators

Pre-v1; break cleanly.

- `graph/qualityrule/eval/` builtins move to `executors/verifier-shape-
  checks/`.
- `graph/qualityrule/spec.go` removed.
- `template_node.QualityRules` field removed; replaced by `verifiers:`
  sugar.
- `eval.Register(name, ev)` removed.
- AGPL constraint on `graph/qualityrule/eval/` dissolves with the
  package.
- `quality_rule_failed` event rolls into `executor_errored` event with
  `error_class: "verifier_failed"`.

## Sensors as bundled executors

External-trigger pattern. No new primitive — same logic as verifier-
executors: bundled executors with conventional userdata, dispatched via
existing `schedule:` or `Snooze` + callback mechanisms.

### Pattern

A sensor node:

- Dispatched on schedule (`schedule: { cron: ... }` for polling) or via
  park/resume (`Snooze` + callback for inbound signals).
- On dispatch, evaluates its watch condition.
- Condition met → completes with `changed: true`; writeback carries
  observation payload; `on_executor_complete: { invalidate: [target] }`
  fires downstream.
- Condition not met → `Snooze` until next check or callback.
- Multi-target fan-out via named events: `condition_observed: { ... }` per
  observation; downstream `on_event` handlers fire per-event.

Works today; no protocol or foundation changes. What ships is the bundled
executor set plus documentation.

### Bundled sensor set

Three cover most cases:

1. **`sensor-object-store`** — watches S3 / GCS / Azure prefix for new
   objects. Polling with cron; tracks high-watermark in writeback; emits
   per-object events (for per-object fan-out) or completes with a list
   (for batch downstream); operator chooses via userdata.
2. **`sensor-webhook`** — accepts inbound webhooks. On dispatch, emits
   `Snooze` with no `resume_at` and a stable callback URL. External
   callers POST; supervisor's callback dispatcher wakes the node. Resume
   dispatch reads callback payload, completes with `changed: true`.
   Idempotency-key support in callback contract (header
   `X-Rimsky-Idempotency-Key`).
3. **`sensor-http`** — polls an HTTP endpoint on schedule; checks against
   declared condition (status code, body JSONPath match). Completes with
   `changed: true` when match; `Snooze` otherwise. Lighter alternative to
   object-store for "the data lake exposes a REST inventory."

Not bundled in v1:

- **`sensor-sql`** — SQL polling. Substrate/connection/query surface gets
  complex fast. Consumers build via `http-node` to their own query
  service.
- **`sensor-kafka`** — Heavy dependency. Defer until real demand.

### Watermark / cursor convention

Polling sensors record state in their writeback:

- `observations: [...]` for current dispatch's findings.
- `cursor: { ... }` for state (high watermark, last-seen response
  signature).

Standard shape across bundled sensors. Operators can inspect cursor
history, replay by invalidating with prior version, etc.

### Per-observation fan-out

The `on_event` handler gains a small annotation for sensors emitting
per-observation events:

```yaml
on_event:
  new_object:
    invalidate:
      targets: [process-object]
      fan_out_value: "{{event.payload.key}}"
```

Composes sensor and fan-out machinery without special-casing either.

### Concurrency

- Polling sensors: cron-serialized by existing schedule machinery.
- Webhook sensors: concurrent dispatches — bursty webhook traffic each
  wakes a separate parked instance.

### Failure semantics

Default `on_executor_errored: { resolve: retry, retries: 3 }` with
exponential backoff. After exhausting, sensor parks with
`parked_reason: SIGNAL_WAIT` and waits for next scheduled fire.

### Interactions

- **Partitions**: per-object events drive per-object fan-out work units
  via the `fan_out_value` annotation.
- **Dynamic partition discovery (V2)**: sensors emit `partition_discovered`
  events; control-api registers; same uniform discovery pattern.
- **Parked-state reasons**: webhook sensors park with
  `PARK_REASON_SIGNAL_WAIT`.
- **Content lineage**: sensor observations recorded as inputs to downstream
  invalidated nodes.

## Content lineage

Records "what specific values produced this output value" — value lineage
on top of structural cascade.

### Why

Audit, compliance, debug, reproducibility. Cascade graph IS structural
lineage; rimsky lacks value lineage. The data is already captured by the
events log; the gap is a canonical record shape, persistent storage,
queryable projection.

### Record shape

Per attribute-version terminal:

```
LineageRecord {
  attribute_id, attribute_version, partition_key?, fan_out_key?,
  inputs: [
    { source_attribute_id, source_attribute_version,
      source_partition_keys?, substitution_path }
  ],
  claims: [
    { producer_name, scope_canonical, claim_id, open_address }
  ],
  producer: {
    executor_name, executor_version, template_hash,
    template_node_type, fan_out_value?
  },
  params_snapshot, userdata_hash, observed_at, changed
}
```

Doesn't include: column-level lineage (deferred), executor diagnostics,
the writeback itself.

### Persistence

`rimsky_lineage` table indexed by `(attribute_id, attribute_version)` for
forward lookups and `(source_attribute_id, source_attribute_version)` for
reverse. Built from events on commit; events log remains source of truth.

Retention is operator policy: default keeps lineage as long as
corresponding attribute version is retained, plus a trailing window.
Operator can prune older lineage independently.

### Query surface

Control-api:

- `GET /lineage/{attribute_id}/{version}` — record for one version.
- `GET /lineage/{attribute_id}/{version}/ancestors?depth=N` — recursive
  backward walk.
- `GET /lineage/{attribute_id}/{version}/descendants?depth=N` — forward
  walk.
- `GET /lineage/by-source/{source_attribute_id}/{source_version}` —
  reverse lookup.
- `GET /lineage/by-producer/{executor_name}?version=...` — by-producer.

Walks bounded by recursion depth.

### OpenLineage compatibility

Significant interop win for small cost. Ship a bundled lifecycle-subscriber
that emits OpenLineage events as nodes terminate.

Mapping:

- `instance_id + fan_out_key` → OpenLineage `run.runId`.
- `template_node_type` → OpenLineage `job.name`.
- Inputs / outputs → OpenLineage `inputs[] / outputs[]` with `namespace +
  name` derived from attribute identity.
- Producer metadata → OpenLineage `producer` URI.
- Custom facets carry rimsky-specific fields (claim handles, params
  snapshot, userdata hash).

Bundled as `mcp-servers/lifecycle-openlineage/` (or similar). Operators
wire via `cfg:lifecycle_subscribers` in `rimsky.yml`; runs against any
OpenLineage-compatible backend (Marquez, DataHub).

Makes rimsky plug-and-play with the data-engineering lineage ecosystem.

### V1 vs deferred

V1 ships: record shape, persistence, queries, OpenLineage emitter, node-
level lineage, sensor-as-input, fan-out and partition awareness.

Deferred:

- **Column-level lineage**: executor declares via terminal payload; SDK
  provides typed column-mapping interface. Defer until executor authors
  push.
- **Cross-instance lineage navigation UX**: falls out of existing queries
  with `instance_id` filter.

### Interactions

- **Fan-out**: each work unit's lineage record keyed by `fan_out_key`.
- **Partitions**: per-partition lineage; ancestor walks partition-aware.
- **Materialization**: incremental writebacks tag lineage with
  materialization-appropriate scope (append-only delta records; full-
  replace whole-version records).
- **Held claims**: claim_id appears as input in lineage records.
- **Backfills**: record the backfill as the trigger in resulting lineage
  records — `producer.trigger: { kind: backfill, operation_id: ...,
  reason: ... }`.

## Asset-thinking vocabulary

Presentation reframe. No new primitives. The win is positioning rimsky for
the data-engineering audience by speaking their dialect.

### The taxonomy

Three concentric categories:

- **All attributes** — anything a node produces and substitutes
  downstream. JSON-Schema-typed by default. Small values for orchestration
  metadata.
- **Blessed-typed attributes** — `blob`, `table`, `geo`. "Passable state"
  tier. Carried by handle; typed substrate driver; SDK adapters surface
  as native types.
- **Assets** — blessed-typed attributes with `lifetime: durable`. The
  subset that persists beyond a single run. What data-engineering tools
  mean by "asset" — durable artifact with provenance, lineage, versions,
  materialization history.

Every asset is a blessed-typed attribute is an attribute. JSON attributes
are never assets (no matter the lifetime — they're metadata).
`subgraph`-lifetime blessed attributes are transient passable state, not
assets. `durable`-lifetime blessed attributes ARE assets.

### Template DSL: asset shorthand

Templates pair node + attribute via an asset shorthand:

```yaml
assets:
  parcels:
    type: table
    materialization: partition_overwrite
    partitions: { kind: time, resolution: daily }
    depends_on: [boundary, source_config]
    producer:
      executor: http-node
      userdata: { ... }
```

Canonicalizer desugars to node + attribute pair at template registration.
Lossless round-trip; canonical hash over expanded form. Templates mix
shapes — `assets:` for durable blessed-typed; `nodes:` for sensors,
verifiers, control nodes, transient state, JSON metadata.

### Control-api endpoints

Alongside existing node and attribute endpoints:

- `GET /assets` — list across deployed templates.
- `GET /assets/{namespace}/{name}` — declaration, producer, materialization,
  partition spec, current state.
- `GET /assets/{namespace}/{name}/versions` — version history.
- `GET /assets/{namespace}/{name}/versions/{id}/lineage` — content lineage
  walk.
- `GET /assets/{namespace}/{name}/materialization-history` — when, by what
  run, status, duration.
- `POST /assets/{namespace}/{name}/materialize` — alias for invalidating
  producer node.

Underneath: same machinery as `/nodes/...` and `/attributes/...`.

### CLI vocabulary

```sh
rimsky-cli asset list
rimsky-cli asset show parcels
rimsky-cli asset materialize parcels
rimsky-cli asset versions parcels --limit 10
rimsky-cli asset lineage parcels --version v42
```

### Dashboard reframe

Asset-primary view surfaces durable subset prominently. Click-through to
producer node's run history. Rimsky-internals view stays for platform-
operator audience.

### Namespacing

Per-instance scoping for v1. `{instance_key}:{asset_name}`. Cross-instance
asset references and global namespacing deferred.

### Vocabulary alignment

Doc reorganization: `concept:asset` as derived concept pointing at
"durable blessed-typed attribute + producer node." Glossary lists asset,
asset version, asset materialization, asset consumer. Agent-shaped and
human-shaped docs introduce "asset" alongside "node" and "attribute."

---

# Part 3: smaller additions

## Atomic-staging pattern

Worked example doc for custom claim producers with stage-then-swap-on-
Commit semantics. Generic across substrates (Postgres schema swap, S3
prefix rename, Iceberg branch fast-forward, filesystem directory move,
manifest pointer flip).

Lands as `docs/agents/examples/atomic-staging.md`. Reference impl at
`examples/atomic-staging-fs-producer/` — Go binary; filesystem directory
swap; demonstrates the four `ClaimProducer` verbs implementing the
pattern. Sample template with two verifier nodes inheriting the staging
claim, demonstrating all-success-Commit / any-failure-Abandon end-to-end.

Atomicity caveats documented per substrate: Postgres / Iceberg / POSIX-
rename are atomic; S3 with copy+delete has a window; Kafka is incoherent
for the pattern.

Pattern composes with verifier executors for "verify staging before
promoting" without special machinery.

## Fan-in barrier executor

Investigation into ergonomics for conditional subgraph fan-in. Today's
strict-AND dependency model + on-event invalidate pattern works but is
verbose for "wait for these completion signals to fire before proceeding."

Lowest-commitment path: a bundled `barrier` executor implementing the
parked-readiness pattern with a clean userdata schema:

```yaml
nodes:
  - type: barrier
    executor: barrier
    dependencies: [spine_z]
    userdata:
      wait_for: [optional_check_1.completed, optional_check_2.completed]
      timeout_seconds: 300
      on_timeout: proceed     # or fail
    inputs_from: [intake]
    cascade_when:
      filter: applicable_subgraphs
```

A barrier reads upstream attributes to determine which signals to wait
for, parks until those arrive (via `on_event` handlers attached to the
barrier itself), then completes. Uses `parked_reason: BARRIER_WAIT` (see
parked-state section).

No foundation changes; just a bundled executor with documented userdata.
If consumer usage shows consistent pain points around the verbose
template wiring, lift to foundation-level "optional dependencies" or
"any-of-set" syntax later. For v1: ship the barrier executor and observe.

## Parked-state dashboard surface

Small extension. Today every parked node looks alike to
`concept:operational-health`; distinct usage shapes (time-wait, signal-
wait, awaiting-human, barrier-wait, retry-backoff) want operationally-
different alerting.

### Wire change

Extend `Snooze` (formerly `ParkRequested`) with a typed `reason`:

```protobuf
message Snooze {
  optional google.protobuf.Timestamp resume_at = 1;
  optional bytes payload = 2;
  optional string session_token = 3;
  optional ParkReason reason = 4;
  optional string reason_label = 5;  // free-form consumer-specific label
}

enum ParkReason {
  PARK_REASON_UNSPECIFIED = 0;
  PARK_REASON_TIME_WAIT = 1;
  PARK_REASON_SIGNAL_WAIT = 2;
  PARK_REASON_AWAITING_HUMAN = 3;
  PARK_REASON_BARRIER_WAIT = 4;
  PARK_REASON_RETRY_BACKOFF = 5;
  PARK_REASON_OTHER = 99;
}
```

### Persistence and surface

`ALTER TABLE rimsky_nodes ADD COLUMN parked_reason TEXT;` (pre-v1
baseline change).

Diagnostics endpoint accepts `?reason=` filter. `rimsky-cli parked list
--reason=awaiting_human`. Bundled dashboard distinguishes categories.

### Per-reason policy

Optional follow-up: `max_park_duration` configurable per-reason.

```yaml
max_park_duration:
  time_wait: 1h
  awaiting_human: 7d
  signal_wait: 30m
```

Different categories have different reasonable bounds.

### Bundled executor emission

`sensor-webhook`, the `barrier` executor, rate-limit-aware HTTP executors
all emit `Snooze{reason: ...}` per their natural category.

## Backfills as control-api operation

Falls out of partitions. Not a separate primitive.

```
POST /backfills {
  template: "...",
  instance_key: "...",
  node: "load-parcels",
  partition_range: { start: "2024-01-01", end: "2024-09-30" },
  reason: "fixing the geometry-validity bug"
}
```

The foundation dispatches one work unit per partition in the range,
treating each as invalidated. Each produces a new version of its
partition. Cascade walks per-partition through downstreams with
`per_partition` mode. Status surfaced as a roll-up: completes/fails/in-
flight, with drill-down to individual partitions.

Backfill recorded in lineage as the trigger: `producer.trigger: { kind:
backfill, operation_id: ..., reason: ... }` for the affected partitions'
new versions.

`rimsky-cli backfill` and MCP `backfill` tool surfaces (see agentic
sketch).

---

# Phasing across the suite

The work breaks into stages with reasonable independence.

**Stage 1 — design lockdown.** Pre-implementation alignment.

- Blessed-type registry surface in foundation.
- `rimsky.yml` `typed_attributes:` config schema.
- Substitution semantics for blessed types.
- Lifetime model and held-subgraph integration for typed attributes.
- Partition spec design (types, dependency modes, cascade rules, walk-
  bounded behaviors).
- Fan-out template DSL + work-unit lifecycle.
- Aggregator vocabulary + failure-policy semantics.
- Materialization strategy declaration + substrate-driver contract.
- Verifier-executor convention + template authoring sugar.
- ClaimProducer protocol extension for `ValidateClaimantUserdata`.
- `Snooze.reason` proto change.
- Asset taxonomy and DSL shorthand.
- Lineage record shape and persistence schema.
- Documentation reorganization for asset-thinking vocabulary.

**Stage 2 — `blob` evolution.** First blessed type. Refactor existing blob
backend into typed-attribute surface. Teaches the implementation pattern.

**Stage 3 — Python executor SDK.** Without typed-attribute adapters yet;
just executor-protocol ergonomics. Distribution via PyPI.

**Stage 4 — `table` first ship.**

- Parquet backing store driver.
- Per-partition versioning + retention.
- Materialization strategy implementations (full, append, partition
  overwrite).
- Fan-out machinery in scheduler + persistence.
- Python SDK adapter for `table`.
- Worked example: `docs/agents/examples/table-pipeline.md`.

**Stage 5 — verifier-executor shipment.**

- `verifier-shape-checks` bundled executor.
- `verifier-http` bundled executor.
- `verifiers:` template authoring sugar.
- Deprecation of `graph/qualityrule/`.

**Stage 6 — content lineage.**

- `rimsky_lineage` table and event-driven population.
- Control-api queries.
- OpenLineage emitter lifecycle-subscriber.

**Stage 7 — sensors and barrier.**

- `sensor-object-store`, `sensor-webhook`, `sensor-http` bundled
  executors.
- `barrier` bundled executor.
- `parked_reason` taxonomy and dashboard surface.

**Stage 8 — TypeScript executor SDK + claude-agent refactor.**

**Stage 9 — `geo` first ship.**

- PostGIS and GeoParquet backing-store drivers.
- Spatial operation surface in SDKs.
- Worked example: `docs/agents/examples/geo-pipeline.md`.

**Stage 10 — asset-thinking presentation.**

- Asset-named control-api endpoints.
- CLI subcommands.
- Dashboard reframe.
- Doc reorganization, glossary alignment.

**Stage 11 — atomic-staging worked example.** Doc + reference impl.

**Stage 12 — backfill control-api operation.** Falls out of partitions;
small additional surface.

**Deferred**: column-level lineage; library-wrapper verifier executors
(GE / Soda / Deequ); dynamic and multi-dimensional partition types;
generator-style `fan_out.over`; merge materialization; `stream` / `kv` /
`graph` blessed types.

Each stage is reviewable independently; each contributes to the overall
direction. Stages 1-7 form the credible-data-platform-baseline; 8-12
round out ergonomics and coverage.

---

# Open design questions across the suite

1. **Substrate flexibility within a type.** One-per-type for v1; multi-
   backing if a real case demands.
2. **Version retention defaults.** Tune via consumer experience; "keep
   latest 3 versions per partition" as starting point.
3. **Rimsky-owned platform footprint.** Blessing `table` and `geo` makes
   rimsky responsible for durability. Operator runbooks for backup,
   restore, DR are part of the shipment.
4. **High-cardinality dashboard UX.** 1M-partition fan-out shows one node
   row with aggregate status; drill-down per partition needs efficient
   pagination. Worked through during dashboard implementation.
5. **Schema evolution policy nuance.** Additive permitted; non-additive
   produces new major version with no compat claim. Reader tolerance
   semantics need careful spec (what happens when reading an old version
   under the new schema's tolerance rules).
6. **Validator behavior under unreachable producer.** Permissive-with-
   warning by default; strict-mode operator-configurable.
7. **Cross-table verifier patterns.** Documented as standard pattern (two
   inheritances; cross-table invariant in userdata) but worth a worked
   example per common shape.
8. **Sensor / dynamic partition discovery integration.** V2; settle the
   discovery mechanism (sensor / executor-emit / control-api manual)
   when shipping V2 partitions.
9. **OpenLineage facet specifics.** Which rimsky-specific fields warrant
   custom facets vs. standard fields. Mostly worked out during
   implementation.
10. **Asset namespacing for cross-template references.** V2 if external
    subgraph references arrive; out of scope until then.
