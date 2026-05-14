# Data-platform cycle — blessed typed-attributes (`blob` + `table`), per-language executor SDKs, verifier-executor convention

**Date:** 2026-05-13
**Status:** sketch / cycle proposal
**Dependencies:** `ParkReason` enum on `proto:executor.proto::Snooze`
(consumed by the Python/TS SDK's `executor.snooze(reason=...)`
surface).
**Supersedes:** `.ok-planner/history/sketches/2026-05-13-data-platform-wishlist-index.md`,
`.ok-planner/history/sketches/2026-05-13-blessed-typed-attributes.md`
(the `blob` + `table` portions),
`.ok-planner/history/sketches/2026-05-13-per-language-executor-sdks.md`,
`.ok-planner/history/sketches/2026-05-13-verifier-executor-convention.md`.

## What this cycle is

The conceptual centerpiece of the data-platform expansion. Three pieces
that lock together so tightly they ship as a single cycle:

1. **Blessed typed-attribute stdlib** — a small, bounded, opinionated
   standard library of attribute types. `blob` (evolution of today's
   blob backend) and `table` (row-oriented dataset with COW
   versioning).
2. **Per-language executor SDKs** — Python and TypeScript SDKs over the
   existing executor protocol. Hide the gRPC ceremony; expose a
   decorator/builder API; resolve blessed-typed-attribute handles into
   native types via substrate adapters.
3. **Verifier-executor convention** — collapse today's
   `concept:quality-rule` into the executor model. Quality checks
   become verifier nodes; in-process Go evaluators get deprecated;
   bundled verifier executors (`verifier-shape-checks`,
   `verifier-http`) cover the common cases.

The three are coupled because the SDK adapter contracts depend on the
blessed-type registry surface, and the verifier-shape-checks bundled
executor operates against blessed-typed attributes (and against claim-
backed substrates). Shipping them together produces one coherent worked-
example suite (`docs/agents/examples/python-executor.md`, `table-
pipeline.md`, `verifier-shape-checks.md`) rather than three rounds of
half-baked docs.

The result expands rimsky from "an orchestration platform that happens
to handle data workloads with effort" into "an orchestration platform
whose data-engineering surface is first-class and ergonomic."

---

## Piece 1: Blessed typed-attribute stdlib

### The shape

Today rimsky has two surfaces for "the thing a node produces or
consumes":

- **Attributes** — typed JSON values declared by JSON Schema, validated
  at dispatch and at commit, with `source:` substitution into downstream
  nodes. Small-to-medium structured data. Blob backend spills oversized
  values.
- **Claims** — declared assertions against scoped state in a producer-
  managed substrate. Opaque address, opaque payload, opaque scope.
  Concurrency gating via the producer's conflict matrix.

The two cover different cases reasonably well, but the data-engineering
shape of work (load → transform → verify → dump, where intermediate
values can be quite large) sits awkwardly between them:

- Pushing the data through attributes inflates the wire payload and
  bloats the orchestrator's mediation surface.
- Pushing it through claims forces template authors and operators to
  think about substrate selection, configuration, and lifecycle for data
  that's conceptually just "the result of this node."

The proposal is a third surface: a small **blessed standard library of
attribute types** where rimsky picks the substrate, owns the
implementation, and provides predetermined semantics. Bounded.
Opinionated. Substrate-aware under the hood; substrate-erased above.

This is **not** an attempt to abstract over arbitrary substrates. We've
seen that fail (Apache Beam IO, Calcite SQL dialects, etc.). The bounded
type set is the discipline that prevents it.

### Discipline

A type earns blessing by being **excellent**, not just present. The bar:

- Rimsky implements it well enough that a consumer prefers it over
  rolling their own via claims.
- Its concurrency, lifetime, and substitution semantics are
  predetermined and documented; the consumer doesn't get to argue with
  them.
- The backing substrate is operator-configurable per type (and per
  cluster); the consumer doesn't see substrate choice at template-
  authoring time.
