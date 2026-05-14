# Verifier-executor convention (quality-rule collapse)

**Date:** 2026-05-13
**Status:** sketch / wishlist
**Companion sketches:** `2026-05-13-blessed-typed-attributes.md`,
`2026-05-13-per-language-executor-sdks.md`

## The critique

Today's `concept:quality-rule` is under-engineered specifically because it's
the wrong shape. Three forcing functions:

1. **Data location.** Evaluators receive `EvalInput.NewData` as `[]map[string]
   any` — JSON-shaped attribute writeback. For attribute-sized data that's
   fine. For dataset-sized data (the data-engineering shape), the data isn't
   in the attribute; it's in a substrate-backed handle. The evaluator either
   needs to resolve the handle itself (recreating the executor model with
   extra steps) or operate only on metadata.
2. **Process boundary.** `eval.Register(name, ev)` is in-process Go. Custom
   evaluators have to be linked into the supervisor binary, and the
   evaluator package is AGPL per `licensing.yml`. This rules out non-Go
   evaluators, proprietary evaluators, evaluators that need their own
   runtime (Spark, Python with pandas/polars), and evaluators that scale
   independently of the supervisor.
3. **Conceptual surface.** "Evaluator" is just "executor that returns
   pass/fail/details for a writeback." There's no architectural reason it
   deserves a separate protocol, registration, registry, or failure-routing
   convention.

The cleaner shape: **verifier nodes**. Full executor protocol. Full
deployment flexibility. Language- and runtime-agnostic. Regular error-
routing through `on_executor_errored`. The held-subgraph semantics already
give the "bad data never reaches production" guarantee that today's commit-
time quality-rule gate provides.

## The shape

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

