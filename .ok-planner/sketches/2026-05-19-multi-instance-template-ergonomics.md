# Multi-instance template ergonomics

**Date:** 2026-05-19
**Status:** Sketch — pre-spec design exploration
**Audience:** Future planner / implementer; rimsky maintainers

## Context

A complex consumer dogfooding rimsky for the "one template, many
parameterized instances" pattern (the omnibus-template model) surfaced
a batch of small ergonomic frictions when authoring a 20-node template
with heavy executor reuse, long inline prompts, and a held-claim
atomic-staging subgraph. The pattern itself is well-served by primitives
that already landed across the 2026-05 cycles (subscriptions, params,
held-claim atomic-staging, sensor-driven triggers, blob spill). The
frictions are at the template-author and operator surfaces — not in the
runtime model.

This sketch bundles six items into one cycle because they're all small,
all independent, and all reduce paper-cut friction on the
single-template-many-instances workflow. Five are pure ergonomic polish
on the template DSL and operator CLI. The sixth (DB-backed verifier
checks) is a real new capability — bigger than the rest but still
scoped — and is included here because it's discovered by the same
workload that surfaces the ergonomic items.

The items were surfaced by an external dogfood load. The naming and
example shapes in this sketch are illustrative-generic per project
rules: a consumer named `project-alpha` runs an omnibus template called
`item-ingest` against named domains like `analytics_production`.

---

## The bundle

| # | Item | Shape | Scope |
|---|------|-------|-------|
| 1 | Template-level `defaults:` for executor-shared userdata | Deep-merge at instance creation | Small |
| 2 | Long-prompt-from-file (or any string-leaf-from-file) | CLI-side resolve at register | Small |
| 3 | Whole-attribute object pull in substitution | Empty-path walkPath | Tiny |
| 4 | Node-level `tags:` array | Pass-through annotation | Small |
| 5 | Bulk-instance manifest (`rimsky instances apply`) | New CLI subcommand | Small |
| 6 | DB-backed verifier checks | New sibling executor | Medium |

Everything except #6 is a pure additive change to existing surfaces. #6
ships a new bundled executor.

---

## Item 1 — Template-level `defaults:` block

### Friction

A template with many nodes using the same executor repeats per-node
userdata config. A 20-node template using `claude-agent` heavily has
the same `model`, `handle_rate_limits`, `max_schema_corrections`, and
sometimes `allowedTools` set on every node. Single-edit policy changes
(e.g. upgrading every node to a new model) become a sweep across the
template.

`POST /instances` already supports `userdata_overrides.by_executor` at
the operator surface. That mechanism handles per-instance overrides but
does not let the template author declare baseline defaults.

### Proposed shape

```yaml
defaults:
  executors:
    claude-agent:
      userdata:
        cli:
          model: claude-opus-4-7
          handle_rate_limits: true
          max_schema_corrections: 3

nodes:
  - type: discover-items
    executor: claude-agent
    userdata:
      cli:
        allowedTools: [WebSearch]
        # model, handle_rate_limits, max_schema_corrections from defaults
        system_prompt: |
          ...
```

### Merge semantics