- The escape hatch — claims and producers — is always available for
  cases the blessed types don't cover.

If we can't make a candidate type excellent, we don't bless it. Half-
baked blessed types are worse than no blessed type, because consumers
will route around them to substrate-specific claim producers and the
blessed surface will accumulate as dead weight.

### Type set for this cycle

Two types.

#### `blob` (evolve)

The existing blob backend, surfaced as a first-class blessed type.

- **Shape**: opaque bytes.
- **Concurrency**: immutable post-write. No concurrent-writer semantics.
- **Lifetime**: by default tied to holding-subgraph completion;
  promotable via `lifetime: durable`.
- **Backing store**: operator-configured (`inline | pg-largeobject |
  filesystem | s3 | gcs | azure`).
- **Wire**: read via substitution path-walk (same opacity discipline as
  attribute values per `invariant:21`); written via executor writeback
  as an attribute value or via a handle reference.
- **SDK adapters**: `bytes`, `stream`, `iter_chunks` per language.

Already mostly there. The work is to surface it as a typed-attribute
concept rather than as "what happens when attributes spill."

#### `table`

Row-oriented tabular dataset.

- **Shape**: typed rows. Columns declared in the attribute schema,
  mapped to Arrow / Parquet types.
- **Concurrency**: RW with COW versioning. Writes produce a new version;
  readers see a stable prior version. The holding-subgraph aggregate
  outcome picks the canonical version (all-success → promote new; any-
  failure → drop new, prior remains).
- **Lifetime**: holding-subgraph by default; durable via opt-in.
- **Backing store**: rimsky-managed Parquet on operator-configured
  object storage (local-fs / s3 / gcs / azure). Versioning via prefix
  or manifest.
- **Wire**: handle reference passed via substitution; the SDK adapter
  resolves to the substrate-native reader.
- **SDK adapters**: `to_arrow`, `to_pandas`, `to_polars`, `iter_rows`,
  `to_records` per language; symmetric writer interface.
- **Schema evolution**: additive evolution permitted with reader
  tolerance for absent newer columns; non-additive evolution produces a
  new version with no compatibility claim.

This is the type that opens rimsky to general data-engineering
consumers. Many "I have a dataset that flows through transforms"
workflows that today require ad-hoc orchestration over Spark / dbt /
Airflow are expressible directly here.

### Future candidates (not for this cycle)

- **`stream`** — append-only event log. Concurrency: multi-writer fan-
  in, multi-reader fan-out, bounded retention. Implementation
  candidates: Kafka-backed, NATS-JetStream-backed, rimsky-managed log.
  Heavy. Skip unless a concrete consumer pushes.
- **`kv`** — key-value map. Concurrency: per-key serializable; could be
  CRDT-flavored for distributed cases. Useful for state-machine shapes;
  unclear it earns blessing over "use a claim against your KV substrate
  of choice."
- **`graph`** — graph data (nodes + edges). Useful for analytics, but
  the ecosystem is fragmented; abstracting across it is the Beam-style
  trap.

Skip these unless and until a real consumer makes the case.

### Implementation surface

The foundation layer needs:

- **A blessed-type registry.** Each type declares its lifecycle hooks
  (open/commit/abandon for write semantics), its concurrency model, its
  substitution behavior, its backing-store contract.
- **Per-type substrate drivers** in `foundation/typed_attributes/<type>/`
  (or similar). Each driver is the rimsky-owned implementation of the
  type against its operator-configured backing store. For `table`:
  Parquet reader + writer; version manifest; GC of expired versions.
  For `blob`: refactor of the existing blob backend.
- **`rimsky.yml` config surface.** Operator declares per-type backing-
  store policy. Example:

  ```yaml
  typed_attributes:
    blob:
      backing: filesystem
      filesystem: { root: /var/lib/rimsky/blobs }
    table:
      backing: s3-parquet
      s3-parquet:
        bucket: rimsky-tables
        prefix: prod/
        versioning: prefix
  ```

