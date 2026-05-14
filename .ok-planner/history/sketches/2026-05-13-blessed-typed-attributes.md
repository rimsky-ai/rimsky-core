# Blessed typed-attribute stdlib

**Date:** 2026-05-13
**Status:** sketch / wishlist
**Companion sketches:** `2026-05-13-per-language-executor-sdks.md`,
`2026-05-13-verifier-executor-convention.md`,
`2026-05-13-atomic-staging-pattern.md`,
`2026-05-13-fan-in-conditional-subgraphs.md`,
`2026-05-13-parked-state-dashboard-surface.md`

## The shape

Today rimsky has two surfaces for "the thing a node produces or consumes":

- **Attributes** — typed JSON values declared by JSON Schema, validated at
  dispatch and at commit, with `source:` substitution into downstream nodes.
  Small-to-medium structured data. Blob backend spills oversized values.
- **Claims** — declared assertions against scoped state in a producer-managed
  substrate. Opaque address, opaque payload, opaque scope. Concurrency
  gating via the producer's conflict matrix.

The two cover different cases reasonably well, but the data-engineering shape
of work (load → transform → verify → dump, where intermediate values can be
quite large) sits awkwardly between them:

- Pushing the data through attributes inflates the wire payload and bloats
  the orchestrator's mediation surface.
- Pushing it through claims forces template authors and operators to think
  about substrate selection, configuration, and lifecycle for data that's
  conceptually just "the result of this node."

The proposal is a third surface: a small **blessed standard library of
attribute types** where rimsky picks the substrate, owns the implementation,
and provides predetermined semantics. Bounded. Opinionated. Substrate-aware
under the hood; substrate-erased above.

This is **not** an attempt to abstract over arbitrary substrates. We've seen
that fail (Apache Beam IO, Calcite SQL dialects, etc.). The bounded type set
is the discipline that prevents it.

## Discipline

A type earns blessing by being **excellent**, not just present. The bar:

- Rimsky implements it well enough that a consumer prefers it over rolling
  their own via claims.
- Its concurrency, lifetime, and substitution semantics are predetermined and
  documented; the consumer doesn't get to argue with them.
- The backing substrate is operator-configurable per type (and per cluster);
  the consumer doesn't see substrate choice at template-authoring time.
- The escape hatch — claims and producers — is always available for cases
  the blessed types don't cover.

If we can't make a candidate type excellent, we don't bless it. Half-baked
blessed types are worse than no blessed type, because consumers will route
around them to substrate-specific claim producers and the blessed surface
will accumulate as dead weight.

## Initial type set

Three types. Each earns its place via concrete data-engineering use cases.

### `blob` (evolve)

The existing blob backend, surfaced as a first-class blessed type.

- **Shape**: opaque bytes.
- **Concurrency**: immutable post-write. No concurrent-writer semantics.
- **Lifetime**: by default tied to holding-subgraph completion; promotable
  via `lifetime: durable`.
- **Backing store**: operator-configured (`inline | pg-largeobject |
  filesystem | s3 | gcs | azure`).
- **Wire**: read via substitution path-walk (same opacity discipline as
  attribute values per `@blessed-invariant 21`); written via executor
  writeback as an attribute value or via a handle reference.
- **SDK adapters**: `bytes`, `stream`, `iter_chunks` per language.

Already mostly there. The work is to surface it as a typed-attribute concept
rather than as "what happens when attributes spill."

### `table`

Row-oriented tabular dataset.

- **Shape**: typed rows. Columns declared in the attribute schema, mapped to
  Arrow / Parquet types.
- **Concurrency**: RW with COW versioning. Writes produce a new version;
  readers see a stable prior version. The holding-subgraph aggregate outcome
  picks the canonical version (all-success → promote new; any-failure →
  drop new, prior remains).
- **Lifetime**: holding-subgraph by default; durable via opt-in.
- **Backing store**: rimsky-managed Parquet on operator-configured object
  storage (local-fs / s3 / gcs / azure). Versioning via prefix or manifest.
- **Wire**: handle reference passed via substitution; the SDK adapter
  resolves to the substrate-native reader.
- **SDK adapters**: `to_arrow`, `to_pandas`, `to_polars`, `iter_rows`,
  `to_records` per language; symmetric writer interface.
- **Schema evolution**: additive evolution permitted with reader tolerance
  for absent newer columns; non-additive evolution produces a new version
  with no compatibility claim.

This is the type that opens rimsky to general data-engineering consumers.
Many "I have a dataset that flows through transforms" workflows that today
require ad-hoc orchestration over Spark / dbt / Airflow are expressible
directly here.