The verifier executor receives the userdata, resolves the inherited claim
(reads from the upstream's staged data), runs the checks, and returns a
terminal:

- All pass → `Complete{changed: false}` with details in writeback
  (which checks ran, what numbers).
- Any fail with `severity: error` → `Error{error_class: "verifier_failed"}`
  with details.
- Any fail with `severity: warn` → `Complete{changed: false}` with details
  including the warnings.

Error routing through the node's `on_executor_errored` handler. Standard
machinery; no new concept.

Held-subgraph membership preserves the "bad data never reaches production"
guarantee: the verifier inherits the upstream producer's held claim; a
verifier failure forces holding-subgraph Abandon; producer drops the staged
data; production write doesn't fire.

## Bundled verifier executors

The rimsky stdlib ships a small set of bundled verifier executors.

### `verifier-shape-checks`

Covers today's quality-rule builtins plus reasonable extensions:

- `no_nulls(fields)` — every row has non-null values for named fields.
- `nullable_fields_present(fields)` — every row has each named field key.
- `pk_unique(fields)` — primary-key uniqueness over named fields.
- `row_count_ratio(min_ratio)` — new row count vs prior writeback.
- `row_count_absolute(min, max)` — bounds on row count.
- `value_in_set(field, values)` — values are within an allowed set.
- `regex_match(field, pattern)` — values match a regex.
- `numeric_range(field, min, max)` — values within a numeric range.

Operates against blessed typed attributes (`table`, `geo`) and against
substrate-backed claim outputs (resolves the handle, reads, evaluates).
Bundled as a small Go service.

### `verifier-http`

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

The verifier POSTs `body_template` (with claim handles substituted) and
expects a response of shape `{passed: bool, details: string, warnings: [],
errors: []}`. Maps to the protocol terminal accordingly.

This is the bundled answer to "I want to write a domain-specific check in
my consumer's language without forking rimsky-supervisor or worrying about
the in-process Go AGPL licensing."

### Future bundled wrappers

If demand emerges:

- `verifier-great-expectations` — Python service wrapping a GE suite.
  Userdata names the suite; verifier runs GE against the claim address;
  reports back.
- `verifier-soda` — same shape, SodaCL.
- `verifier-deequ` — JVM, PyDeequ as the Python variant.
- `verifier-pandera` — Python.
- `verifier-frictionless` — language-agnostic via Table Schema.

Each is a small standalone service. Ship them as the ecosystem matures and
consumer demand surfaces.

## Template authoring sugar

For ergonomics, allow a `verifiers:` block adjacent to a producing node that
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

Canonicalization expands this into:

- `load-zoning-verify-shape` node with `executor: verifier-shape-checks`,
  `dependencies: [load-zoning]`, claim inheritance from `load-zoning`,
  userdata derived from the `shape:` block.
- `load-zoning-verify-http` node with `executor: verifier-http`, etc.

The canonical template hash is computed over the expanded form, so the
sugar is purely an authoring affordance — the registered template is
fully explicit.

Operators reading the template see a `verifiers:` block right next to the
node it protects; readability matches today's quality-rule declarations
while the underlying mechanism is uniform nodes.

## Severity model

`severity: error | warn`. Same partition as today's `concept:quality-rule`,
but implemented by the verifier executor:

- `severity: error` → verifier returns `Error` on any failure; node routes
  through `on_executor_errored` with `error_class: "verifier_failed"`.
- `severity: warn` → verifier returns `Complete{changed: false}` even on
  failures, with warnings in writeback details. No error routing.

This is the meaningful piece of today's quality-rule severity machinery
that survives the collapse. The "only literal `'warning'` diverts" footgun
(`tension:quality-rule-severity-string-footgun`) goes away — the verifier
executor parses severity at userdata-validation time and either accepts the
two known values or rejects with a clear error.

## Deprecation of in-process Go evaluators

Pre-v1; break cleanly.

- `graph/qualityrule/eval/` builtins move to `executors/verifier-shape-
  checks/` as part of the bundled verifier executor.
- `graph/qualityrule/spec.go` removed.
- `template_node.QualityRules` field removed; replaced by `verifiers:` sugar
  expanding to nodes.
- `eval.Register(name, ev)` removed; consumers needing custom checks use
  `verifier-http` or write their own verifier executor.
- AGPL licensing constraint on `graph/qualityrule/eval/` goes away (the
  package goes away). The bundled `verifier-shape-checks` executor stays
  Apache-licensed (it's the spec types only; eval code becomes an Apache
  executor binary that interprets userdata).
- `quality_rule_failed` event becomes `verifier_failed` (or just rolls into
  the generic `executor_errored` event with `error_class: "verifier_failed"`
  — discuss).

The CHANGELOG entry for this is substantial; mention dev-DB nuke if any
schema changes ride along.

## Cross-node verifiers

Future capability: a verifier that reads from multiple upstream claims and
checks invariants across them.

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

The verifier resolves both claim addresses and evaluates the cross-table
invariant. This is the rimsky-native answer to "cross-table coverage
ratios," "join completeness checks," "referential integrity across two
upstream tables."

No new machinery needed — it's just an executor with two inheritances.
Worth documenting as a pattern in the worked example.

## Open design questions

1. **What does the verifier write back as its attribute value?** Today
   quality-rule failures emit `quality_rule_failed` events; the node's
   value is the writeback. For verifiers-as-nodes, the writeback should
   probably be a summary (which checks ran, results, durations) — useful
   for downstream substitution and for operator inspection. Schema for the
   summary needs design.
2. **Severity granularity.** Today's `severity: warn | error` is coarse.
   Some real-world cases want "error blocks promote, warn produces a row
   in a quality report, info logs only." Pragmatic: stay coarse for v1;
   add granularity if demand emerges.
3. **Quality-report aggregation.** Multiple verifiers across a graph each
   write a summary. Operators want a roll-up: "the Phoenix instance has 12
   verifier nodes; 11 passed, 1 has warnings, none failed." This is a
   dashboard / `concept:operational-health` concern more than a verifier
   concern. Track separately.
4. **What about today's `null_pct` / `unique_pct` / statistical checks?**
   These show up in mature data-quality libraries (GE, Soda, Deequ) and
   aren't in the proposed `verifier-shape-checks` set. Probably add to
   `verifier-shape-checks` as it matures; or punt to the wrapper bundled
   executors for those libraries.

## Phasing

**Phase 1**: design lockdown.
- Verifier convention documented in `docs/concepts/`.
- Template authoring sugar (`verifiers:` block) and canonicalization rules.
- Deprecation plan for `graph/qualityrule/`.

**Phase 2**: `verifier-shape-checks` bundled executor.
- Covers today's three builtins plus the reasonable extensions.
- Operates against blessed typed attributes and claim-backed substrates.
- Conformance against `cmd:rimsky-executor-conformance` in stub mode.

**Phase 3**: `verifier-http` bundled executor.
- Generic HTTP delegation surface.
- Reference docs and example consumer-side endpoint shape.

**Phase 4**: deprecation cutover.
- Remove `graph/qualityrule/` after both bundled executors are stable.
- Update templates in `docs/agents/examples/` to use the new shape.
- CHANGELOG.

**Phase 5** (deferred): library wrappers (GE, Soda, etc.) as demand emerges.