- **Per-language SDK adapters.** See Piece 2.
- **Introspection.** `rimsky-cli` surfaces blessed-attribute state:
  current version, substrate, size, retained versions, holding-subgraph
  membership. Without this, the substrate-erased surface becomes
  obscuring.

### Lifetime model

A blessed attribute's lifetime is declared at the template level via
the attribute schema:

```yaml
attributes:
  parcels:
    type: table
    lifetime: subgraph     # default; reaped at holding-subgraph completion
  approved-shapes:
    type: table
    lifetime: durable      # persisted beyond run; reaped only via explicit retention policy
```

`subgraph` is the default because most pipeline intermediate values are
transient. `durable` is opt-in because durable storage is operationally
real (capacity, replication, lifecycle policies).

The held-subgraph aggregate-outcome resolution already implements the
machinery: a `subgraph`-lifetime attribute's new version is staged
during the writing node's run; the holding-subgraph terminates with
all-success → the version promotes to canonical; with any-failure →
the version drops. This is `concept:claim-handle#held-variant`
semantics applied to typed attributes.

### Concurrency model — be honest about what it is

The RW-COW model for `table` is:

- Writes never block reads.
- Concurrent writers from two nodes against the same attribute fork
  into two versions.
- The holding-subgraph aggregate outcome picks one (or fails the
  subgraph if conflict resolution can't be honest about which to keep).

This is fine for pipeline shapes (different nodes produce different
attributes; conflicts are rare) but is **a footgun for state-machine
shapes**. Two transformers both incrementing a counter, both
committing successfully, get two forks; one wins; the other's increment
is lost.

We name the semantics honestly. `table` is
`rw_async_cow_subgraph_picks_one`, not `rw_async_cow`. Documentation
surfaces this prominently. For state-machine workloads where this
matters, the escape hatch is a claim against a substrate with proper
transactional semantics (Postgres with `rw_serializable`, etc.).

### Schema evolution

For `table`:

- Additive evolution (new columns) is permitted at any time. Readers
  tolerate absence — the SDK adapter materializes missing columns as
  nulls.
- Non-additive evolution (renames, deletions, type changes) produces a
  new major version of the attribute. Old readers continue to read old
  versions; new readers read new versions. No automatic migration.
- Cross-major-version compatibility is the consumer's problem.

This matches Parquet's pragmatic stance and avoids the trap of trying
to be a schema-evolution framework.

### Relationship to claims

Internally, blessed types are implemented in terms of claims against
rimsky-internal producers (`rimsky-internal-blob-producer`,
`rimsky-internal-table-producer`). These are bundled, deployed
alongside the supervisor, and not user-configurable beyond the
`typed_attributes:` block in `rimsky.yml`.

Protocol-level: the same machinery. User-level: a different
vocabulary. This is the layering that makes the model coherent —
blessed types are sugar over the existing primitives, not a parallel
system.

For consumers who need substrate-specific semantics not covered by the
blessed types, the original claim-producer surface remains. The line
is clear: blessed types when one fits; claims + producers when not.
No ambiguity, because the blessed type set is finite and named.

### Open design questions

1. **Substrate flexibility within a type.** Should `table` support
   multiple backing stores simultaneously (one cluster, some tables on
   S3, some on local FS based on size or hint)? Or one substrate per
   type per cluster? The latter is simpler; the former is more
   flexible. Start with one-per-type; add multi-backing if a real case
   demands it.
2. **Version retention policy.** `table` versions accumulate as nodes
   produce successive writes. Operator config needs a retention policy
   (keep N latest, keep for D days, keep until manually pruned).
   Default?
3. **The "rimsky-owned platform footprint" question.** Today rimsky is
   orchestration-only; data durability lives in consumer-managed
   substrates. Blessing `table` makes rimsky responsible for the
   durability, replication, and recovery of significant data. This is
   a real platform commitment that needs operator-side runbooks for
   backup, restore, DR. Worth explicit acknowledgment in the spec.
4. **Naming.** Does "attribute" stay as the umbrella term, with
   "blessed attribute" or "typed attribute" as the variant? Or do we
   introduce a distinct name (e.g. "value"). Lean: stay with
   "attribute," qualify when needed. `concept:attribute` doc evolves to
   cover both JSON-Schema attributes and blessed-typed attributes.
5. **Migration from existing usage.** Pre-v1; no migration path
   needed. The blob backend evolves; consumers using it adapt.
   CHANGELOG entry covers the surface change.

---

## Piece 2: Per-language executor SDKs (Python + TypeScript)

### The gap

Writing a rimsky executor today means:

1. Standing up a gRPC service that implements
   `proto:executor.proto::Execute`.
2. Handling the capabilities handshake, callback URL, terminal events,
   named events, attribute writeback.
3. Resolving claim addresses to substrate-specific clients by hand.
4. Managing the executor's deployment lifecycle.

For a data engineer who wants to write a Python function that takes
data in and emits data out, this is substantial ceremony. Each new
executor re-implements the same protocol plumbing in whatever language
the consumer uses. The friction shows up most acutely in two places:

- **Data-engineering consumers** writing transformations. They want
  pandas / polars / Arrow ergonomics, not gRPC servers.
- **Agentic consumers** wiring up LLM calls. Today the
  `executors/claude-agent` reference impl wraps Claude-specific
  concerns into a usable surface; consumers wanting other LLMs or
  other agent shapes re-implement that wrapping themselves.

Per-language SDKs close both gaps without changing the executor
protocol.

### What an SDK is

A library, per language, that:

- Hosts the gRPC service for the consumer's executor.
- Handles the capabilities handshake, callback URL registration,
  terminal-event marshaling, named-event emission, attribute writeback.
- Provides a decorator / class / builder API that lets the consumer
  write a function and have it be the executor.
- Includes a **substrate-adapter registry** for blessed typed
  attributes (Piece 1) — turns blessed-attribute handles into language-
  native types.
- Provides error and retry shapes idiomatic to the language.

Two SDKs in this cycle: **Python** (the data-engineering default) and
**TypeScript** (because `executors/claude-agent` is already TS and
patterns can share). Go is workable directly against the existing
protocol bindings; add a Go SDK if consumer demand emerges. Rust
later.

### Python SDK shape

Pseudocode for the user-facing surface:

```python
from rimsky import executor, Reads, Writes, Table, Blob

@executor(name="zone-normalize", version="1.0")
def normalize(
    inputs: Reads[
        ("raw_zoning", Table),
    ],
    outputs: Writes[
        ("normalized_zoning", Table),
    ],
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

- Service hosting on the declared port.
- Capabilities advertisement (`userdata_schema`, `declared_events`).
- Attribute resolution: `inputs.raw_zoning` is a `Table` instance
  wrapping the handle; `.to_polars()` resolves the handle via the
  operator-configured substrate adapter.
- Writeback: `outputs.normalized_zoning.write_polars(df)` stages a new
  version; the SDK manages the protocol-level writeback at terminal.
- Lifecycle: a `Complete` is emitted automatically when the function
  returns; an `Error` if it raises.
- Named events: `executor.emit("milestone", {...})` available inside
  the function.
- Park: `executor.snooze(resume_at=..., reason=...)` available inside
  the function (uses the `ParkReason` enum dependency).
- Retries / blocked: idiomatic exceptions (`raise
  rimsky.BlockedError(...)`) that map to the protocol's error_class
  semantics.

`Reads[...]` and `Writes[...]` are typed containers. The SDK uses
Python type annotations to validate that the function's signature
matches the template's declared attribute schema at dispatch time,
before the function body runs.

### TypeScript SDK shape

```typescript
import { executor, Reads, Writes, Table, Blob } from "@rimsky/sdk";

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

Same machinery: handler-as-function; typed input/output containers;
substrate-resolved at access; protocol plumbing hidden.

### Substrate adapter registry

Each SDK ships with adapters for the blessed typed attributes:

- `blob` → `bytes`, `stream`, `iter_chunks` (Python); `Buffer`,
  `ReadableStream` (TypeScript).
- `table` → `to_arrow`, `to_pandas`, `to_polars`, `iter_rows`,
  `to_records` (Python); `toArrow`, `toRecords`, `iterRecords`
  (TypeScript).

Adapter resolution is operator-policy-aware. If the operator configured
`table` to be backed by `s3-parquet`, the adapter does Parquet column-
pruned reads against the S3 prefix that the handle resolves to. The
user-facing API doesn't change based on backing store.

Third parties can register adapters for substrates rimsky doesn't bless
— that's a separate plug point, used when a consumer wants their
domain-specific substrate to feel native in the SDK without going
through claim producer machinery. Optional; not first-cut.

### Error / retry shape

The protocol's `error_class` machinery maps to idiomatic exceptions:

- `raise rimsky.RetryableError(reason)` →
  `Error{error_class: "executor_errored"}` with the policy's retry
  handling.
- `raise rimsky.BlockedError(reason, payload)` →
  `Error{error_class: "executor_blocked"}`.
- `raise rimsky.GiveUpError(reason)` →
  `Error{error_class: "give_up"}`.
- Uncaught exception → `Error{error_class: "executor_errored"}` with
  exception details in the `details` field.

The SDK's job is to make the wire-shape consequences of these visible
to the consumer without requiring them to understand the protocol.

### Named-event emission

```python
@executor(name="discover")
def discover(inputs, outputs, params):
    for endpoint in candidates:
        if works(endpoint):
            executor.emit("endpoint_found", {"url": endpoint, "kind": "arcgis-rest"})
    outputs.endpoints.write(found)
```

Each `executor.emit(name, payload)` produces a `NamedEvent` in the
protocol stream before terminal. Templates that subscribe via
`on_event` handlers fire invalidates accordingly.

### Park / resume

```python
@executor(name="await-external")
def await_external(inputs, outputs, params, ctx):
    if ctx.resume_reason == "external_invalidate":
        result = read_external_result(ctx.session_token)
        outputs.result.write(result)
        return
    session = start_external_work()
    executor.snooze(
        payload=session,
        session_token=session.id,
        reason="signal_wait",         # ParkReason enum (dependency)
    )
```

Park / resume is first-class in the SDK. The resume dispatch sees
`ctx.resume_reason` and `ctx.session_token`, populated by the SDK from
the protocol's `ResumeContext`.

### Coexistence with `executors/claude-agent`

The existing TS `claude-agent` executor predates this SDK proposal.
Two options:

1. **Refactor `claude-agent` to use the TS SDK** once the SDK exists.
   The claude-agent-specific logic (CLI wrapping, MCP tools, session
   handling) stays where it is; the protocol plumbing moves to the
   SDK.
2. **Leave `claude-agent` as the worked example of bespoke protocol
   use.** The SDK is for consumers; `claude-agent` is a bundled
   reference impl.

Lean toward option 1 — the maintenance cost of two protocol
implementations in the same repo (the SDK and `claude-agent`'s bespoke
server) is real, and the SDK should be a credible enough surface that
the reference impl uses it.

### Open design questions

1. **Subprocess executors.** Should the SDK support "run a subprocess;
   treat stdin / stdout as the data plane"? This is a common shape for
   wrapping existing CLI tools. Probably yes for Python; less clear for
   TypeScript.
2. **Async vs sync.** Python: support both `async def` and `def`
   handlers. TypeScript: native async. Go: native goroutines, no
   decision needed.
3. **Hot reload / dev mode.** During development, restart-on-change
   for the executor binary. Nice-to-have, not blocking.
4. **Conformance.** Each SDK needs to pass
   `cmd:rimsky-executor-conformance` in stub mode. The SDK's tests
   should include conformance runs as regression nets.
5. **Versioning.** SDK version, protocol version, supervisor version —
   three axes. The SDK should detect protocol-version mismatch at
   startup and fail loud, not at runtime.
6. **Distribution.** Python SDK on PyPI (`pip install rimsky`).
   TypeScript SDK on npm (`@rimsky/sdk`). Versioned alongside rimsky
   core; published from CI on tag.
7. **Bundling vs separate repos.** Python and TypeScript SDKs could
   live in-tree (`sdks/python/`, `sdks/typescript/`) or as sibling
   repos. In-tree is operationally simpler for version coordination;
   sibling repos let the SDKs move at their own cadence. Lean in-tree
   until cadence pressure shows.

---

## Piece 3: Verifier-executor convention (quality-rule collapse)

### The critique

Today's `concept:quality-rule` is under-engineered specifically because
it's the wrong shape. Three forcing functions:

1. **Data location.** Evaluators receive `EvalInput.NewData` as
   `[]map[string]any` — JSON-shaped attribute writeback. For
   attribute-sized data that's fine. For dataset-sized data (the data-
   engineering shape), the data isn't in the attribute; it's in a
   substrate-backed handle. The evaluator either needs to resolve the
   handle itself (recreating the executor model with extra steps) or
   operate only on metadata.
