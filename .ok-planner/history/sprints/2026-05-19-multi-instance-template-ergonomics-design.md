# Multi-instance template ergonomics

**Date:** 2026-05-19
**Status:** design
**Sketch:** `.ok-planner/sketches/2026-05-19-multi-instance-template-ergonomics.md`

## Context

A consumer dogfooding rimsky for the omnibus-template / many-parameterized-instances pattern surfaced a batch of small ergonomic frictions when authoring a 20-node template with heavy executor reuse, long inline prompts, and a held-claim atomic-staging subgraph. The pattern itself is well-served by primitives that landed across the 2026-05 cycles (subscriptions, params, held-claim atomic-staging, sensor-driven triggers, blob spill). The frictions sit at the template-author and operator surfaces — not in the runtime model.

This spec bundles five quality-of-life items into one cycle because they're all small, all independent, and all reduce paper-cut friction on the single-template-many-instances workflow. Four are pure ergonomic polish on the template DSL, the substitution grammar, and the CLI. The fifth (verifier role in the SQL-based bundled store) is a real new capability that the same workload surfaces, fused into the existing `stores/postgres/` binary rather than landed as a standalone executor.

The sketch's "Item 5" (bulk-instance manifest CLI subcommand) was considered and declined. The downstream workload that proposed it has thousands of instances, where YAML manifests are the wrong tool; bulk loaders are. Rimsky should not absorb that responsibility.

## Item 1 — Template-level userdata defaults

### Friction

A template with many nodes using the same executor repeats per-node userdata. A 20-node template using `claude-agent` heavily has the same `model`, `handle_rate_limits`, `max_schema_corrections`, and `allowedTools` set on every node. Single-edit policy changes (e.g. upgrading every node to a new model) become a sweep across the template.

### Shape

```yaml
defaults:
  userdata:
    by_executor:
      claude-agent:
        cli:
          model: claude-opus-4-7
          handle_rate_limits: true
          max_schema_corrections: 3
```

The shape mirrors per-instance `userdata_overrides.by_executor.<name>` so the merge stack reads symmetrically across author-side defaults and operator-side overrides.

### Merge order

At dispatch time, after substitution and before validation against the executor's `userdata_schema`:

```
template.defaults.userdata.by_executor[<executor>]   # template-author baseline
→ node.userdata                                       # node-author specialization
→ instance.userdata_overrides.by_executor[<executor>] # operator baseline
→ instance.userdata_overrides.by_node[<node>]         # operator most-specific
```

Each layer wins over the prior on key collision. Deep-merge applies recursively to objects; arrays replace, per the existing `foundation/shared/jsonmerge.go::DeepMergeJSON` semantics. Operator-level overrides win over template-author defaults.

### Wiring

- `foundation/spec/template.go::TemplateSpec` gains an optional `Defaults *TemplateDefaults` field. New types: `TemplateDefaults { Userdata *TemplateUserdataDefaults }` and `TemplateUserdataDefaults { ByExecutor map[string]map[string]any }`.
- Template-registration validation rejects entries under `defaults.userdata.by_executor.<name>` where `<name>` doesn't match any node's executor (typo catcher). Validation inspects only the routing key, never fragment contents.
- Merge site: `runtime/userdata_overrides.go::applyUserdataOverrides` extends to layer template defaults underneath. Callers thread `templateDefaults.byExecutor[executor]` from the dispatched node's bound template.

### Scope

`by_executor` only. No `by_node` — author-level per-node specialization already happens by declaring `userdata:` on the node itself; a `by_node` defaults entry would be redundant.

### Validation discipline

The defaults validator inspects only routing keys (`by_executor`, plus the executor names it references), never fragment values. Same discipline as the per-instance overrides validator. Preserves the userdata-inertness invariant (`@blessed-invariant 11`).

### Inheritance-by-reference (deferred)

A more general alternative was considered: abstract "base nodes" that named nodes inherit from by reference, allowing N differently-tuned configurations against the same executor (e.g. `_base-opus` vs `_base-haiku` both using `claude-agent`). It was deferred:

- The defaults-by-executor shape closes the documented friction (one config per executor across a template).
- Adding base-node inheritance later is a clean extension: defaults stays as the always-applies bottom layer; base-node inheritance, if added, layers above defaults and below node-specific declarations.
- Inheritance-by-reference would alter the boundary of `concept:node` (abstract nodes don't materialize as `rimsky_nodes` rows), introduce a new resolution mechanism, and gate registration validation behind inheritance flattening — heavier than this cycle's polish framing warrants.

## Item 2 — `source_file:` references in templates

### Friction

Templates with long LLM system prompts (typically 50-200 lines for `claude-agent` nodes) inline them as YAML block scalars. The template YAML grows to ~600+ lines mostly because of prose. Review diffs are dominated by prompt churn, prompt authors can't use editor Markdown mode, and copying a prompt across templates means copying a YAML block.

### Shape

Anywhere a string-valued position exists in the template spec, an author may substitute `{ source_file: <relative-path> }` (an object with exactly one key, `source_file`, whose value is a path string). The CLI resolves the reference at register time:

```yaml
nodes:
  - type: discover-items
    executor: claude-agent
    userdata:
      cli:
        system_prompt:
          source_file: prompts/discover-items.system.md
        user_prompt_template:
          source_file: prompts/discover-items.user.md
```

### Resolution

`control/cli/templates.go::readSpecFile` grows a resolution pass between `os.ReadFile` and `yaml.Unmarshal(raw, &spec)`:

1. Parse the YAML to a generic `map[string]any`.
2. Walk the tree depth-first; for every object of the exact shape `{source_file: "<path>"}`, replace it with the file's text content as a plain string.
3. Re-marshal and decode into `foundation/spec/template.go::TemplateSpec`.

The wire-side `POST /templates` is unchanged. The server only ever sees resolved bytes, parsed via the existing decode path.

### Scope of application

Any string position in the spec — `userdata.cli.system_prompt` (the dominant case), `attributes.schema` JSON Schema blobs, claim `data:` payloads, `description:` fields, anything else. Uniform rule. Misuse (e.g. inlining a file where the spec expects an enum) fails loudly at typed-spec decode, which is clearer than special-casing the resolution to userdata only.

### Path resolution and containment

- Resolved relative to the template YAML's directory.
- After `filepath.Clean`, the resolved absolute path must remain inside the template directory subtree — `filepath.Rel(templateDir, resolved)` must not produce a `..` prefix. Rejects exfiltration via tampered templates.
- Absolute paths are rejected.

### Failure modes

- File missing: CLI fails fast with exit code 2 (usage / local-validation per `control/cli/templates.go::reportError`).
- Path escape or absolute path: CLI fails fast with a security error, exit code 2.
- File-content failures at typed-spec validation: surface as usual on the wire-side decode error path.

### Single pass

Files are inlined as plain text and are NOT re-parsed for further `source_file:` references. No indirection chains; no cycle detection needed.

### Hash semantics

The wire-side spec is the resolved spec; the template content-hash is over those bytes (per `concept:template`'s JCS canonicalization). Two templates with different `source_file:` references that resolve to identical content produce identical hashes. The same template YAML re-registered after a prompt-file edit produces a new hash.

### No control-api change

`POST /templates` continues to accept a single JSON body with `{spec, tag, source}`. The CLI does all resolution before the wire call.

## Item 3 — Whole-directive value lift in substitution

### Friction

Today every `{{nodes.X.attribute.Y}}` reference targets a specific leaf field; forwarding an entire structured object from upstream to downstream requires listing every field as its own source line. Numeric substitution (`{{params.count}}` against an integer param) currently produces the string `"42"`, accepted by downstream JSON Schema only via type coercion. Both are friction symptoms of one underlying gap: substitution always produces a string, even when the input directive is the entire value and the natural result is a JSON value.

### Rule

A directive resolves in one of two modes, chosen by the input string's shape:

- **Whole-directive mode.** If `trim(input)` is exactly one `{{...}}` directive (no literal characters outside the braces), the resolved JSON value is returned as-is — object, array, string, number, or bool.
- **Embedded mode.** If the input contains literal text alongside the directive (`"foo{{bar.x}}baz"`) or contains multiple directives (`"{{a}}{{b}}"`), each directive's resolution is stringified and concatenated. Current behavior preserved.

The discriminator is the input string's shape, not the directive's kind or the resolved value's type.

JSON `null` is not in the supported-types list above. `graph/attribute/substitution.go::walkPath` treats `null` along the resolution path as "not found" (existing behavior at lines 434, 439 — `null` values cause an `ErrMissingSource`). This spec does not change that behavior; lifting `null` distinctly from missing would require a `walkPath` rewrite that other callers depend on, and JSON `null` is rarely the intended substitution payload. If a consumer needs a null-bearing value, they can wrap it in an object property whose presence is independent of the inner `null`.

### Empty trailing path

Each path-walking directive kind that has a multi-segment shape admits an optional-empty trailing path. With an empty trailing path the directive resolves to the kind's JSON root:

- `{{nodes.<X>.attribute}}` — whole-attribute object (2 dot-segments; passes the universal `len(parts) >= 2` guard at `graph/attribute/substitution.go::resolveDirective#202`)
- `{{nodes.<X>.event.<name>}}` — whole named-event payload (3 dot-segments)
- `{{claim.<alias>.payload}}` — whole claim payload object (2 dot-segments)
- `{{claim.<alias>.address}}` and `.scope` — already take no trailing path; existing behavior preserved
- `{{trigger.message.payload}}` — whole trigger message payload (3 dot-segments)

`{{child.partition_key}}` keeps its existing single-field shape.

`{{params}}` (1 dot-segment) is **not** admitted as a bare form: it would fail the universal `len(parts) >= 2` guard at `resolveDirective`, which this spec deliberately does not relax. Consumers wanting "whole params" express it with an extra wrapping layer in `params_schema` (e.g. param `config: { ... }` and pull `{{params.config}}`). Whole-params pull is rare enough that the convention pays for itself.

`graph/attribute/substitution.go::resolveNodes`, `resolveClaim`, and `resolveTrigger` relax their inner length checks to accept the bare form; `walkPath` already returns the JSON root cleanly when given an empty field path.

### Implementation site

`graph/attribute/substitution.go` gains a value-returning entry point (working name: `SubstituteValue(input string, ctx ResolveContext) (any, error)`). The existing `Substitute(input, ctx) (string, error)` either becomes a stringifying wrapper around `SubstituteValue` (every caller keeps its string contract) or gets replaced and callers update (cleaner, more churn). The plan decides based on call-site count.

### Detection

A trimmed input qualifies for whole-directive mode iff `graph/attribute/substitution.go::directivePattern.FindString(trimmed) == trimmed` — the directive pattern matches the entire trimmed input with no surrounding characters.

### Pre-v1 behavior change (called out)

Today `"{{params.count}}"` against a numeric param resolves to the string `"42"`. Under the new rule, the bare-directive form produces the integer `42`. Schemas that rely on JSON Schema's type coercion may need tightening (or relaxation, depending on intent). Per project pre-v1 rules ("break freely"), this is the kind of cleanup the project welcomes. The test-suite sweep is part of the plan.

### Inertness / introspection discipline

The new `SubstituteValue` entry point does not introduce a fourth introspection site — it sits inside `walkPath`'s call frame. The three sites tracked by `tensions/substitution-introspection-site-count.md` (`walkPath`, `stringifyRaw`, `makeClaimHandle`) are unaffected.

### Adjacent tension

`tensions/substitution-grammar-count-drift.md` tracks doc-vs-code drift on the substitution kinds list. This spec partly addresses that drift by updating the `concept:attribute` invariant text (see Design Changes); the broader cross-doc consistency sweep (CLAUDE.md, `docs/concepts/attributes.md`) stays out of scope.

## Item 4 — Node-level tags

### Friction

Multi-phase templates have no way to mark which nodes belong to which category (setup vs recurring, agent-driven vs http-driven, etc.). Operators can't filter the dashboard or the events surface; the only available signal today is the node `type` string, and conventions like `onboarding.discover-items` are operator-invented and inconsistent across templates.

### Shape

```yaml
nodes:
  - type: discover-items
    executor: claude-agent
    tags: [setup, agent-driven, "domain:{{params.domain}}"]
    userdata: ...
```

Free-form strings; no rimsky-defined vocabulary; no length cap; no effect on scheduling, cascade, or validation. Pure operator-facing metadata.

### Substitution support

Tag values admit `{{params.<key>}}` substitution at materialization time (instance creation, when `rimsky_nodes` rows are written). Other source kinds are not available at that phase — `claim`, `nodes.<X>.attribute`, `nodes.<X>.event`, `trigger`, `child` all require dispatch-time context that does not yet exist when an instance is created.

The materialization-time substitution call builds a `ResolveContext` with `Params` populated and every other field nil. Per existing `graph/attribute/substitution.go::resolveDirective` semantics, an unsupported-kind directive returns `ErrMissingSource` — the materialization path treats this as a hard failure: the instance cannot be created if its tags cannot be resolved.

Composes with Item 3:

- Embedded mode (`"domain:{{params.domain}}"`): stringify-and-concat. Numeric or boolean params get rendered into the string normally.
- Whole-directive mode (`"{{params.region}}"`): lift via `SubstituteValue`. The lifted JSON value must be a string; non-string lifts fail materialization with a typed error citing the tag and the resolved JSON type. The string-only check happens at the tag site, not in the substitution engine.

### Failure modes at materialization

- Missing param → fail instance creation with `ErrMissingSource` surfaced as a typed control-api error.
- Non-string whole-directive lift → fail instance creation with a typed error.

### Validation at template registration

Tag strings are parsed for directives at registration. The existing template-registration validator under `graph/node/template_validator.go` already validates the shape `{{params.<key>}}` syntactically; this spec extends that check with a key-existence cross-check against `TemplateSpec.ParamsSchema`. If a tag references `{{params.<key>}}` whose `<key>` isn't declared in `ParamsSchema`, the registration is rejected. The cross-check is new — the existing validator does not consult `ParamsSchema` today. The same extension applies symmetrically to substitution refs in attribute schemas (catching the same kind of typo there too); the plan decides whether to extend both surfaces in this cycle or just tags.

### Storage

- New appended migration at `foundation/persistence/postgres/migrations/002-tags.sql`: `ALTER TABLE rimsky_nodes ADD COLUMN tags TEXT[] NOT NULL DEFAULT '{}'`. Per project pre-v1 rules ("migrations are numbered and append-only — that's how the migration runner works"), this is an append, not an edit-in-place to `001-baseline.sql`.
- New appended migration at `foundation/persistence/sqlite/migrations/002-tags.sql`: `ALTER TABLE rimsky_nodes ADD COLUMN tags TEXT NOT NULL DEFAULT '[]'`. SQLite stores the array as a JSON-encoded TEXT column, following the existing convention. Sibling array columns at `foundation/persistence/sqlite/migrations/001-baseline.sql#116` (`accepted_stores`) and `#134` (`required_stores`) use the same `TEXT NOT NULL DEFAULT '[]'` pattern with JSON encoding handled by the persistence layer; the dialect-drift mapping comment at `001-baseline.sql#17` documents the convention.
- Postgres: add a GIN index on `rimsky_nodes.tags` for tag-filtered listings, in the same `002-tags.sql` migration. Sibling array columns in `001-baseline.sql` (`accepted_executors`, `accepted_stores`, `required_stores`) use the bare `TEXT[]` shape; this tags column follows the same shape plus an index because tag-filtered listings are the primary read pattern.

### Row type and creation

- `foundation/persistence/nodes.go::NodeRow` gains `Tags []string`.
- `foundation/persistence/nodes.go::NodeCreateInput` gains `Tags []string`.
- The instance-creation path (`control/controlapi/instances.go`'s `POST /instances` handler) reads tags from the bound template's `TemplateNodeDef.Tags`, runs the materialization-time substitution pass with a `params`-only `ResolveContext`, and passes the resolved tags through to `NodeCreateInput`.

### Read path

`Get`, `ListByInstance`, `ListByInstancePaged` return `Tags` as part of each row. Wire-side `GET /instances/{id}/nodes` includes `tags` in each row's JSON.

### Filter

`GET /instances/{id}/nodes?tag=<value>` — single-value exact-match filter, AND-applied to the existing pagination/state filters. WHERE clause: `'<value>' = ANY(tags)` on postgres; equivalent JSON-array containment on sqlite. Multi-tag combinations (e.g. AND across multiple `?tag=` repeats) are not in v1; addable later when a friction case lands.

### `tags` and `userdata_overrides`

Tags are template-author concern. Operators cannot add or remove tags via per-instance `userdata_overrides` or any other route. The `userdata_overrides` shape today inspects only `by_executor` / `by_node` routing keys; tags are not in that envelope.

### Drift semantics

`rimsky_nodes.tags` is a projection of the template's `TemplateNodeDef.tags` at instance-creation time. The instance binds to a specific `template_hash`; the materialized tags reflect that bound template version. Re-registering a template with edited tags produces a new hash; new instances pick up the new tags; existing instances retain the tags they were materialized with.

## Item 6 — Verifier role in the SQL-based bundled store

### Friction

The atomic-staging pattern (`concept:atomic-staging`) composes a held claim spanning producer + co-holding verifiers + aggregation: the producer's `Open` creates a staging schema; verifier nodes co-hold the staging claim via `holds:`; their terminals contribute to the parent's aggregation; aggregate success fires `Commit` (atomic schema swap to production); any failure fires `Abandon` (staging dropped). This is the verify-before-promote shape complex production workloads need.

The existing bundled verifier `executors/verifier-shape-checks/` operates on rows substituted inline into userdata — it breaks down at scale, where a staged Postgres table of 50k rows cannot reasonably pass through the substitution pipe. Consumers building this pattern today either abuse `verifier-shape-checks` past its scaling envelope, roll their own verifier executor, or skip verification entirely.

### Shape

The bundled SQL-based store — `stores/postgres/` — registers `proto:executor.proto::Executor` alongside its existing `proto:claim_producer.proto::ClaimProducer` service on the same gRPC server. One binary, two protocol roles.

Per `concept:service`'s umbrella ("services can implement one or more rimsky service protocols"), the multi-protocol pattern is architecturally licensed; today's bundled binaries just haven't exercised it for the fused-store shape. (The existing `stores/postgres/server/server.go` already registers `ClaimProducer` + `LifecycleSubscriber` on one server, so the multi-service registration pattern itself is established.) The fused store is the first reference impl that fuses the executor role specifically.

Scope note: of the bundled stores currently in tree (`stores/{filesystem,postgres,stub}/`), only `postgres` is SQL-backed and applicable to this work. A future PostGIS or other SQL-substrate store can adopt the same fusion pattern; this spec restricts the work to `stores/postgres/` only.

### Userdata shape (verifier role)

```yaml
userdata:
  schema: "{{claim.staging.address}}"
  table:  items
  checks:
    - {kind: no_nulls,           config: {fields: [id, payload]}}
    - {kind: row_count_absolute, config: {min: 1000}}
    - {kind: pk_unique,          config: {fields: [id]}}
```

No `data_source:`, `connection_ref:`, `kind:`, or backend selector. The store's executor role uses the same DB connection pool that backs its producer role; both come from the store's own deployment env at startup. The `schema:` field substitutes the producer-returned claim address (via the existing `{{claim.<alias>.address}}` directive resolved in `graph/attribute/substitution.go::resolveClaim`).

### Check vocabulary (v1)

| Kind | Config | SQL shape | Failure |
|---|---|---|---|
| `no_nulls` | `fields: [c1, c2, …]` | `SELECT count(*) FILTER (WHERE c IS NULL) FROM <schema>.<table>` per named column | sum > 0 (or > `threshold` if config sets it) |
| `row_count_absolute` | `min: N`, `max?: N` | `SELECT count(*) FROM <schema>.<table>` | count < `min` or (when `max` set) count > `max` |
| `pk_unique` | `fields: [c1, c2, …]` | `SELECT c1,c2,…, count(*) FROM ... GROUP BY ... HAVING count(*) > 1 LIMIT 1` | any row returned |

Naming and config keys align with the existing in-process check vocabulary at `executors/verifier-shape-checks/checks/checks.go` so the SQL-side and shape-side vocabularies stay coherent:

- `row_count_absolute` config `{min, max?}` matches `runRowCountAbsolute` exactly.
- `no_nulls` config `fields: [...]` matches `runNoNulls`. The SQL-side adds an optional `threshold` (default 0) not present in the in-process version — a benign extension since omitting it produces the existing "fail if any null" semantic.
- `pk_unique` config `fields: [...]` matches `runPKUnique`.

Future cross-substrate verifiers should follow the same convention; cross-version config-key compatibility is the goal.

Each check compiles to one aggregate-only SQL query. The executor never reads row data; only counts and existence.

The sketch's `row_count_ratio` is deferred. It requires a baseline (live production count or operator-supplied), and either option is v1-ambiguous (where does the production schema name come from? how is the baseline pinned for re-runs?). Addable later without breaking compatibility.

### Shared check compiler

`stores/shared/sql-checks/` — Apache-licensed Go package. Holds:

- `CheckSpec` types decoded from userdata.
- Kind-keyed compilers that take `(schema, table, config)` and emit aggregate SQL.
- `Run(ctx, conn, checks) ([]Result, error)` entry point that executes the compiled queries against a `pgx.Conn` or equivalent.

`stores/postgres/` consumes this package, wiring it against its own connection pool. The package is structured to admit future consumers (a PostGIS store, an Iceberg store, etc.) without per-store reimplementation; for v1 it has exactly one consumer.

### Side-effect discipline

Verifier queries are SELECT-only by construction. The check compiler refuses to emit anything but `SELECT`-prefixed queries; a test in the shared package pins this. Producer-role DDL (schema swap, drop) goes through the separate `Commit`/`Abandon` RPCs and is unaffected.

### Terminal semantics

- All checks pass → `StreamClose.Success` with a small Struct payload summarizing per-check counts. No row content.
- Any check fails → `StreamClose.Error{ error_class: "verifier_failed", payload: <per-check failure summary> }`. Same error class as the existing `verifier-shape-checks` / `verifier-http` executors; the standard `concept:error-policy` chain handles it.

Aggregation across co-holding verifiers fires `Commit` on all-success and `Abandon` on any-failure per the atomic-staging pattern (`concept:auto-terminal`).

### Operator registration

Operators using the SQL store in both roles register it under both groups in `rimsky.yml` (same binary, same endpoint). The YAML shapes per group are different — that's the existing convention:

```yaml
claim_producers:
  postgres-staging:
    endpoint: "grpc://postgres-staging:9099"
    protocols: [claim_producer]
    write_semantics_allowed: [sync]

executors:
  postgres-staging:
    transport: grpc
    endpoint: "postgres-staging:9099"
    protocols: [executor]
```

`claim_producers:` entries use URL-scheme endpoints (`grpc://host:port`) per the convention in `deploy/rimsky.yml` and parse via `control/config/stores.go::yamlClaimProducerEntry`. `executors:` entries use `transport: grpc` + bare `host:port` endpoint per `yamlExecutorEntry`. Both entries point at the same listening port on the same binary.

Same name in both namespaces — `executors:` and `claim_producers:` are separate name-spaces in `rimsky.yml`'s structure and reuse communicates "this is one service playing two roles." Operators wanting role-separation (e.g. read-only DB role for the verifier work) deploy two instances of the same binary under distinct names with different env.

Protocols listed: `protocols: [claim_producer]` on the producer side; `protocols: [executor]` on the executor side. `stores/postgres/` does not advertise the `data_processing` or `validation` mix-ins today and this spec does not add them — those mix-ins are real protocols (`protocols/claimproducer/types.go::ProtocolDataProcessing`, `ProtocolValidation`) but are out of scope.

### Example template fragment

```yaml
graphs:
  - name: main
    nodes:
      - type: stage-items
        executor: stage-items-worker
        stores:
          - name: postgres-staging
            selector: items_staging
            intent: rw
            alias: staging
            lifetime: subgraph

      - type: verify-staged-table
        executor: postgres-staging         # the fused store, in executor role
        holds:
          staging: {from: stage-items}     # co-hold the upstream claim
        userdata:
          schema: "{{claim.staging.address}}"
          table: items
          checks:
            - {kind: no_nulls,           config: {fields: [id, payload]}}
            - {kind: row_count_absolute, config: {min: 1000}}
            - {kind: pk_unique,          config: {fields: [id]}}
```

### Licensing

`stores/postgres/` and `stores/shared/sql-checks/` are Apache-licensed. No AGPL leakage from the retired `concept:quality-rule`'s old in-process evaluator package; the retired `graph/qualityrule/` tree remains gone (per the 2026-05-15 spec's clean-break deprecation).

## Design changes

### Concept mutations

- **`concepts/userdata.md`** — under "Per-instance overrides", update the merge-order text from three layers (`template → by_executor[<executor>] → by_node[<node>]`) to four:

  ```
  template.defaults.userdata.by_executor[<executor>]
    → node.userdata
    → instance.userdata_overrides.by_executor[<executor>]
    → instance.userdata_overrides.by_node[<node>]
  ```

  More specific wins; operator-level overrides win over template-author defaults. Append a Notes entry: `2026-05-19 — Template-level userdata defaults added per spec 2026-05-19-multi-instance-template-ergonomics-design. @blessed-invariant 11 unchanged: only routing keys (by_executor plus executor names) are inspected; fragment values are never read.`

  Also fix pre-existing citation drift at lines 37 and 45 of this concept doc: replace `code:graph/shared/jsonmerge.go::DeepMergeJSON` and `graph/shared.DeepMergeJSON` with `code:foundation/shared/jsonmerge.go::DeepMergeJSON` (the package moved before this spec; bringing the citations into alignment with current code while the doc is being touched anyway).

- **`concepts/attribute.md`** — update the Invariants section. Replace the existing "closed enumeration: six source kinds" text with the current grammar (which has drifted from the doc):

  > The substitution grammar is a closed enumeration of source kinds: `nodes.<X>.attribute.<field-path>`, `nodes.<X>.event.<name>.<field-path>`, `claim.<alias>.{address|scope|payload.<field-path>}`, `params.<field-path>`, `trigger.message.payload.<field-path>`, `child.partition_key`. Each path-walking kind admits an optional-empty trailing path; with an empty trailing path the directive resolves to the kind's JSON root. Resolution is either whole-directive (the input is exactly one `{{...}}` directive modulo whitespace; returns the JSON value verbatim) or embedded (the input has literal text alongside directives; stringifies and concatenates). The legacy `deps.<X>.<Y>` form is retired and rejected with a migration-pointer error.

  Append a Notes entry: `2026-05-19 — Grammar text corrected (retired deps.*, added live trigger.* and child.*) and whole-directive value-lift documented per spec 2026-05-19-multi-instance-template-ergonomics-design. Adjacent tensions/substitution-grammar-count-drift.md is partly addressed by this update; the cross-doc-prose sweep (CLAUDE.md, docs/concepts/attributes.md) remains open.`

  Also under "Open within this concept", update the bullet referencing the three introspection sites to use the current name `code:runtime/runner_dispatch.go::makeClaimHandle` (the bullet currently names the stale `makeStoreHandle`). Same rename as the tension-file fix below; both surface the name so both need updating in lockstep.

- **`concepts/node.md`** — extend the "What it is" sentence to enumerate `tags` among the declarative fields a node carries. Add a new Invariants bullet:

  > Tag values admit `{{params.<key>}}` substitution at materialization time (instance creation); no other substitution source kinds are available at that phase. Tag substitution failures are fatal at instance creation, matching the dispatch-time discipline for required-attribute substitution. Tags do not gate dispatch, cascade, or validation — they are operator-facing metadata.

  Extend the Boundaries section's "owns" list to include "operator-facing tags." Append a Notes entry dated `2026-05-19 — Tags added per spec 2026-05-19-multi-instance-template-ergonomics-design.`

  Also fix pre-existing drift in this concept doc while it's being touched:
  - "What it is" (line 16) lists `optional quality_rules` and `optional on_event map` among the node's declarative fields. Both are retired (`concept:quality-rule` retired by spec 2026-05-15; `on_event:` retired by spec 2026-05-14 in favor of `subscribes:`). Remove both from the enumeration and add `subscribes:` and `holds:` in their place.
  - Boundaries (line 24) lists "its quality-rule evaluations" among what the node owns. Remove that phrase; the verifier-executor pattern (which replaced quality rules) is its own executor, not part of the node's owned surface.
  - Boundaries (line 24) Adjacent list includes `node-state` and `on-event-handler`, both retired per `concepts.md`. Remove both from the Adjacent list (the relevant successors — `concept:node-run` for state, `concept:node-subscription` for event handling — are already implied by other adjacencies or worth adding explicitly if not).

- **`concepts/claim-producer.md`** — extend the Boundaries section:

  > The bundled SQL-based store `stores/postgres/` additionally registers `proto:executor.proto::Executor` to support verification of its own staged content; see `concept:executor`. The same binary plays both roles via separate gRPC service registrations on a single endpoint. The pattern is open to future SQL-substrate stores adopting the same fusion.

  Append a Notes entry dated `2026-05-19 — stores/postgres/ extends to the executor role per spec 2026-05-19-multi-instance-template-ergonomics-design.`

- **`concepts/executor.md`** — extend the Boundaries section:

  > The bundled SQL-based store `stores/postgres/` registers this protocol alongside `concept:claim-producer`. The same binary plays both roles via separate gRPC service registrations on a single endpoint. Future SQL-substrate stores may adopt the same pattern.

  Append a Notes entry dated `2026-05-19 — stores/postgres/ extends to the executor role per spec 2026-05-19-multi-instance-template-ergonomics-design.`

- **`concepts/atomic-staging.md`** — append a Notes entry:

  > 2026-05-19 — Reference impl set extends from `examples/atomic-staging-fs-producer/` (POSIX filesystem) to the SQL-backed pattern demonstrated end-to-end by the fused `stores/postgres/` per spec 2026-05-19-multi-instance-template-ergonomics-design. Substrate-atomicity table unchanged.

- **`concepts/claim-co-holdership.md`** — fix pre-existing drift discovered during review. The example at line 22 uses the retired `dependencies: [load-data]` shape; post-2026-05-14 receiver-side coupling is declared via `subscribes:`. Update the example to use the current subscription form (e.g. `subscribes: [{node: load-data, on: state, when: fresh}]` or whatever fits the example's intent), or remove the `dependencies:` line if it's not load-bearing for the example.

- **`concepts/service.md`** — fix pre-existing drift discovered during review.
  - Notes at line 54: replace `sensors:` block with `publishers:` (post-2026-05-17 rename per the `publisher` / `publisher-subscription` unification collapsing sensor as one kind of publisher). Replace the conformance-binary reference `cmd/rimsky-sensor-conformance` with `cmd/rimsky-publisher-conformance` (the rename also moved the conformance binary). Adjust surrounding phrasing so the Notes paragraph reflects "publisher" as the umbrella concept and "sensor" as one class within it.
  - Adjacent list at line 45: replace `concept:sensor` with `concept:publisher` (the umbrella concept post-rename; `concept:sensor` remains as a sub-concept but service-level adjacency points at the umbrella).

- **`concepts/rimsky.md`** — extend the Boundaries section's "owns" list to include:

  > Resolution of `source_file:` references in spec YAML at `rimsky template register`, before the wire call to `POST /templates`. The wire-side spec is always resolved bytes.

  Append a Notes entry dated `2026-05-19 — source_file: client-side resolution added per spec 2026-05-19-multi-instance-template-ergonomics-design.`

- **`concepts/template.md`** — append a Notes entry:

  > 2026-05-19 — `TemplateSpec` gains optional `Defaults *TemplateDefaults` carrying template-author userdata baselines (`defaults.userdata.by_executor.<name>`). `TemplateNodeDef` gains optional `Tags []string` for operator-facing metadata (with materialization-time `{{params.<key>}}` substitution support). Both extensions are additive; hash semantics unchanged. Per spec 2026-05-19-multi-instance-template-ergonomics-design.

- **`concepts/rimsky-yml.md`** — append a Notes entry:

  > 2026-05-19 — A single service binary that plays multiple protocol roles (e.g. `stores/postgres/` as both `concept:claim-producer` and `concept:executor`) is registered under each role's namespace in this file. Reusing the same logical name across `claim_producers:` and `executors:` blocks for one binary is the canonical pattern; the entries' YAML shapes differ per the existing per-namespace conventions (URL-scheme endpoint for claim-producers, `transport:` + bare host:port for executors). Per-namespace `protocols:` enumerations are unchanged by this addition: `claim_producers:` entries continue to advertise `[claim_producer]` (plus optional mix-ins); `executors:` entries advertise `[executor]`. The new pattern is "same binary registered in both namespaces," not "new protocol values in either namespace." Per spec 2026-05-19-multi-instance-template-ergonomics-design.

  Also fix pre-existing drift at line 16 of this concept doc: the "What it is" enumeration of top-level blocks (`persistence:, named_locks:, claim_producers:, executors:`) omits `publishers:`. Add `publishers:` to the enumeration so it matches the canonical shape documented in `concepts.md` and used by `deploy/rimsky.yml`.

### Tension impacts

- `tensions/substitution-grammar-count-drift.md` — partly addressed by the `concepts/attribute.md` invariant-text update. The doc-vs-code drift inside the concept doc is resolved; the broader cross-doc sweep (CLAUDE.md, `docs/concepts/attributes.md`) remains open. Append a Notes entry to the tension file referencing this spec's partial resolution.
- `tensions/substitution-introspection-site-count.md` — the tension's prose names the third introspection site as `makeStoreHandle` in `foundation/integration/runner_dispatch.go`. The actual current name is `code:runtime/runner_dispatch.go::makeClaimHandle`. Update the tension file's prose to reflect the current name (the function was renamed/moved; `makeStoreHandle` was the older spelling). The tension remains open — the "single sanctioned introspection site" claim still drifts from three actual sites; this is a name fix only.
- No new tensions created.

## Testing strategy

### Item 1

- Unit tests on `runtime/userdata_overrides.go::applyUserdataOverrides` covering the four-layer merge: template-default applied; node override beats template default; operator `by_executor` beats template default; operator `by_node` beats operator `by_executor`; arrays replace; nested objects deep-merge.
- Template-registration validation test: `defaults.userdata.by_executor.<name>` where `<name>` is unknown is rejected.

### Item 2

- Unit tests on the CLI's resolution pass in `control/cli/templates.go`: simple inline; nested inside userdata; nested inside `attributes.schema`; multiple files in one template; missing file → error; path escape (`../`) → error; absolute path → error.
- End-to-end test: a template with `source_file:` is registered, then `GET`'d back via `GET /templates/{hash}`; the response carries resolved bytes, not the reference.
- Hash-stability test: two templates with different `source_file:` references that resolve to identical content produce identical hashes.

### Item 3

- Unit tests on `SubstituteValue`: whole-directive returns object, array, string, number, bool; embedded mode stringifies; whitespace around the directive in whole-directive mode is tolerated; multiple directives → embedded. JSON `null` along the path is treated as `ErrMissingSource` (existing `walkPath` behavior, unchanged).
- Tests on each kind's bare-form pull: `nodes.<X>.attribute` returns whole attribute; `claim.<alias>.payload` returns whole payload; `nodes.<X>.event.<name>` returns whole event payload; `trigger.message.payload` returns whole trigger payload. `{{params}}` (no field path) is NOT admitted — verify it returns `ErrMissingSource` per the universal `len(parts) < 2` guard at `resolveDirective#202`.
- Existing substitution tests are swept; tests asserting on the string form of numeric or bool substitution results are updated to expect the lifted JSON value.
- Schema-validation tests at dispatch: whole-object pull into a `type: object` property succeeds; type mismatch (`type: integer` with a string param) fails at dispatch-time validation.

### Item 4

- Migration tests for postgres + sqlite: column added with default empty array; existing rows unaffected.
- Filter test: `GET /instances/{id}/nodes?tag=X` returns only rows where `X` is in `tags`.
- Materialization-time substitution tests: missing param → instance creation fails; non-string whole-directive lift → instance creation fails; embedded mode with numeric param stringifies into the tag.
- Template-registration test: tag referencing an undeclared `params` key is rejected.

### Item 6

- Unit tests on `stores/shared/sql-checks/`: each kind's SQL compilation pinned by string equality on the emitted query; each check's pass/fail interpretation of result rows; the SELECT-only enforcement test pins that the compiler refuses non-SELECT SQL.
- Scenario test using testcontainers-go (per `test/scenarios/*` pattern) exercising an atomic-staging held-claim subgraph with `stores/postgres/` playing both roles: producer `Open` creates a staging schema; downstream nodes write data; verify-node (also `stores/postgres/`) runs checks; aggregate success fires `Commit`; data ends up in production schema. Repeat with one check failing → `Abandon`, staging dropped, production unchanged.
- Conformance probes: the standard `Executor` and `ClaimProducer` conformance suites under `cmd/rimsky-executor-conformance` and `cmd/rimsky-claim-producer-conformance` (or whatever the current binary names are; the plan verifies) pass against the fused `stores/postgres/` binary in each role.

## Non-goals

- **Bulk-instance manifest CLI** (sketch's Item 5). Declined; bulk loaders are the right tool at the scale that motivates the friction. Documented in Context.
- **Inheritance-by-reference / abstract template nodes.** Considered for Item 1; deferred per the Item 1 discussion.
- **`row_count_ratio` verifier check.** Deferred per the Item 6 vocabulary section; addable later without compatibility break.
- **Cross-doc grammar sweep.** `tensions/substitution-grammar-count-drift.md`'s broader CLAUDE.md / `docs/concepts/attributes.md` reconciliation is out of scope; this spec only updates `concepts/attribute.md`'s invariant text.
- **Read replicas for the verifier role.** Sketch Q5 — N/A under the fused-store design. Operators deploy a separate store instance against a read-replica DB role if they want read-side isolation.

## Open questions

### Resolved by this spec

- Sketch Q1 (defaults precedence vs operator overrides) — operator wins over template defaults. Item 1.
- Sketch Q2 (whole-attribute pull and schema validation) — whole-directive lift returns the JSON value as-is; receiver-side schema validates the value's actual JSON type. Item 3.
- Sketch Q3 (`source_file:` and template hash) — wire-side spec is always resolved; hash covers resolved bytes; two templates referencing identical-content files hash identically. Item 2.
- Sketch Q4 (tags in `userdata_overrides`) — tags are template-author concern; operators filter on them, do not author them. Item 4.
- Sketch Q5 (verifier read replicas) — N/A under fused-store design. Operators handle role separation by deploying separate store instances. Item 6.
- Sketch Q6 (apply and template version skew) — N/A; Item 5 declined.

### Open (surfaced for the plan)

- The exact migration of `Substitute` vs `SubstituteValue` in `graph/attribute/substitution.go` (in-place replacement vs sibling addition). The plan decides based on call-site count and test-suite blast radius.
