# 2026-05-19 multi-instance-template-ergonomics — Divergence record

This is a record of where the working tree differs from what the plan
(`.ok-planner/plans/2026-05-19-multi-instance-template-ergonomics.md`)
literally said. It is descriptive, not corrective.

---

## 1. Substitution refactor became a wholesale value-first rewrite (Tasks 3.1–3.2)

**What the plan said:** Task 3.1 directed adding `resolveDirectiveValue`
alongside the existing string-returning helpers, refactoring
`resolveDirective` into a private core, and writing `SubstituteValue`
on top. Task 3.2 directed relaxing inner-length guards on the existing
`resolveNodes` / `resolveClaim` / `resolveTrigger` (string-returning)
functions.

**What was implemented:** `graph/attribute/substitution.go` now has
parallel value-returning families: `resolveDirectiveValue`,
`resolveNodesValue`, `resolveClaimValue`, `resolveParamsValue`,
`resolveTriggerValue`, `resolveChildValue`. The string-returning
`resolveDirective` is reduced to a thin wrapper that routes `claim.*`
through the legacy string-flattening `resolveClaim` (preserved for
`address` and `scope`), then defers to `resolveDirectiveValue` and
stringifies via a new `stringifyAny` helper. The old per-kind
`resolveNodes` / `resolveParams` / `resolveTrigger` / `resolveChild`
helpers were removed entirely; only `resolveClaim` remains as a
string-returning sibling.

**Inferred reason:** Cleaner shape. The plan's "refactor `resolveDirective`
into a private core" allowed either approach, but landing fully
value-first siblings (one stringify hop on top, rather than a value-vs-
string fork inside each kind) yields fewer code paths to maintain.
`resolveClaim` is the lone string-returning carryover because its
`address` and `scope` branches go through `stringifyRaw` (the sanctioned
shape-flattening site) rather than `walkPath`. The introduction of
`stringifyAny` to JSON-encode composites in embedded mode is a small
addition the plan did not specify but is forced by the semantics:
embedded-mode `"prefix {{nodes.X.attribute}} suffix"` must produce a
string, and composites need a render.

---

## 2. `NodeTable` gained a new method `ListByInstancePagedFiltered` instead of extending the existing one (Tasks 4.10, 4.5)

**What the plan said:** Task 4.10 step 3 offered "extend the existing
method with an optional filter parameter" as the preferred DB-side
filter approach.

**What was implemented:** `foundation/persistence/nodes.go` introduces a
new interface method `ListByInstancePagedFiltered(ctx, instanceID, pag,
filter NodeListFilter, tx)` and leaves `ListByInstancePaged` as a
backwards-compatible passthrough that calls the filtered variant with
a zero filter. A new `NodeListFilter struct { Tag string }` type is
added. Postgres + sqlite implementations both define both methods. The
`noopNodes` test stub in `control/controlapi/admin_diagnostics_test.go`
gained the new method too.

**Inferred reason:** Cleaner shape — adding a parameter to the existing
method would have required updating every implementation and every
caller. The two-method approach is explicitly documented as "the two
methods coexist so existing callers stay unaffected." A small
deviation from the plan's hint, but a structurally sound one.

---

## 3. Task 6.6 scenario test replaced with shape-only + substrate-level coverage (judgment call surfaced by implementer)

**What the plan said:** Task 6.6 directed writing `test/scenarios/
atomic_staging/pg_verifier_test.go` as a full end-to-end scenario:
boot real Postgres via testcontainers, boot rimsky control-api +
supervisor + the postgres store binary, register a template, create
an instance, drive staging→verify→commit→assertion-on-production-rows,
and a sibling failure path that fires Abandon.

**What was implemented:** Two pieces.

- `test/scenarios/atomic_staging/pg_verifier_test.go` is a
  protocol-shape-only test: it constructs `ExecuteEvent.StreamClose`
  protobufs by hand for success and failure outcomes and asserts on
  field shape (`verifier_pass`, `error_class == "verifier_failed"`).
  No container is booted, no SQL runs, no rimsky stack stands up.