2. **Process boundary.** `eval.Register(name, ev)` is in-process Go.
   Custom evaluators have to be linked into the supervisor binary, and
   the evaluator package is AGPL per `licensing.yml`. This rules out
   non-Go evaluators, proprietary evaluators, evaluators that need
   their own runtime (Spark, Python with pandas/polars), and
   evaluators that scale independently of the supervisor.
3. **Conceptual surface.** "Evaluator" is just "executor that returns
   pass/fail/details for a writeback." There's no architectural reason
   it deserves a separate protocol, registration, registry, or
   failure-routing convention.

The cleaner shape: **verifier nodes**. Full executor protocol. Full
deployment flexibility. Language- and runtime-agnostic. Regular error-
routing through `on_executor_errored`. The held-subgraph semantics
already give the "bad data never reaches production" guarantee that
today's commit-time quality-rule gate provides.

### The shape

A verifier is an executor with conventional userdata shape:

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
      severity: error    # or warn
```

The verifier executor receives the userdata, resolves the inherited
claim (reads from the upstream's staged data), runs the checks, and
returns a terminal:

- All pass → `Complete{changed: false}` with details in writeback
  (which checks ran, what numbers).
- Any fail with `severity: error` →
  `Error{error_class: "verifier_failed"}` with details.
- Any fail with `severity: warn` → `Complete{changed: false}` with
  details including the warnings.

Error routing through the node's `on_executor_errored` handler.
Standard machinery; no new concept.

Held-subgraph membership preserves the "bad data never reaches
production" guarantee: the verifier inherits the upstream producer's
held claim; a verifier failure forces holding-subgraph Abandon;
producer drops the staged data; production write doesn't fire.

### Bundled verifier executors

The rimsky stdlib ships a small set of bundled verifier executors.

#### `verifier-shape-checks`

Covers today's quality-rule builtins plus reasonable extensions:

- `no_nulls(fields)` — every row has non-null values for named fields.
- `nullable_fields_present(fields)` — every row has each named field
  key.
- `pk_unique(fields)` — primary-key uniqueness over named fields.
- `row_count_ratio(min_ratio)` — new row count vs prior writeback.
- `row_count_absolute(min, max)` — bounds on row count.
- `value_in_set(field, values)` — values are within an allowed set.
- `regex_match(field, pattern)` — values match a regex.
- `numeric_range(field, min, max)` — values within a numeric range.

Operates against blessed typed attributes (`table`) and against
substrate-backed claim outputs (resolves the handle, reads, evaluates).
Bundled as a small Go service.

#### `verifier-http`

Delegates the check to a consumer-side HTTP endpoint:

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

The verifier POSTs `body_template` (with claim handles substituted)
and expects a response of shape `{passed: bool, details: string,
warnings: [], errors: []}`. Maps to the protocol terminal accordingly.

This is the bundled answer to "I want to write a domain-specific check
in my consumer's language without forking rimsky-supervisor or
worrying about the in-process Go AGPL licensing."

#### Future bundled wrappers (later)

If demand emerges:

- `verifier-great-expectations` — Python service wrapping a GE suite.
  Userdata names the suite; verifier runs GE against the claim
  address; reports back.
- `verifier-soda` — same shape, SodaCL.
- `verifier-deequ` — JVM, PyDeequ as the Python variant.
- `verifier-pandera` — Python.
- `verifier-frictionless` — language-agnostic via Table Schema.

Each is a small standalone service. Ship them as the ecosystem matures
and consumer demand surfaces. Not in this cycle.

### Template authoring sugar

For ergonomics, allow a `verifiers:` block adjacent to a producing
node that desugars to verifier nodes at template-canonicalization
time:

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

Canonicalization expands this into:

- `load-zoning-verify-shape` node with `executor: verifier-shape-
  checks`, `dependencies: [load-zoning]`, claim inheritance from
  `load-zoning`, userdata derived from the `shape:` block.
- `load-zoning-verify-http` node with `executor: verifier-http`, etc.

The canonical template hash is computed over the expanded form, so the
sugar is purely an authoring affordance — the registered template is
fully explicit.

Operators reading the template see a `verifiers:` block right next to
the node it protects; readability matches today's quality-rule
declarations while the underlying mechanism is uniform nodes.

### Severity model

`severity: error | warn`. Same partition as today's
`concept:quality-rule`, but implemented by the verifier executor:

- `severity: error` → verifier returns `Error` on any failure; node
  routes through `on_executor_errored` with
  `error_class: "verifier_failed"`.
- `severity: warn` → verifier returns `Complete{changed: false}` even
  on failures, with warnings in writeback details. No error routing.

This is the meaningful piece of today's quality-rule severity
machinery that survives the collapse. The "only literal `'warning'`
diverts" footgun (`tension:quality-rule-severity-string-footgun`) goes
away — the verifier executor parses severity at userdata-validation
time and either accepts the two known values or rejects with a clear
error.

### Deprecation of in-process Go evaluators

Pre-v1; break cleanly.

- `pkg:graph/qualityrule/eval/` builtins move to `executors/verifier-
  shape-checks/` as part of the bundled verifier executor.
- `pkg:graph/qualityrule/spec.go` removed.
- `template_node.QualityRules` field removed; replaced by `verifiers:`
  sugar expanding to nodes.
- `eval.Register(name, ev)` removed; consumers needing custom checks
  use `verifier-http` or write their own verifier executor.
- AGPL licensing constraint on `pkg:graph/qualityrule/eval/` goes away
  (the package goes away). The bundled `verifier-shape-checks`
  executor stays Apache-licensed (it's the spec types only; eval code
  becomes an Apache executor binary that interprets userdata).
- `quality_rule_failed` event becomes `verifier_failed` (or just rolls
  into the generic `executor_errored` event with
  `error_class: "verifier_failed"` — decide during brainstorm).

The CHANGELOG entry for this is substantial; mention dev-DB nuke if
any schema changes ride along.

### Cross-node verifiers

Future capability: a verifier that reads from multiple upstream claims
and checks invariants across them.

```yaml
nodes:
  - type: verify-coverage
    executor: verifier-http
    dependencies: [load-features, load-references]
    inherits:
      - { claim: features }
      - { claim: references }
    userdata:
      url: "http://my-checks.internal:8080/verify-coverage"
      checks: [feature-ref-coverage-ratio]