### `geo`

Geospatial features — polygons, lines, points, multipolygons — with native
geometric semantics.

Motivation: geospatial data shows up in cadastral systems, transportation
networks, environmental monitoring, location analytics, geofencing,
infrastructure planning. Every workload eventually wants
geometry-aware operations (intersect, contains, within, buffer, simplify,
area, length). Doing this through claim producers means each consumer ports
a different fragment of the PostGIS / GeoPandas / Turf.js feature set.

- **Shape**: typed feature collection. Each feature has a geometry (GeoJSON-
  compatible types), a CRS declaration, and named property fields.
- **Concurrency**: RW with COW versioning, same as `table`.
- **Lifetime**: holding-subgraph by default; durable via opt-in.
- **Backing store**: rimsky-managed. Operator picks: a PostGIS-backed
  implementation (transactional, indexed, spatial query support), or a
  Parquet-with-WKB columnar implementation (cheaper for batch).
- **Wire**: handle reference; the SDK adapter resolves to substrate-native
  spatial types (Shapely / GeoPandas / Turf.js / etc.).
- **SDK adapters**: `to_geopandas`, `to_geoarrow`, `iter_features`,
  `spatial_query(predicate, geometry)` per language.
- **Spatial operations**: a curated subset surfaced via the SDK
  (intersection, area, bbox, buffer, simplify). Substrate-aware execution
  pushes these to the backing store when possible (PostGIS ST_Intersect
  rather than client-side).

`geo` is more ambitious than `table` and benefits from a concrete consumer
push to keep its design honest. Likely shipped *after* `table` is real;
designed in parallel.

## Future candidates (not for first ship)

- **`stream`** — append-only event log. Concurrency: multi-writer fan-in,
  multi-reader fan-out, bounded retention. Implementation candidates: Kafka-
  backed, NATS-JetStream-backed, rimsky-managed log. Heavy. Skip unless a
  concrete consumer pushes.
- **`kv`** — key-value map. Concurrency: per-key serializable; could be
  CRDT-flavored for distributed cases. Useful for state-machine shapes;
  unclear it earns blessing over "use a claim against your KV substrate of
  choice."
- **`graph`** — graph data (nodes + edges). Useful for analytics, but the
  ecosystem is fragmented; abstracting across it is the Beam-style trap.

Skip these unless and until a real consumer makes the case.

## Implementation surface

The foundation layer needs:

- **A blessed-type registry.** Each type declares its lifecycle hooks
  (open/commit/abandon for write semantics), its concurrency model, its
  substitution behavior, its backing-store contract.
- **Per-type substrate drivers** in `foundation/typed_attributes/<type>/`
  (or similar). Each driver is the rimsky-owned implementation of the type
  against its operator-configured backing store. For `table`: Parquet reader
  + writer; version manifest; GC of expired versions. For `geo`: PostGIS or
  GeoParquet writers; spatial index management. For `blob`: refactor of the
  existing blob backend.
- **`rimsky.yml` config surface.** Operator declares per-type backing-store
  policy. Example:

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
    geo:
      backing: postgis
      postgis:
        dsn: postgres://...
        schema: rimsky_geo
  ```

- **Per-language SDK adapters.** See `2026-05-13-per-language-executor-sdks.md`.
- **Introspection.** `rimsky-cli` surfaces blessed-attribute state: current
  version, substrate, size, retained versions, holding-subgraph membership.
  Without this, the substrate-erased surface becomes obscuring.

## Lifetime model

A blessed attribute's lifetime is declared at the template level via the
attribute schema:

```yaml
attributes:
  parcels:
    type: table
    lifetime: subgraph     # default; reaped at holding-subgraph completion
  approved-shapes:
    type: geo
    lifetime: durable      # persisted beyond run; reaped only via explicit retention policy
```

`subgraph` is the default because most pipeline intermediate values are
transient. `durable` is opt-in because durable storage is operationally
real (capacity, replication, lifecycle policies).

The held-subgraph aggregate-outcome resolution already implements the
machinery: a `subgraph`-lifetime attribute's new version is staged during
the writing node's run; the holding-subgraph terminates with all-success →
the version promotes to canonical; with any-failure → the version drops.
This is `concept:claim-handle#held-variant` semantics applied to typed
attributes.

## Concurrency model — be honest about what it is

The RW-COW model for `table` and `geo` is:

- Writes never block reads.
- Concurrent writers from two nodes against the same attribute fork into
  two versions.