At dispatch time (after substitution, before validation against the
executor's `userdata_schema`):

```
final_userdata = deep_merge(
  defaults.executors[node.executor].userdata,  # template-author defaults
  node.userdata,                                # node-level
  instance.userdata_overrides.by_executor[node.executor],  # operator-level
  instance.userdata_overrides.by_node[node.type],          # operator-level, most-specific
)
```

Each layer wins over the prior on key collision. Deep-merge applies
recursively to nested objects; arrays replace (do not concatenate).

The merge composes cleanly with the existing `applyUserdataOverrides`
in `foundation/integration/userdata_overrides.go` — it's two new merge
layers underneath. `modeling/shared/jsonmerge.go` already has the
deep-merge primitive.

### Validation

- `defaults.executors.<name>.userdata` is opaque to rimsky (same as
  per-node userdata). Validation against `userdata_schema` runs on the
  final merged result at dispatch.
- Template registration rejects a template where
  `defaults.executors.<name>` references a name that doesn't appear on
  any node (catches typos like `default.executors.claude_agent` vs
  `claude-agent`).

### Alternatives considered

- **YAML anchors.** Template authors can already use `&anchor` /
  `<<: *anchor` to share fragments. Works, but invisible to operators
  reading the resolved template and confusing for non-YAML-experts.
  Doesn't override cleanly per-node.
- **Status quo (only operator-side overrides).** Forces every multi-node
  template to over-specify or for operators to bake template author's
  intent into their own override blocks. Misplaces the responsibility.

---

## Item 2 — Long-prompt-from-file

### Friction

Templates with multi-paragraph system prompts (LLM-driven nodes
typically have 50–200 line prompts) inline them as YAML block scalars.
The template YAML grows to ~600+ lines mostly because of prose. Side
effects: review diffs are dominated by prose changes, prompt-authors
can't use their editor's markdown mode, and copying a prompt across
templates means copying a YAML block.

### Proposed shape

A new `source_file:` form on any string-typed userdata leaf, resolved
**CLI-side** at `rimsky template register`:

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

`rimsky template register ./template.yml` reads referenced files
(resolved relative to the template YAML's directory), inlines their
contents as plain strings into the spec, and uploads the **resolved**
spec to `POST /templates`. The control-api stays unchanged: it sees
flat strings. The template hash covers the resolved bytes.

Files referenced but not present at register time: CLI fails fast with
a clear error. The wire-side template spec never carries unresolved
references.

### Why CLI-side, not server-side

Server-side resolution would require the control-api to do filesystem
I/O against a path the operator's machine has but the server may not.
Multi-file uploads at `POST /templates` complicate the wire contract.
CLI-side resolution keeps the server simple and gives the operator
control over what gets uploaded.

### Validation

- CLI rejects a `source_file:` whose target doesn't exist (404 at
  client time, never reaches server).
- CLI rejects a `source_file:` whose path escapes the template's
  directory (no `../../etc/passwd` exfiltration via a tampered
  template).
- The resolved spec is what gets content-hashed. Re-registering the
  same template YAML with edited prompt files produces a new hash.

### Alternatives considered

- **Template hash includes file digests, server fetches.** Operator
  uploads template + bundle of files; server resolves. Heavier wire
  protocol, no real benefit over CLI-side resolution.
- **YAML block scalar with an explicit "this is markdown" hint.**
  Doesn't actually solve the editor / diff problem.

---

## Item 3 — Whole-attribute object pull in substitution

### Friction

Today every `{{nodes.X.attribute.Y}}` reference targets a specific field.
Forwarding an entire structured object from upstream to downstream (e.g.,
a full config object that's passed verbatim into an http-node request
body) requires listing every field of the object as its own source line,
and the receiving schema must mirror every field of the upstream schema.
Two-way coupling on every shape change.

### Proposed shape

Allow the bare form `{{nodes.X.attribute}}` (no trailing field path) to
resolve to the entire upstream attribute object as a value:

```yaml
attributes:
  schema:
    type: object
    properties:
      full_config:
        type: object
        source: "{{nodes.generate-item-config.attribute}}"
```

The same form applies to `{{claim.alias.payload}}` (whole payload) and
`{{nodes.X.event.name}}` (whole named-event payload). Consistent rule:
empty trailing path returns the object root.

### Implementation

In `graph/attribute/substitution.go::walkPath`, treat a zero-length
`fieldPath` as a request for the object itself. The grammar parser at
`resolveNodes#300-331` already splits on `.`; the only change is that
`rest := strings.Split(expr, ".")[3:]` of length 0 returns the
attribute root rather than erroring.

### Validation

The receiving property's schema is checked normally — it must accept
the object's shape. If the upstream attribute happens to be a string or
number (uncommon but legal), the receiving property's schema must
accept that type. No new validation logic; same JSON Schema engine.

### Alternatives considered

- **Status quo.** Forces brittle field-by-field forwarding. Doesn't
  scale to compound objects.
- **A new `source_attribute: upstream-node` field** (no `{{...}}`).
  Cleaner-looking but doubles the substitution grammar surface for one
  case. The bare-form extension is one-line in the parser.

---

## Item 4 — Node-level `tags:` array

### Friction

Multi-phase templates (e.g., a template with an initial setup subgraph
and a recurring operation subgraph) have no way to mark which nodes
belong to which phase. Operators can't filter the dashboard or events
view by "show me only the recurring-phase nodes." Today the only
available signal is the node's `type` string; conventions like
`onboarding.discover-items` are operator-invented and inconsistent.

### Proposed shape

Add a free-form `tags: [string]` field on `TemplateNodeDef`:

```yaml
nodes:
  - type: discover-items
    executor: claude-agent
    tags: [setup, agent-driven]
    userdata: ...

  - type: recurring-ingest
    executor: http-node
    tags: [recurring, atomic-staging-acquirer]
    userdata: ...
```

### Semantics

- Free-form strings. No rimsky-defined vocabulary or constraints.
- Stored on the `rimsky_nodes` row (`tags TEXT[]` column).
- Returned in `GET /instances/{id}/nodes` responses
  (`{ node_id, type, state, tags, ... }`).
- Exposed as a `tags` filter parameter in the dashboard SPA.
- Searchable via `GET /instances/{id}/nodes?tag=recurring`.

### Non-goals

- No rimsky-side semantics. Tags don't gate scheduling, cascade, or
  validation. They're operator-facing metadata.
- No `meta: {key: value}` map. A `tags:` array covers the
  group-and-filter use cases; richer metadata can be added later if
  consumers demonstrate need.

### Schema migration

One column on `rimsky_nodes`. No semantic changes; pure additive.

---

## Item 5 — Bulk-instance manifest

### Friction

A consumer running an omnibus template against N domains has to script
N `POST /instances` calls. There's no idempotent "reconcile cluster
state to match this manifest" workflow for instances. `rimsky compose
up -f compose.yml` solves the analogous problem at the template level
(register N templates, create their initial instances as a unit), but
not at the instance level for a single template's many instances.

### Proposed shape

A new CLI subcommand `rimsky instances apply -f manifest.yml`:

```yaml
# instances.yml
template: item-ingest@1.0
instances:
  - instance_key: project-alpha
    params:
      domain: alpha.example.com
      region: us-west
  - instance_key: project-beta
    params:
      domain: beta.example.com
      region: us-east
```

```bash
rimsky instances apply -f instances.yml
# → 2 created, 0 skipped, 0 errors
```

### Semantics

- One template at a time. Different templates → different manifest
  files. (Or extend `rimsky-compose.yml`'s existing `instances:` block
  for the multi-template case.)
- Idempotent on `instance_key`. Re-running the same `apply` is a no-op
  for already-existing instances.
- **No reconciliation of params on existing instances.** Changing an
  instance's params after creation is a separate, non-trivial concern
  (some params may be load-bearing for resources already created).
  This sketch's `apply` is create-only-when-absent.
- **No teardown of instances absent from the manifest.** Removing an
  instance from the manifest is a no-op against the cluster. Use
  `rimsky instance cancel <key>` explicitly.

### Wire shape

Either:
- **Client-side fan-out.** CLI reads the manifest, issues N parallel
  `POST /instances` calls, aggregates results. Zero new control-api
  surface.
- **New `POST /instances/apply` endpoint.** Server reads the manifest,
  loops internally. Single round-trip, atomic-ish (still creates one
  instance at a time underneath).

Client-side is simpler; pick that unless a single-round-trip property
is load-bearing for some operator workflow we haven't seen.

### Alternatives considered

- **Document a recommended client-side script pattern.** Works but
  every consumer reinvents the loop, error handling, and idempotency
  story. Rimsky should own this since `instance_key`-based idempotency
  is rimsky's contract.
- **Extend `rimsky-compose.yml`.** Already supports an `instances:`
  block. `apply` is a degenerate case (one template); could fold in.
  The cycle-cost difference is small; pick whichever fits the existing
  CLI's verb grammar better.

---

## Item 6 — DB-backed verifier checks

### Friction

`verifier-shape-checks` accepts `rows: [...]` in userdata, where rows
are populated by substitution from upstream attributes or claim
payloads. This works for small datasets but breaks down at scale: a
verifier checking row counts and null rates on a staged database table
of, say, 50,000 rows cannot reasonably pass all 50k rows through the
substitution pipe.

The atomic-staging pattern (held claim spans producer + verifier nodes;
producer Open creates a staging schema; verifiers co-hold; aggregate
success fires producer Commit which atomic-swaps the schema into
production) is exactly the verify-before-promote shape that complex
production workloads need. The bundled verifier is the missing piece.

### Two shape options

**Option A: Extend `verifier-shape-checks` with optional `data_source:` block.**

```yaml
userdata:
  data_source:
    kind: postgres
    connection_ref: analytics-pg        # named in rimsky.yml
    schema: "{{claim.staging.address}}" # substituted at dispatch
    table: items
  checks:
    - { kind: no_nulls, config: { fields: [id, payload] } }
    - { kind: row_count_gte, config: { rows: 1000 } }
    - { kind: pk_unique, config: { fields: [id] } }
```

When `data_source:` is present, the executor connects to the named
connection, runs aggregate-only SQL (`SELECT count(*), count(*) FILTER
(WHERE id IS NULL) FROM <schema>.<table>`, etc.), and evaluates checks
against the results. When `data_source:` is absent, the existing
`rows:`-based shape applies.

Connection refs are declared in `rimsky.yml`:

```yaml
verifier_data_sources:
  analytics-pg:
    kind: postgres
    dsn: ${ANALYTICS_PG_URL}
    max_open_conns: 5
```

**Option B: Ship a new sibling executor `verifier-sql-checks`.**

Same idea as A, but as a separate binary. `verifier-shape-checks` stays
pure (in-memory shape validation on inline rows). `verifier-sql-checks`
is the DB-aware sibling; same `checks:` vocabulary, plus the
`data_source:` block.

### Recommendation

**Option B.** Reasons:

1. **Separation of concerns.** Shape-checks is generic; SQL-checks is
   Postgres-specific. Two cleanly-scoped executors are easier to
   reason about and test than one executor with optional modes.
2. **Dependency boundaries.** SQL-checks needs the Postgres driver,
   connection pool, and DSN credentials. Bundling that into
   shape-checks bloats the latter for consumers that don't need it.
3. **Future extensibility.** Other backends (`verifier-iceberg-checks`,
   `verifier-bigquery-checks`) follow the same pattern without
   collapsing shape-checks into a multi-backend dispatcher.

Wire shape:

```yaml
# in rimsky.yml
verifier_data_sources:
  staging-pg:
    kind: postgres
    dsn: ${STAGING_PG_URL}
    max_open_conns: 5

executors:
  verifier-sql-checks:
    transport: grpc
    endpoint: "verifier-sql-checks:9093"
    protocols: [executor]
```

The executor reads `verifier_data_sources` from its own startup config
(separate concern from rimsky.yml's `executors:` block) — same pattern
as claude-agent's MCP catalog.

### Check vocabulary

Shared with `verifier-shape-checks` where the semantics carry over:

- `no_nulls` — `SELECT count(*) FILTER (WHERE col IS NULL) FROM ...`
  for each named column. Fails if > 0 (or > `threshold` if set).
- `row_count_gte` — `SELECT count(*) FROM ...`. Fails if below the
  declared minimum.
- `row_count_ratio` — needs a baseline. Either fetched from the live
  schema (`production_<key>`) or passed in as a baseline value. Decide
  during spec; this is the only check with a "previous run" semantic
  and may not belong in v1.
- `pk_unique` — `SELECT col, count(*) FROM ... GROUP BY col HAVING
  count(*) > 1 LIMIT 1`. Fails if any duplicates exist.

Each check runs as one aggregate-only SQL query. The executor never
reads row data — only counts and existence.

### Operator obligations

- Provision the connection's credentials.
- Ensure the verifier's DB role has SELECT on the schemas it checks
  (typically staging schemas, scoped narrowly).
- Treat the connection refs as auth-bearing infrastructure config:
  rimsky stays auth-blind; the operator owns credential delivery.

### Alternatives considered

- **Don't ship; consumers write their own.** Each consumer that uses
  atomic-staging reimplements the same handful of aggregate-SQL
  checks. Rimsky already owns Postgres-side primitives (postgres
  claim producer); a Postgres verifier is consistent.
- **Make `verifier-shape-checks` accept a "row generator" callback
  URL.** Verifier hits a consumer-provided endpoint to fetch rows in
  batches. More general but reinvents the data-fetch surface that SQL
  already gives us for free in the common case.

---

## Sequencing

Items 1–5 are independent and can land in any order. Each is small
and orthogonal to the others.

Item 6 (verifier-sql-checks) is the heaviest. It's also the most
load-bearing for consumers building atomic-staging subgraphs — the
atomic-staging pattern is incomplete without a generic DB-backed
verifier. Recommend treating item 6 as a separate execute-plan after
the design lands; items 1–5 land as one bundle.

A reasonable single brainstorm-to-plan cycle covers all six items
under one spec; the plan can split execution into "ergonomic polish"
(1–5) and "verifier-sql-checks" (6) as two phases.

---

## Open questions for design

1. **Defaults precedence with `userdata_overrides`.** The merge order
   above is `template_defaults → node_userdata → by_executor_overrides
   → by_node_overrides`. Verify with the existing override
   implementation that this order matches what consumers actually want;
   in particular, can template defaults override operator
   `by_executor` overrides? Almost certainly no — operator wins on
   collision — but the order should be spelled out in
   `@blessed-invariant 11` (or wherever userdata-substitution
   invariants live).

2. **Whole-attribute pull and schema validation.** When a property is
   sourced as a whole-object pull, the property's JSON Schema validates
   the result. What's the right behavior when the upstream attribute's
   schema is a strict superset of the receiver's schema? The receiver's
   schema wins — but the bare-form `{{nodes.X.attribute}}` makes it
   easy to write a receiver schema that silently mismatches the
   upstream shape. Worth a docs note.

3. **`source_file:` and the template hash.** If two templates reference
   the same prompt file (deliberate sharing), should they end up with
   the same template hash? Yes if the resolved contents are identical
   — that's what content-addressed buys us. Worth a docs note that
   `source_file:` is a CLI-side concept; the wire-side spec is always
   resolved.

4. **`tags:` in `userdata_overrides`.** Should operator-side
   `userdata_overrides` be able to add/remove tags from nodes at
   instance creation? Probably no — tags are a template-author
   concern, like the executor name. Operators filter on them; they
   don't author them. But it's worth deciding before the migration
   lands.

5. **`verifier-sql-checks` and read replicas.** For very large staging
   tables, verifier queries may want to run against a read replica
   rather than the live primary. Worth supporting `read_replica_dsn`
   on the connection config from day one. Trivial to add; cheap to
   defer.

6. **`rimsky instances apply` and template version skew.** If the
   manifest references `item-ingest@1.0` but the cluster has both
   `1.0` and `2.0` deployed, what happens? Pin to the named version;
   refuse to "upgrade" existing instances to a newer template (that's
   a separate workflow). Spell this out in the CLI's help text and
   error messages.