```

The verifier resolves both claim addresses and evaluates the cross-
table invariant. This is the rimsky-native answer to "cross-table
coverage ratios," "join completeness checks," "referential integrity
across two upstream tables."

No new machinery needed — it's just an executor with two inheritances.
Worth documenting as a pattern in the worked example.

### Open design questions

1. **What does the verifier write back as its attribute value?** Today
   quality-rule failures emit `quality_rule_failed` events; the node's
   value is the writeback. For verifiers-as-nodes, the writeback
   should probably be a summary (which checks ran, results, durations)
   — useful for downstream substitution and for operator inspection.
   Schema for the summary needs design.
2. **Severity granularity.** Today's `severity: warn | error` is
   coarse. Some real-world cases want "error blocks promote, warn
   produces a row in a quality report, info logs only." Pragmatic:
   stay coarse for v1; add granularity if demand emerges.
3. **Quality-report aggregation.** Multiple verifiers across a graph
   each write a summary. Operators want a roll-up: "the Phoenix
   instance has 12 verifier nodes; 11 passed, 1 has warnings, none
   failed." This is a dashboard / `concept:operational-health` concern
   more than a verifier concern. Track separately.
4. **What about today's `null_pct` / `unique_pct` / statistical
   checks?** These show up in mature data-quality libraries (GE, Soda,
   Deequ) and aren't in the proposed `verifier-shape-checks` set.
   Probably add to `verifier-shape-checks` as it matures; or punt to
   the wrapper bundled executors for those libraries.

---

## Cross-piece interactions

- **The blessed-type registry shape drives the SDK adapter API.** The
  SDK can't ship `Table` resolution until the type registry exists.
- **`verifier-shape-checks` operates on blessed types.** Implementation
  uses the same substrate adapters as the SDK, but server-side (within
  the bundled Go executor binary). Reuse via shared Go packages where
  possible.
- **SDK + verifier-executor + claude-agent.** `claude-agent` refactor
  onto the TS SDK validates that the SDK is credible enough for the
  reference impl to use; verifier executors are early adopters of the
  Go executor patterns the SDK enshrines.

## Phasing within the cycle

1. **Type registry surface + `rimsky.yml` typed-attributes config +
   `blob` evolution.** Foundation work, no new types yet. The `blob`
   refactor teaches the implementation pattern.
2. **Python SDK first ship + `blob` adapter.** No `table` yet. Just
   the executor-protocol ergonomics. Distribute via PyPI. Worked
   example: `docs/agents/examples/python-executor.md`.
3. **`table` first ship + Python `table` adapter.** First real data-
   engineering blessed type. Worked example:
   `docs/agents/examples/table-pipeline.md`.
4. **`verifier-shape-checks` bundled executor.** Replaces quality-rule
   for the basic case. Operates against `table` (and against claim-
   backed substrates).
5. **`verifier-http` bundled executor.** Generic HTTP delegation.
6. **TypeScript SDK first ship + `blob` + `table` adapters +
   `claude-agent` refactor onto the SDK.**
7. **Deprecation cutover.** Remove `pkg:graph/qualityrule/`. Update
   templates in `docs/agents/examples/` to the new shape. CHANGELOG.

Steps 1–5 are sequenced (each depends on prior). Step 6 can run in
parallel with 4 and 5 if labor allows. Step 7 closes the cycle.

## What this isn't

- **Not a v1 commitment.** Pre-v1; break cleanly. The conceptual shape
  is more important than wire-stability through the rollout.
- **Not a demand to deprecate any existing rimsky surface** other than
  `pkg:graph/qualityrule/`. Claims, producers, the executor protocol —
  all stay.
- **Not an attempt at full substrate abstraction.** The bounded-
  discipline framing is the load-bearing observation. Don't bless types
  we can't make excellent; don't paper over substrate quirks the
  consumer needs to know about.