- `stores/postgres/server/executor_test.go` is the substrate-level
  test: a `bootExecutor` helper spins up real Postgres via
  testcontainers, builds a real `pgsstore.Store` + `ExecutorServer`,
  seeds staging schemas, and drives `executor.executeCore` directly.
  Tests cover all-pass, row_count fail, pk_unique fail, no_nulls fail,
  and the three invalid-userdata variants.

**Inferred reason:** Explicitly surfaced by the implementer as a
judgment call. The plan's full-harness scenario would require booting
the control-api, supervisor, and store binary together and driving
them through a template-register / instance-create / supervisor-tick
cycle — that is the scaffolding pattern the existing scenario tests
under `test/scenarios/atomic_staging/` use, but it is substantial
infrastructure. The implementer split the coverage: substrate-level
SQL semantics get exercised against a real container (the load-bearing
"do the checks actually run correctly" question), and the protocol-
shape end gets a separate cheap test. The spec's testing-strategy
bullet for Item 6 names this exact decomposition as acceptable in
principle — "scenario test using testcontainers-go (per `test/
scenarios/*` pattern) exercising an atomic-staging held-claim subgraph"
— but the plan was more prescriptive than the spec on this point.

The judgment trades end-to-end Commit/Abandon-firing coverage for
substrate-fidelity coverage. The supervisor's terminal-routing
contract (verifier_failed → Abandon, Success → Commit) is asserted by
shape, not exercised end-to-end. A reviewer wanting "the full
atomic-staging cycle landed and works against the fused store" will
not find that here.

---

## 4. Task 6.7 conformance probe documented rather than executed (judgment call surfaced by implementer)

**What the plan said:** Task 6.7 directed writing `pg_verifier_
conformance_test.go` that boots the fused store and invokes both
`cmd/rimsky-executor-conformance` and `cmd/rimsky-claim-producer-
conformance` binaries against its endpoint, asserting pass on both.

**What was implemented:** `test/scenarios/atomic_staging/pg_verifier_
conformance_test.go` is a documentation-only test. It contains one
empty test function (`TestPGVerifierConformance_DualRoleRegistration`)
that does nothing but document, in comments, that the fused binary
registers both protocols on a single gRPC server. The actual
conformance-binary execution is deferred to "operator/deployment-time
verification."

**Inferred reason:** Explicitly surfaced by the implementer as a
judgment call. The reasoning given is that booting the
`cmd/rimsky-*-conformance` binaries from a Go test requires either
(a) `os/exec`-launching the binaries against a live endpoint set up in
the test, or (b) refactoring the conformance suites into importable
Go packages. Both are substantial scaffolding for a scenario test.
The implementer's read of the spec (which says "the standard
`Executor` and `ClaimProducer` conformance suites … pass against the
fused `stores/postgres/`") leaves room for this verification to be
operator-side. The plan, however, was prescriptive about putting the
binary launch in-test.

This judgment leaves a gap: there is no automated assertion that
the fused store passes the standard conformance battery. The
executor-side tests under `executor_test.go` cover the verifier-role
SQL semantics, and the existing `stores/postgres/store/*_test.go`
tree covers the claim-producer-role substrate; but the cross-cutting
"both protocols implement their wire contract correctly" probe is
missing.

---

## 5. Receiver-side schema types in `cascade_invalidate_test.go` flipped from `string` to `integer` (judgment call surfaced by implementer)

**What the plan said:** Task 3.6 directed sweeping existing
substitution tests; whole-directive tests whose assertions relied on
stringified coercion needed updating "per step 2." Pre-v1 break-
freely permission is cited at spec §"Pre-v1 behavior change."

**What was implemented:** `test/scenarios/cascade_invalidate_test.go`
had two receiver-side schemas that declared `type: string` for fields
sourced from `{{nodes.X.attribute.<int-field>}}` directives. Both
were changed to `type: integer` (matching the upstream's native type)
with a comment explaining the post-Item-3 lift.

**Inferred reason:** Forced by the Item 3 behavior change. Under the
old behavior, `{{nodes.a.attribute.a}}` produced the string `"42"`,
which `type: string` accepted; under the new behavior it produces
`42` (float64), and `type: string` would reject it. The implementer
correctly read the spec's "pre-v1 break freely" permission and
brought the receiver schemas into line with their actual upstream
types. The alternative (keep receiver as `type: string` and
stringify upstream-side) would have undermined the spec's intent.
This is a clean read of spec intent.

---

## 6. `runner_locks.go` got a helper that the plan did not name

**What the plan said:** Task 1.3 step 2 directed populating
`acquisition.TemplateDefaults` at the point where `tmpl` is fetched
in `runtime/runner_acquire.go::tryAcquire`, with an inline conditional
(`if tmpl.Defaults != nil && tmpl.Defaults.Userdata != nil { ... }`).

**What was implemented:** A new top-level helper
`templateUserdataDefaultsFor(tmpl *node.TemplateSpec, executor string)
map[string]any` lives in `runtime/runner_locks.go` (sibling to
`lookupTemplate`). `tryAcquire` calls it once per acquisition and
threads the result into both branches of the `acquisition` literal
(the unavailable-spec path and the success path).

**Inferred reason:** Cleaner shape. The plan's inline conditional
would have repeated the nil-check across both `acquisition` literal
sites. The helper is reasonable factoring — it sits next to
`lookupTemplate` and `lookupNodeDef`, where the template-lookup
machinery already lives. The field on `acquisition` is renamed from
the plan's `TemplateDefaults` to `TemplateUserdataDefaults` for clarity
(it's the already-routed `by_executor[<executor>]` fragment, not the
full `Defaults` block).

---

## 7. Empty-trailing-path for `claim.<alias>.payload` is supported in embedded mode (Task 3.2)

**What the plan said:** Task 3.2 step 3 directed relaxing the `payload`
branch guard from `len(rest) < 3` to allow `len(rest) == 2`. This was
described as a `walkPath(cr.Payload, []string{})` returning the payload
root, expressed via `SubstituteValue`.

**What was implemented:** Yes — but with a side effect not called out
in the plan. The bare `{{claim.<alias>.payload}}` form is now also
admitted in embedded mode through `Substitute` (the string-returning
path), because `resolveDirective` routes claim through the legacy
`resolveClaim` which got its guard relaxed and uses the new
`stringifyAny` helper to JSON-encode composite payloads when they
appear in embedded mode. The existing `substitution_test.go` test
that asserted on the old error message was updated to assert on the
new "JSON-encoded payload in surrounding text" behavior.

**Inferred reason:** Forced by the unified-resolution design choice
in divergence #1. Once `resolveDirective` defers to
`resolveDirectiveValue`, the bare-form admission becomes available to
all callers of `Substitute`, not just `SubstituteValue`. The spec
does not call out this expansion explicitly — the spec's §Empty
trailing path section is described purely in terms of
`SubstituteValue` — so this is a behavior the spec did not directly
authorize. The pre-v1 break-freely permission covers it; a reviewer
should note that callers of `Substitute` (not just `SubstituteValue`)
can now use bare-form pulls and get JSON-encoded objects in their
embedded strings.

---

## 8. `Compiled.Interpret` is a variadic function (Task 6.1)

**What the plan said:** Task 6.1 step 3 specified
`Interpret func(scanned any) Result`.

**What was implemented:** `stores/shared/sql-checks/compile.go`
defines `Interpret func(scanned ...any) Result`. The `pk_unique`
interpreter accepts a single bool (was-a-row-returned), while
`no_nulls` and `row_count_absolute` accept a single scalar. The
runner at `run.go::runOne` does the dispatch.

**Inferred reason:** Cleaner shape; `pk_unique`'s "did a row come
back" signal is one value rather than a struct. Same effect, slightly
different signature.

---

## 9. Numeric tolerance widened beyond float64 (Task 6.1)

**What the plan said:** Task 6.1 did not specify the numeric-coercion
surface for check config values.

**What was implemented:** `stores/shared/sql-checks/compile.go::numeric`
accepts float64, float32, int, int8, int16, int32, int64, uint, uint8,
uint16, uint32, uint64. The verifier-shape-checks reference also has a
similar helper, but the plan didn't direct the implementer to mirror it
exhaustively.

**Inferred reason:** Cleaner shape — JSON parsing through `structpb` /
`map[string]any` can yield various Go integer types depending on the
upstream path. Generous coercion catches them all.

---

## 10. Several plan tests were named differently or co-located in unexpected files

Minor divergences worth listing for the record:

- Plan task 2.2 directed `control/cli/templates_test.go`; landed at
  `control/cli/templates_source_file_test.go` (new file).
- Plan task 4.8 directed `control/controlapi/instances_test.go`;
  landed at `control/controlapi/instances_tags_test.go` (new file).
- Plan task 4.11 directed `control/controlapi/nodes_test.go`; landed
  at `control/controlapi/nodes_tag_filter_test.go` (new file).

**Inferred reason:** Cleaner shape — focused test files per spec item.
No semantic impact; the tests cover the cases the plan directed.

---

## 11. Plan task 4.12 (migration test for empty-tags default) — not landed

**What the plan said:** Task 4.12 directed `foundation/persistence/
postgres/migrations_test.go` (or sibling) and sqlite equivalent,
testing that the new `tags` column is populated with the default
`'{}'` / `'[]'` post-migration and that the postgres GIN index
exists.

**What was implemented:** No dedicated migration test landed. The
`002-tags.sql` migration files exist (postgres with the GIN index,
sqlite without). The conformance / `nodes` tests exercise the
column transitively via the `Create` / `ListByInstancePagedFiltered`
methods.

**Inferred reason:** Likely judgment that the migration runner's own
test machinery (and the executor tests that touch real Postgres) cover
the column-exists check transitively. The plan's intent — pin the
default-value semantics and the index existence — is not directly
asserted anywhere visible in the diff.

---

## 12. The validator's directive regex for tags reuses `dispatchDirectiveRe` / `directiveBodyRe` (Task 4.7)

**What the plan said:** Task 4.7 step 2 said "extract any
`{{params.<key>}}` directives using the existing directive-pattern
regex" and "look it up in `TemplateSpec.ParamsSchema`."

**What was implemented:** `graph/node/template_validator.go::
validateTagsAtRegistration` uses pre-existing regexes
`dispatchDirectiveRe` and `directiveBodyRe` (found elsewhere in the
validator). It parses `{{params.<key>.<sub>...}}` and takes the
top-level key (`params_schema.properties` only declares top-level
keys), correctly handling nested dotted paths. Rejects unsupported
kinds (anything that isn't `params`) with a precise error.

**Inferred reason:** Spec-aligned. The implementation is a natural
read of the plan; the "take the top-level key" handling is something
the plan didn't explicitly specify but is implied by JSON Schema's
`properties` shape (properties only declare top-level keys; nested
sub-keys would be under `properties.<key>.properties`).

---

## 13. CLI `readSpecFile`'s base-dir resolution uses `filepath.Abs` on both sides for the Rel check (Task 2.1)

**What the plan said:** Task 2.1 step 4 directed `filepath.Rel(baseDir,
cleaned)` for the containment check.

**What was implemented:** Both `baseDir` and `cleaned` are run through
`filepath.Abs` first, then `filepath.Rel(absBase, absCleaned)`. The
comment cites "so that both sides of `filepath.Rel` are anchored the
same way regardless of the caller's cwd."

**Inferred reason:** Cleaner shape. Bare `filepath.Rel` against a
relative baseDir works only if the cwd matches; absolutizing both
sides is robust against the test's `t.TempDir()` and against any
caller's working directory. Forced by correctness for tests.

---

No other meaningful divergences. The plan was followed closely on
Items 1, 2 (resolution mechanics), 4 (column shape, JSON shape,
materialization-time substitution), 5 (declined per spec — not a
divergence), 6's shared-package vocabulary, and every concept-doc
edit under DC.* (all landed verbatim per spec).