- The holding-subgraph aggregate outcome picks one (or fails the subgraph if
  conflict resolution can't be honest about which to keep).

This is fine for pipeline shapes (different nodes produce different
attributes; conflicts are rare) but is **a footgun for state-machine shapes**.
Two transformers both incrementing a counter, both committing successfully,
get two forks; one wins; the other's increment is lost.

We name the semantics honestly. `table` and `geo` are
`rw_async_cow_subgraph_picks_one`, not `rw_async_cow`. Documentation surfaces
this prominently. For state-machine workloads where this matters, the
escape hatch is a claim against a substrate with proper transactional
semantics (Postgres with `rw_serializable`, etc.).

## Schema evolution

For `table` and `geo`:

- Additive evolution (new columns / new property fields) is permitted at
  any time. Readers tolerate absence — the SDK adapter materializes missing
  columns as nulls.
- Non-additive evolution (renames, deletions, type changes) produces a new
  major version of the attribute. Old readers continue to read old versions;
  new readers read new versions. No automatic migration.
- Cross-major-version compatibility is the consumer's problem.

This matches Parquet's pragmatic stance and avoids the trap of trying to be
a schema-evolution framework.

## Relationship to claims

Internally, blessed types are implemented in terms of claims against
rimsky-internal producers (`rimsky-internal-blob-producer`,
`rimsky-internal-table-producer`, `rimsky-internal-geo-producer`). These
are bundled, deployed alongside the supervisor, and not user-configurable
beyond the `typed_attributes:` block in `rimsky.yml`.

Protocol-level: the same machinery. User-level: a different vocabulary.
This is the layering that makes the model coherent — blessed types are sugar
over the existing primitives, not a parallel system.

For consumers who need substrate-specific semantics not covered by the
blessed types, the original claim-producer surface remains. The line is
clear: blessed types when one fits; claims + producers when not. No
ambiguity, because the blessed type set is finite and named.

## Open design questions

1. **Substrate flexibility within a type.** Should `table` support multiple
   backing stores simultaneously (one cluster, some tables on S3, some on
   local FS based on size or hint)? Or one substrate per type per cluster?
   The latter is simpler; the former is more flexible. Start with one-per-
   type; add multi-backing if a real case demands it.
2. **Version retention policy.** `table` versions accumulate as nodes
   produce successive writes. Operator config needs a retention policy
   (keep N latest, keep for D days, keep until manually pruned). Default?
3. **The "rimsky-owned platform footprint" question.** Today rimsky is
   orchestration-only; data durability lives in consumer-managed substrates.
   Blessing `table` and `geo` makes rimsky responsible for the durability,
   replication, and recovery of significant data. This is a real platform
   commitment that needs operator-side runbooks for backup, restore, DR.
   Worth explicit acknowledgment in the spec.
4. **Naming.** Does "attribute" stay as the umbrella term, with "blessed
   attribute" or "typed attribute" as the variant? Or do we introduce a
   distinct name (e.g. "value"). I'd lean: stay with "attribute," qualify
   when needed. `concept:attributes` doc evolves to cover both JSON-Schema
   attributes and blessed-typed attributes.
5. **Migration from existing usage.** Pre-v1; no migration path needed. The
   blob backend evolves; consumers using it adapt. CHANGELOG entry covers
   the surface change.
6. **Whether to bless `geo` in the first ship or treat it as a fast-follow.**
   `geo` is more ambitious than `table` (spatial operations, CRS handling,
   substrate-aware predicate pushdown). One read: ship `table` first, get it
   right, then `geo` builds on the pattern. Another: design both at once so
   the type-registry surface is general enough. Lean toward the latter for
   API stability; ship implementations sequentially.

## Phasing

**Phase 1**: design lockdown.
- Type registry surface in foundation.
- `rimsky.yml` typed-attributes config.
- Substitution semantics for blessed types.
- Lifetime model and held-subgraph integration.
- Naming, vocabulary, doc reorganization.

**Phase 2**: `blob` evolution.
- Surface today's blob backend as blessed `blob` type.
- SDK adapter contracts.
- Introspection.

**Phase 3**: `table` first ship.
- Parquet backing store driver.
- Versioning + retention.
- SDK adapters (Python first, then TypeScript).
- Worked example as `docs/agents/examples/table-pipeline.md`.

**Phase 4**: `geo` first ship.
- PostGIS and GeoParquet backing-store drivers.
- Spatial operation surface in SDKs.
- Worked example as `docs/agents/examples/geo-pipeline.md`.

Each phase is a substantial chunk of design + implementation. Don't ship a
type until its design has been pressure-tested against a real consumer
workload.
