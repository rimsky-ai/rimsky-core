# Userdata collapse into attributes — Implementation Plan

**Spec:** `.ok-planner/specs/2026-05-20-userdata-collapse-into-attributes-design.md`

**Goal:** Retire `userdata` as a distinct concept in rimsky. Collapse its job into `attributes` so each schema property is one of three shapes (source-bound, static-default, executor-written), with a unified four-layer override merge. Relax the attribute-source grammar to admit embedded text + multiple directives. Add a per-directive `?` marker for lenient-on-missing (default is strict). Update the wire protocol, persistence schema, runtime dispatch, validator, the claude-agent executor, and the design docs to match. Pre-v1 break-freely; no migration shim.

**Architecture:** Pre-v1 destructive change. Wire-protocol surfaces drop their userdata fields entirely. The `rimsky_instances.userdata_overrides` column drops and `attribute_overrides` recreates in its place. The substitution engine extends to recognize the `?` marker. The template-validator relaxes one rule (admit embedded text + multi-directive sources) and gains one new rule (`checkAttributesSchema`: every property has source/default/`readOnly`). The runtime's `applyUserdataOverrides` becomes `applyAttributeOverrides` and merges only L3 + L4 at runtime (L1 folds into the effective schema at registration). The claude-agent executor retires `renderTemplate` and instead reads source-bound prompts verbatim then appends a fixed metadata footer for executor-private vars. Five concept docs and one tension move as part of the same change.

**Tech Stack:** Go (stdlib + `github.com/jackc/pgx/v5` + `modernc.org/sqlite` + `google.golang.org/protobuf`), Markdown (concept doc updates), Protobuf (field removal + rename via `reserved` directives), TypeScript (claude-agent executor).

---

## Context for the implementer

You're picking up a finished design. The spec at `.ok-planner/specs/2026-05-20-userdata-collapse-into-attributes-design.md` is the source of truth for **what to build and why**. This plan translates it into mechanical tasks; read the spec before starting if anything below is ambiguous.

**Working directory:** `/Users/patrick/Documents/projects/research/zonebase/submodules/rimsky/` (the rimsky submodule root). All paths in this plan are relative to that root unless otherwise specified.

**Build order matters.** Migrations (Tasks 1–2) land before persistence-impl (Tasks 7–10) so the SQL schema exists when Go code references the renamed column. Proto changes (Tasks 3–5) regenerate (Task 6) before any Go that consumes the renamed/removed fields (Tasks 7+). The substitution-engine change (Task 12) and validator changes (Tasks 13–16) land before the runtime callers (Task 17) that depend on them. Concept-doc mutations and cross-cutting annotation sweeps interleave with the code changes — concept docs change as one unit with the code, per `.ok-planner/CLAUDE.md`.

**Pre-v1 — break freely.** Per `.claude/rules/rules.md`, destructive migrations, proto field removals, and breaking-shape changes are permitted. No backwards-compatibility shims. No "deprecated path" parsing.

**Mandatory verifications per project rules (`.claude/rules/rules.md`):**
- Any Go change: `go build ./... && go test ./... && make lint`.
- Proto changes: `make proto-gen` first, then the Go checks above.
- Scenario or storage changes: `go test ./test/scenarios/... ./foundation/persistence/... -count=1` (testcontainers — Docker must be running).
- Race-sensitive paths (runtime, persistence, scheduler): add `-race -count=3` to the runtime / persistence / scheduler test runs.
- TypeScript executor: `cd executors/claude-agent && npm install && npm test && npm run build`.

These checks land at the task boundaries where they make sense; a final Task 39 runs the full battery.

**Cold-read conventions** (`.claude/rules/cold-read-cheatsheet.md`): one feature per file, max 2 directory nesting, tests co-located, ~500-line file / ~100-line function guidelines, explicit parameters over DI, logging via stdlib `log/slog` only.

**Citation grammar in this plan:** the plan uses standard repository paths. `file:path` and `code:path::Symbol` references inside the plan body are addressed to the implementer agent for navigation.

---

## File map

Files this plan creates, modifies, renames, or deletes:

```
NEW migrations:
  foundation/persistence/postgres/migrations/005-attribute-overrides-rename.sql
  foundation/persistence/sqlite/migrations/005-attribute-overrides-rename.sql

MODIFIED proto:
  protocols/proto/v1/executor.proto
  protocols/proto/v1/executor_observability.proto
  protocols/proto/v1/validation.proto
REGENERATED:
  protocols/proto/v1/gen/executor.pb.go
  protocols/proto/v1/gen/executor_observability.pb.go
  protocols/proto/v1/gen/executor_observability_grpc.pb.go
  protocols/proto/v1/gen/validation.pb.go
  protocols/proto/v1/gen/validation_grpc.pb.go

MODIFIED foundation/persistence:
  foundation/persistence/instances.go                                 (InstanceRow + InstanceCreateInput rename)
  foundation/persistence/postgres/instances.go                        (SQL rename)
  foundation/persistence/sqlite/instances.go                          (SQL rename)
  foundation/persistence/conformance/instances_attribute_overrides.go (file rename + content rename)
                                                                      (formerly instances_userdata_overrides.go)

MODIFIED foundation/spec:
  foundation/spec/template.go                                         (NodeDef.Userdata + TemplateDefaults.Userdata retire;
                                                                       TemplateDefaults.Attributes introduces)

MODIFIED graph:
  graph/attribute/substitution.go                                     (?-marker in resolveDirectiveValue)
  graph/attribute/substitution_test.go                                (new ?-marker tests)
  graph/node/template_validator.go                                    (relaxed checkAttributeSource;
                                                                       new checkAttributesSchema;
                                                                       retire validateUserdataAgainstSchema)
  graph/node/template_validator_test.go                               (new tests for the above)
  graph/node/template_validator_graphs.go                             (sweep userdata references)
  graph/node/subscription_edges.go                                    (already handles multi-directive; touch nothing
                                                                       unless test sweep surfaces an issue)

MODIFIED runtime:
RENAMED:
  runtime/userdata_overrides.go             → runtime/attribute_overrides.go
  runtime/userdata_overrides_test.go        → runtime/attribute_overrides_test.go
MODIFIED:
  runtime/runner.go                         (RunArgs.UserdataValidator retires; invariant-11 cite removed)
  runtime/runner_acquire.go                 (MergedUserdata→MergedAttributes; cites swept)
  runtime/runner_acquire_helpers.go         (sweep userdata mentions if any remain)
  runtime/runner_dispatch.go                (substituteAttributesSchema emits static defaults;
                                             buildExecuteRequest drops the userdata block)
  runtime/lineage_writer.go                 (rename merged-userdata-hash references)
NEW:
  runtime/runner_dispatch_test.go           (substituteAttributesSchema with statics + L3/L4 + embedded sources)

RETIRED:
  control/observability/userdata_validator.go       (file deleted)
  control/observability/userdata_validator_test.go  (if present — deleted alongside)
MODIFIED:
  control/config/supervisor.go              (UserdataValidator field retires)
  cmd/rimsky-supervisor/main.go             (wiring retires)
  control/controlapi/userdata_overrides.go  → control/controlapi/attribute_overrides.go (rename + content)
  control/controlapi/instances.go           (request/response shape: userdata_overrides → attribute_overrides)

MODIFIED executor (claude-agent):
RENAMED:
  executors/claude-agent/src/userdata-schema.ts → executors/claude-agent/src/expected-attributes-schema.ts
MODIFIED:
  executors/claude-agent/src/agent-run.ts                            (renderTemplate retires; metadata footer appended)
  executors/claude-agent/src/agent-run.test.ts                       (renderTemplate tests delete; new footer tests)
  executors/claude-agent/src/server.ts                               (reads attributes, not userdata)
  executors/claude-agent/src/http-bridge.ts                          (same)
  executors/claude-agent/src/index.ts                                (drop renderTemplate export if present)
  executors/claude-agent/src/server.test.ts                          (update fixtures)
  executors/claude-agent/src/http-bridge.test.ts                     (update fixtures)
  executors/claude-agent/src/lifecycle.e2e.test.ts                   (update fixtures)
  executors/claude-agent/src/attributes-tools.test.ts                (update fixtures if applicable)
RENAMED doc:
  docs/executors/claude-agent/userdata.md → docs/executors/claude-agent/expected-attributes.md

MODIFIED other in-tree executors:
  executors/http-node/server.go                                       (req.GetUserdata() → req.GetAttributes())
  executors/http-node/observability.go                                (advertised schema field rename)
  executors/stub/* (if any references)
  executors/verifier-shape-checks/server.go                           (line 111 read site)
  executors/verifier-shape-checks/validation.go                       (line 84 GetUserdata call)
  executors/verifier-http/*                                           (sweep)

NEW scenario tests:
  test/scenarios/userdata_collapse/static_attributes_test.go
  test/scenarios/userdata_collapse/embedded_source_test.go
  test/scenarios/userdata_collapse/z_pattern_producer_recovery_test.go

MODIFIED concept docs (under .ok-planner/design/):
MOVED:
  .ok-planner/design/concepts/userdata.md → .ok-planner/design/concepts/_retired/userdata.md (with new ## Retirement section)
  .ok-planner/design/tensions/userdata-schema-as-opacity-exception.md
    → .ok-planner/design/tensions/_resolved/userdata-schema-as-opacity-exception.md (with new ## Resolution section)
MODIFIED:
  .ok-planner/design/concepts.md                                      (TOC: remove userdata row; update attribute row)
  .ok-planner/design/concepts/attribute.md                            (substantial updates per spec)
  .ok-planner/design/concepts/inertness.md                            (drop userdata from streams + invariants)
  .ok-planner/design/concepts/instance.md                             (userdata_overrides → attribute_overrides;
                                                                       new ## Notes section)
  .ok-planner/design/concepts/validation.md                           (section updates per spec)
  CHANGELOG.md                                                        (append entry)
```

---

## Task 1 — Postgres migration: rename `rimsky_instances.userdata_overrides` to `attribute_overrides`

**Files:** `foundation/persistence/postgres/migrations/005-attribute-overrides-rename.sql` (new).

### Step 1.1 — Inspect existing migration convention

Read `foundation/persistence/postgres/migrations/004-wait-set-drained-at.sql` to confirm the convention is zero-padded number prefix + descriptive slug. Read `foundation/persistence/postgres/migrations/001-baseline.sql` to find the `rimsky_instances` table definition; locate the `userdata_overrides` column.

### Step 1.2 — Write the migration file

Create `foundation/persistence/postgres/migrations/005-attribute-overrides-rename.sql`:

```sql
-- =====  rimsky_instances.userdata_overrides → attribute_overrides  =====
-- Pre-v1 destructive rename: the collapse of userdata into attributes
-- moves the per-instance override surface from userdata to attribute.
-- Per spec
-- .ok-planner/specs/2026-05-20-userdata-collapse-into-attributes-design.md
-- §"Persistence".
ALTER TABLE rimsky_instances
    RENAME COLUMN userdata_overrides TO attribute_overrides;
```

The `JSONB NOT NULL DEFAULT '{}'::jsonb` constraints carry through the rename — no need to redeclare.

### Step 1.3 — Verify

```
go test ./foundation/persistence/postgres/... -count=1
```

Expect: Go compile errors in downstream callers are likely at this stage and acceptable until the persistence accessors (Task 7+) update. The migration SQL itself must be valid (no SQL syntax errors from `migrator.Up()`).

---

## Task 2 — SQLite migration: same rename

**Files:** `foundation/persistence/sqlite/migrations/005-attribute-overrides-rename.sql` (new).

### Step 2.1 — Write the SQLite migration

```sql
-- =====  rimsky_instances.userdata_overrides → attribute_overrides  =====
-- Per spec
-- .ok-planner/specs/2026-05-20-userdata-collapse-into-attributes-design.md
-- §"Persistence".
ALTER TABLE rimsky_instances
    RENAME COLUMN userdata_overrides TO attribute_overrides;
```

SQLite supports `ALTER TABLE … RENAME COLUMN` since version 3.25. The pure-Go `modernc.org/sqlite` driver used by rimsky bundles a sufficiently recent SQLite version.

### Step 2.2 — Verify

```
go test ./foundation/persistence/sqlite/... -count=1
```

Same acceptance bar as Task 1 — SQL valid; Go callers may not compile yet.

---

## Task 3 — Proto changes: remove `ExecuteRequest.userdata`

**Files:** `protocols/proto/v1/executor.proto`.

### Step 3.1 — Inspect current field

Find the `userdata` field on the `ExecuteRequest` message. Note the field number for the `reserved` directive (so the wire field number can't be silently reused for a different field later).

### Step 3.2 — Remove the field

Delete the `google.protobuf.Struct userdata = <N>;` line on `ExecuteRequest`. Add a `reserved <N>;` and `reserved "userdata";` directive immediately below the surviving field list (matching the convention used elsewhere in this file for retired fields). Update any neighboring comment that mentions userdata as part of the dispatch envelope.

### Step 3.3 — Skip regenerate for now

Do not run `make proto-gen` yet — Tasks 4 and 5 also touch proto files; regenerate once after all three. Verification falls to Task 6.

---

## Task 4 — Proto changes: rename `Capabilities.userdata_schema` → `expected_attributes_schema`

**Files:** `protocols/proto/v1/executor_observability.proto`.

### Step 4.1 — Locate the field

The field is `bytes userdata_schema = 6;` on the `ObservabilityCapabilities` message (around line 44 in the current file). The comment block above it (lines 36–43 or so) describes the field's purpose.

### Step 4.2 — Rename the field

Change `bytes userdata_schema = 6;` to `bytes expected_attributes_schema = 6;`. (The proto field number stays the same — no `reserved` directive needed since this is a rename rather than a removal of an in-use field number.)

Update the doc comment block above the field to describe the unified attribute role: the bytes are the executor's advertised JSON Schema describing the attribute set the executor expects on dispatch (input properties) and writes back at commit (output properties marked `readOnly: true`). The exact wording is the implementer's call; preserve the existing structure (purpose, semantics, exception-discipline note) and substitute "attributes" for "userdata" throughout.

### Step 4.3 — Verify via Task 6

Same as Task 3 — defer `make proto-gen` to Task 6.

---

## Task 5 — Proto changes: remove `ExecutorContext.userdata`

**Files:** `protocols/proto/v1/validation.proto`.

### Step 5.1 — Locate the field

Around line 45 in the file: `bytes userdata = 2;` on the `ExecutorContext` message. The surrounding comment block (around line 40) explains its role. Line 105 carries a field-path example `/executor/userdata/some_field` in the `FieldRef` doc.

### Step 5.2 — Remove the field

Delete the `bytes userdata = 2;` line. Add a `reserved 2;` and `reserved "userdata";` directive in the message body, matching the file's existing reserved-directive convention.

Update the comment block at line 40: replace the userdata-bytes framing with attribute-bag framing (the pipeline now operates against the merged effective attribute set). The author's call on exact wording; preserve the surrounding "validation pipeline input" framing.

Update the field-path example at line 105: change `/executor/userdata/some_field` to `/executor/attributes/some_field`.

### Step 5.3 — Verify via Task 6

Defer regenerate to Task 6.

---

## Task 6 — Regenerate proto-gen Go

**Files (generated):** `protocols/proto/v1/gen/*.pb.go`.

### Step 6.1 — Run proto-gen

```
make proto-gen
```

This regenerates the generated Go from Tasks 3, 4, and 5. After regeneration, the codebase will have a transient compile failure: callers of `req.GetUserdata()` and `cap.GetUserdataSchema()` no longer have those methods. Tasks 7+ fix the callers.

### Step 6.2 — Verify the generated files

```
git status protocols/proto/v1/gen/
```

Expect `executor.pb.go`, `executor_observability.pb.go`, `executor_observability_grpc.pb.go`, `validation.pb.go`, and `validation_grpc.pb.go` to show as modified. The new method set on `ObservabilityCapabilities` should include `GetExpectedAttributesSchema()` rather than `GetUserdataSchema()`. The `ExecuteRequest` should no longer have a `Userdata` field or `GetUserdata()` method. The `ExecutorContext` should no longer have a `Userdata` field.

Compile check at this stage will fail — that's expected. Acceptance bar: `make proto-gen` succeeds without error.

---

## Task 7 — Foundation persistence: rename `InstanceRow.UserdataOverrides`

**Files:** `foundation/persistence/instances.go`.

### Step 7.1 — Rename the field

In `code:foundation/persistence/instances.go`, locate the `InstanceRow` struct (around line 28). Rename the `UserdataOverrides map[string]any` field to `AttributeOverrides map[string]any`. Change its JSON tag from `json:"userdata_overrides"` to `json:"attribute_overrides"`.

Update the doc comment above the field (lines 17–23). The current comment cites `@blessed-invariant 11`; remove that reference and rewrite to describe attribute-overrides semantics. Suggested rewording (the implementer's call on exact text):

```go
// AttributeOverrides carries optional per-instance JSON overrides that
// rimsky deep-merges into per-node attributes at dispatch time. Shape is
// validated at instance-create by the control-api but the contents are
// inert at dispatch (covered by concept:inertness structural-inertness
// discipline). Empty map = no overrides; the column has
// NOT NULL DEFAULT '{}' so dispatch-time reads are unconditional.
```

### Step 7.2 — Rename in `InstanceCreateInput`

Same file, find the `InstanceCreateInput` struct (around line 58+). Rename its `UserdataOverrides` field to `AttributeOverrides`. Update the doc comment (around line 60–63).

### Step 7.3 — Sweep other type references in the same file

Any helper functions, interfaces, or comments referencing `UserdataOverrides` in `foundation/persistence/instances.go` rename accordingly.

### Step 7.4 — Verify

Local compile check:

```
go build ./foundation/persistence/...
```

Expect callers in `postgres/` and `sqlite/` accessors (and conformance) to fail compile until Tasks 8–10. The accessor failures are expected; compile failures elsewhere are a signal that something else references `UserdataOverrides` (sweep them at Task 25).

---

## Task 8 — Postgres persistence accessor: update SQL queries

**Files:** `foundation/persistence/postgres/instances.go`.

### Step 8.1 — Locate SQL queries

Grep within the file for `userdata_overrides` (the column name in SQL) and `UserdataOverrides` (the Go struct field). Replace all SQL occurrences of `userdata_overrides` with `attribute_overrides` (the column rename from Task 1). Replace Go struct field references with `AttributeOverrides` (per Task 7).

### Step 8.2 — Verify

```
go build ./foundation/persistence/postgres/...
```

If `pgtest` integration tests exist that touch the column, they should compile. Run them:

```
go test ./foundation/persistence/postgres/... -count=1
```

Tests should pass — the SQL column rename (Task 1) and Go field rename align after this task.

---

## Task 9 — SQLite persistence accessor: same updates

**Files:** `foundation/persistence/sqlite/instances.go`.

### Step 9.1 — Mirror Task 8

Same SQL column rename + Go struct field rename, applied to the SQLite accessor.

### Step 9.2 — Verify

```
go test ./foundation/persistence/sqlite/... -count=1
```

---

## Task 10 — Conformance file rename + content updates

**Files:**
- Old: `foundation/persistence/conformance/instances_userdata_overrides.go`
- New: `foundation/persistence/conformance/instances_attribute_overrides.go`

### Step 10.1 — Rename the file

```
git mv foundation/persistence/conformance/instances_userdata_overrides.go foundation/persistence/conformance/instances_attribute_overrides.go
```

### Step 10.2 — Update file contents

Open the renamed file. Sweep for occurrences of `userdata_overrides` (SQL/JSON name), `UserdataOverrides` (Go type field), and `userdata` (prose). Replace with their attribute counterparts. Test function names that contain `UserdataOverrides` rename to `AttributeOverrides`.

### Step 10.3 — Update any conformance-test runner that references the file

Grep `foundation/persistence/conformance/conformance.go` for `userdata` references. The conformance harness usually registers a list of test functions; rename any function-table entries.

### Step 10.4 — Verify

```
go test ./foundation/persistence/... -count=1
```

---

## Task 11 — Foundation spec: retire `NodeDef.Userdata` and `TemplateDefaults.Userdata`; introduce `TemplateDefaults.Attributes`

**Files:** `foundation/spec/template.go`.

### Step 11.1 — Remove `TemplateNodeDef.Userdata`

Locate the `TemplateNodeDef` struct (declared at line 97 of `foundation/spec/template.go`). At line 114, remove the field:

```go
Userdata   map[string]any             `yaml:"userdata,omitempty" json:"userdata,omitempty"`
```

(There is no `@concept: userdata` annotation directly above this field. The three `@concept: userdata` annotations in this file are at lines 53, 70, and 80 — all on `TemplateDefaults` / `TemplateUserdataDefaults` — handled by Steps 11.2 and 11.3 below.)

### Step 11.2 — Remove `TemplateDefaults.Userdata` field

Locate the `TemplateDefaults` struct (around line 47–72). Remove the field at line 72:

```go
Userdata *TemplateUserdataDefaults `yaml:"userdata,omitempty" json:"userdata,omitempty"`
```

And remove the `TemplateUserdataDefaults` type definition (the struct from line 75 through line 82, including the `@concept: userdata` annotation at line 80).

### Step 11.3 — Introduce `TemplateDefaults.Attributes`

Add a new field on `TemplateDefaults`:

```go
// Attributes holds template-author attribute-value baselines merged
// into per-node effective schemas at registration. See concept:attribute
// for the four-layer override model.
//
// Shape: by_executor.<exec> → attribute-name → default value (any JSON).
//
// @concept: attribute
Attributes *TemplateAttributeDefaults `yaml:"attributes,omitempty" json:"attributes,omitempty"`
```

And introduce the type:

```go
// TemplateAttributeDefaults carries per-executor attribute-value
// baselines. These contribute `default:` values to the effective
// schema at template registration (L1 in the override merge); per-node
// declarations (L2) override these where they conflict.
//
// @concept: attribute
type TemplateAttributeDefaults struct {
    ByExecutor map[string]map[string]any `yaml:"by_executor,omitempty" json:"by_executor,omitempty"`
}
```

### Step 11.4 — Remove `@blessed-invariant 11` cite

Locate the `@blessed-invariant 11` reference around line 67 (in the `TemplateDefaults` comment block). Remove the line. If the surrounding comment becomes incoherent, reword tersely.

### Step 11.5 — Verify

```
go build ./foundation/spec/...
```

Compile failures in downstream callers (`runtime/runner_dispatch.go`, `graph/node/template_validator.go`, etc.) are expected at this stage. They're addressed in later tasks.

---

## Task 12 — Substitution engine: add `?` marker parsing

**Files:** `graph/attribute/substitution.go`.

### Step 12.1 — Inspect the existing fallback parser

Find `resolveDirectiveValue` (around line 285 of `graph/attribute/substitution.go`). Its current logic:
- Receives a directive body (without `{{}}` braces).
- Checks for `| <literal>` fallback. If present, splits the body into directive + literal; rejects multi-pipe chains.
- Resolves the directive proper. On `ErrMissingSource`, returns the fallback literal (if present) or propagates the error.

### Step 12.2 — Extend with `?` parsing

Add `?` marker parsing **before** the fallback parsing (since `?` and `|` are mutually exclusive, but `?` would appear before `|` in the rare case the validator missed the rejection):

```go
// Parse the optional `?` marker. Strict-default is the contract: a
// missing directive without `?` raises ErrMissingSource. With `?`,
// missing resolves to JSON null. Mutually exclusive with the
// `| <literal>` fallback (rejected at registration).
isLenient := false
if strings.HasSuffix(strings.TrimSpace(directive), "?") {
    isLenient = true
    directive = strings.TrimSuffix(strings.TrimSpace(directive), "?")
}
```

Modify the `ErrMissingSource` return path: if `isLenient` is true, return `nil, nil` (representing JSON `null`) instead of `nil, &ErrMissingSource{...}`.

Defensively: if both `?` and `|` appear in a directive body, the validator should have rejected it. But to be safe, the runtime path: if the directive body still contains `|` after `?`-stripping, prefer the fallback parsing (existing path). The combination is already rejected at registration, so this code path is unreachable in practice — keep the simpler form.

### Step 12.3 — Update the directive-parse helper used by embedded mode

`Substitute` calls `resolveDirective` (the string-returning sibling). That function also needs `?` awareness so embedded sources work correctly. Find `resolveDirective` (around line 264). Either:
- (a) Apply the same `?` parsing at the start of `resolveDirective`, swallowing missing into an empty string for embedded mode; or
- (b) Delegate to `resolveDirectiveValue` and stringify the result (preferred if simpler).

For embedded mode with a lenient `?` marker that resolves to null, the embedded result should be the empty string (matching how composite null lifts work; the spec is silent on this but empty-string is the natural choice for embedded text rendering — null in the middle of a string doesn't make sense).

Suggested implementation: extract the `?`-parsing into a small helper (`parseLenientMarker(body) (lenient bool, stripped string)`) used by both `resolveDirective` and `resolveDirectiveValue`. Keep one place for the parsing.

### Step 12.4 — Verify (build only — tests in Task 26)

```
go build ./graph/attribute/...
```

Local-package build must pass. Downstream test failures are deferred to Task 26.

---

## Task 13 — Template validator: relax `checkAttributeSource`

**Files:** `graph/node/template_validator.go`.

### Step 13.1 — Inspect the current validation

Find `checkAttributeSource` (around line 928 of `graph/node/template_validator.go`). The current rejection logic at lines 936–950 enforces "exactly one `{{...}}` directive with no surrounding text." That rejection is what relaxes.

### Step 13.2 — Replace the directive-count + surrounding-text checks

The new rule: the source must contain at least one `{{...}}` directive; literal text alongside is fine; multiple directives in one source are fine.

Replace the existing block:

```go
matches := dispatchDirectiveRe.FindAllStringSubmatchIndex(trimmed, -1)
if len(matches) != 1 {
    res.Errors = append(res.Errors, ValidationError{
        Path: path,
        Msg:  fmt.Sprintf("source must be exactly one {{...}} directive, got %q", trimmed),
    })
    return
}
m := matches[0]
if m[0] != 0 || m[1] != len(trimmed) {
    res.Errors = append(res.Errors, ValidationError{
        Path: path,
        Msg:  fmt.Sprintf("source must be exactly one {{...}} directive with no surrounding text, got %q", trimmed),
    })
    return
}
```

With:

```go
matches := dispatchDirectiveRe.FindAllStringSubmatchIndex(trimmed, -1)
if len(matches) == 0 {
    res.Errors = append(res.Errors, ValidationError{
        Path: path,
        Msg:  fmt.Sprintf("source must contain at least one {{...}} directive, got %q", trimmed),
    })
    return
}
```

### Step 13.3 — Loop over all directives

The remaining validation (parsing the body, validating the source kind, parsing the fallback literal) currently runs once on the single directive. Wrap that logic in a loop over each `dispatchDirectiveRe.FindAllStringSubmatchIndex` match. For each directive body, validate it as a self-contained substitution directive (kind prefix, optional `?` marker, optional `| literal` fallback).

### Step 13.4 — Add `?` marker parsing + mutual-exclusion check

For each directive body (after stripping the outer `{{}}`), the validator's per-directive logic:

1. Trim whitespace.
2. Check for `?` suffix. If present, strip it; remember `hasLenientMarker = true`.
3. Check for `| <literal>` fallback. If present, validate the literal (existing logic) and strip it; remember `hasFallbackLiteral = true`.
4. If both `hasLenientMarker` and `hasFallbackLiteral` were true, append a ValidationError:

```go
res.Errors = append(res.Errors, ValidationError{
    Path: path,
    Msg:  fmt.Sprintf("source directive %q has both `?` marker and `| <literal>` fallback — pick one (incoherent: `?` says null on missing, `|` says literal on missing)", originalBody),
})
return
```

5. Validate the remaining directive body per existing per-source-kind rules (`directiveBodyRe`, the per-kind parsing for `claim` / `params` / `nodes` / `trigger` / `child`).

### Step 13.5 — Reject array-form multi-source (existing decline, keep clear)

If `srcRaw` (the value of the `source:` field) is an array rather than a string, reject. Find the caller that passes `src` into `checkAttributeSource`. In the surrounding context (`graph/node/template_validator.go` around line 846), the type-assert `srcRaw.(string)` already handles this case (the `!ok` branch returns "source must be a string"). Verify the error message is still clear; tighten if needed.

### Step 13.6 — Verify (build only)

```
go build ./graph/node/...
```

Tests covered in Task 26.

---

## Task 14 — Template validator: add `checkAttributesSchema`

**Files:** `graph/node/template_validator.go`.

### Step 14.0 — Rename the registry hook that exposes executor schema bytes

Before `checkAttributesSchema` can read executor schemas, the existing hook needs renaming for the new advertised field name:

- In `graph/node/template_validator.go` around line 91-95, rename the `RegistryHooks.ExecutorUserdataSchema` field to `RegistryHooks.ExecutorExpectedAttributesSchema`. Update the doc comment block above it (lines 91-94) to describe the renamed advertised field (`Capabilities.expected_attributes_schema` rather than `Capabilities.userdata_schema`).
- Sweep callers across the repo:

```
rg 'ExecutorUserdataSchema' .
```

The known wiring site is in the supervisor's `RegistryHooks` construction (likely in `cmd/rimsky-supervisor/main.go` or `control/config/supervisor.go`). Update those call sites — the closure should now read from the executor's `expected_attributes_schema` capability (the proto rename from Task 4 already made that the live field).

The hook's existing in-tree wiring site is `code:control/controlapi/templates.go:125` (where the closure that reads from the discovery cache is bound onto `hooks.ExecutorUserdataSchema`); its existing in-tree caller is `validateUserdataAgainstSchema` (retiring in Task 16). The new caller is `checkAttributesSchema` from Step 14.1 below. Rename both the field and the wire-up site.

### Step 14.1 — Add the new validator function

Add a new function `checkAttributesSchema` that runs on the per-node *effective* schema (after the L1 merge from Task 15 lands; for now, write it against the per-node `n.Attributes.Schema` and adjust in Task 15 if needed). Place it after `checkAttributeSource`.

```go
// checkAttributesSchema enforces the unified-attribute-surface
// invariant: each property must satisfy one of (a) has `source:`,
// (b) has `default:`, or (c) is marked `readOnly: true` in the
// executor's expected_attributes_schema (executor-write-back
// populates at commit). Properties with both `source:` and
// `default:` are also rejected.
//
// Per spec
// .ok-planner/specs/2026-05-20-userdata-collapse-into-attributes-design.md
// §"Attribute as the unified surface".
//
// @concept: attribute
func checkAttributesSchema(schema map[string]any, executorReadOnlyProps map[string]bool, sbase string, res *ValidationResult) {
    if schema == nil {
        return
    }
    props, ok := schema["properties"].(map[string]any)
    if !ok {
        return
    }
    for name, raw := range props {
        prop, ok := raw.(map[string]any)
        if !ok {
            continue
        }
        _, hasSource := prop["source"]
        _, hasDefault := prop["default"]
        execReadOnly := executorReadOnlyProps[name]
        if hasSource && hasDefault {
            res.Errors = append(res.Errors, ValidationError{
                Path: fmt.Sprintf("%s.properties.%s", sbase, name),
                Msg:  "property declares both `source:` and `default:` — pick one",
            })
            continue
        }
        if !hasSource && !hasDefault && !execReadOnly {
            res.Errors = append(res.Errors, ValidationError{
                Path: fmt.Sprintf("%s.properties.%s", sbase, name),
                Msg:  "property has no `source:`, no `default:`, and is not marked `readOnly: true` in the executor's expected_attributes_schema — declare one of these or the property is unpopulated at dispatch",
            })
        }
    }
}
```

### Step 14.2 — Compute `executorReadOnlyProps` from the executor's schema

The validator needs to know which property names the executor has marked `readOnly: true`. The executor's `expected_attributes_schema` (proto field renamed in Task 4) is available to the template validator via the existing `RegistryHooks` mechanism. Find where the validator currently fetches executor capabilities (likely a `RegistryHooks.ExecutorCapabilities(name)` callback or similar).

After Step 14.0, the hook `RegistryHooks.ExecutorExpectedAttributesSchema` returns the executor's schema bytes. Parse the JSON Schema and extract every property where `readOnly: true`. Build a `map[string]bool`. Pass that into `checkAttributesSchema` per node.

Add a helper in this file:

```go
// extractReadOnlyProps unmarshals the executor's
// expected_attributes_schema bytes into a JSON Schema map and returns
// the set of top-level property names with `readOnly: true`. Returns
// an empty map on parse failure (the schema-validity check at line
// 1238 surfaces parse errors elsewhere; here we degrade gracefully).
func extractReadOnlyProps(schemaBytes []byte) map[string]bool {
    if len(schemaBytes) == 0 {
        return map[string]bool{}
    }
    var schema map[string]any
    if err := json.Unmarshal(schemaBytes, &schema); err != nil {
        return map[string]bool{}
    }
    out := map[string]bool{}
    props, ok := schema["properties"].(map[string]any)
    if !ok {
        return out
    }
    for name, raw := range props {
        prop, ok := raw.(map[string]any)
        if !ok {
            continue
        }
        if ro, _ := prop["readOnly"].(bool); ro {
            out[name] = true
        }
    }
    return out
}
```

### Step 14.3 — Wire the call

In the function that walks each node's attribute schema (currently around line 854 where `checkAttributeSource` is called per property), after looping properties to check each `source:`, also call `checkAttributesSchema(n.Attributes.Schema, execReadOnlyProps, sbase, res)`. The `execReadOnlyProps` comes from `extractReadOnlyProps(executorSchemaBytes)` where `executorSchemaBytes` is fetched via the renamed `RegistryHooks.ExecutorExpectedAttributesSchema(n.Executor)` (per Step 14.0).

### Step 14.4 — Reject template author's `readOnly: true` overriding executor's `readOnly: false`

If the template's per-node schema marks a property `readOnly: true` but the executor's schema does NOT mark it `readOnly: true`, reject:

```go
for name, raw := range props {
    prop, ok := raw.(map[string]any)
    if !ok {
        continue
    }
    if ro, _ := prop["readOnly"].(bool); ro && !executorReadOnlyProps[name] {
        res.Errors = append(res.Errors, ValidationError{
            Path: fmt.Sprintf("%s.properties.%s", sbase, name),
            Msg:  "template marks property `readOnly: true` but the executor's expected_attributes_schema does not — the executor is authoritative on which properties it produces",
        })
    }
}
```

Add this check inside the same property loop in `checkAttributesSchema`.

### Step 14.5 — Verify (build only)

```
go build ./graph/node/...
```

---

## Task 15 — Template validator: implement L1 merge into effective schema at registration

**Files:** `graph/node/template_validator.go`.

### Step 15.1 — Decide where the merge lives

The spec specifies L1 (`template.defaults.attributes.by_executor.<exec>`) merges into the per-node effective schema at registration, contributing `default:` values for properties the executor declared or new properties (if the executor admits `additionalProperties: true`). The merge result is what `checkAttributesSchema` validates against.

Plan: introduce a small helper `mergeAttributeDefaults(execSchema map[string]any, l1Defaults map[string]any, nodeSchema map[string]any) map[string]any` that returns the effective schema. Each layer overrides the previous on `default:` values:
- Executor's schema is the base (types, `readOnly:`, etc.).
- L1's value fragments (`attribute-name → value`) translate into `properties.<name>.default = value` on the effective schema.
- L2's per-node `properties.<name>` overrides anything from layers 1 and L1 (most specific wins on `default:`).

### Step 15.2 — Implement the merge helper

```go
// mergeAttributeDefaults computes the per-node effective attribute
// schema as the union of (1) the executor's expected_attributes_schema,
// (2) template.defaults.attributes.by_executor[<exec>], and (3) the
// node's own attributes.schema. Most specific wins on `default:`.
// Types come from the executor's schema; type conflicts in L2 are
// flagged elsewhere by the existing JSON Schema validation pass.
//
// L1 is value fragments: map<attr_name, JSON value>. Each contributes
// a `default:` entry on the effective schema's properties map.
//
// @concept: attribute
func mergeAttributeDefaults(execSchema map[string]any, l1Defaults map[string]any, nodeSchema map[string]any) map[string]any {
    // Start with a deep copy of the executor's schema.
    out := deepCopyJSON(execSchema)
    if out == nil {
        out = map[string]any{}
    }
    props, _ := out["properties"].(map[string]any)
    if props == nil {
        props = map[string]any{}
        out["properties"] = props
    }
    // Apply L1: for each (attr, value), set properties[attr].default = value.
    for attr, val := range l1Defaults {
        prop, _ := props[attr].(map[string]any)
        if prop == nil {
            prop = map[string]any{}
            props[attr] = prop
        }
        prop["default"] = val
    }
    // Apply L2: deep-merge the node's properties on top.
    if nodeSchema != nil {
        nodeProps, _ := nodeSchema["properties"].(map[string]any)
        for attr, raw := range nodeProps {
            nodeProp, _ := raw.(map[string]any)
            if nodeProp == nil {
                continue
            }
            existing, _ := props[attr].(map[string]any)
            if existing == nil {
                props[attr] = nodeProp
                continue
            }
            for k, v := range nodeProp {
                existing[k] = v
            }
        }
        // Carry over top-level node-schema keys like `required` and `additionalProperties`.
        // The executor's `additionalProperties` setting wins on closed-schema policy;
        // the node's `required` may add fields to the existing list.
        if nodeReq, ok := nodeSchema["required"].([]any); ok {
            existingReq, _ := out["required"].([]any)
            seen := map[string]bool{}
            for _, r := range existingReq {
                if s, ok := r.(string); ok {
                    seen[s] = true
                }
            }
            for _, r := range nodeReq {
                if s, ok := r.(string); ok && !seen[s] {
                    existingReq = append(existingReq, r)
                    seen[s] = true
                }
            }
            out["required"] = existingReq
        }
    }
    return out
}

func deepCopyJSON(v map[string]any) map[string]any {
    if v == nil {
        return nil
    }
    b, err := json.Marshal(v)
    if err != nil {
        return map[string]any{}
    }
    var out map[string]any
    _ = json.Unmarshal(b, &out)
    return out
}
```

Both functions go in `graph/node/template_validator.go` (or a sibling helper file if the validator file grows past the ~500-line guideline — the implementer's call).

### Step 15.2.5 — Type-conflict rejection

If both `execSchema.properties.<name>.type` and `nodeSchema.properties.<name>.type` are present and differ, the merge silently uses the node's. Add a validation step (inside the existing per-node validation loop) that compares pre-merge types and rejects with a clear error:

```go
res.Errors = append(res.Errors, ValidationError{
    Path: fmt.Sprintf("%s.properties.%s.type", sbase, name),
    Msg:  fmt.Sprintf("template declares property type %q but the executor's expected_attributes_schema declares type %q — the executor is authoritative on types", nodeType, execType),
})
```

### Step 15.3 — Wire the merge into the per-node validation pass

Find the function that walks each node's attribute schema (the same area as Task 14.3). Compute the L1 defaults from the template's `Defaults.Attributes.ByExecutor[node.Executor]` (the new field from Task 11), the executor's schema bytes from the `RegistryHooks` callback, and the node's own schema. Pass the merged result to `checkAttributesSchema`.

### Step 15.4 — Plan to recompute effective schema at dispatch

The runtime needs the effective schema at dispatch (Task 17). The spec's Open question 1 leaves the persistence-vs-recompute pick to the plan. Pick **recompute at dispatch** as the simpler path — no template-persistence shape changes. The runtime needs access to the executor's `expected_attributes_schema` bytes at dispatch; Step 17.0 below plumbs that through `RunArgs`.

(If a future agent decides the recompute cost is meaningful, the alternative is to persist the effective schema inside the template body at registration. Out of scope for this plan.)

### Step 15.5 — Verify (build only)

```
go build ./graph/node/...
```

---

## Task 16 — Template validator: retire `validateUserdataAgainstSchema`

**Files:** `graph/node/template_validator.go`.

### Step 16.1 — Remove the function

Locate `validateUserdataAgainstSchema` (around line 1210 in `graph/node/template_validator.go`). Delete the entire function definition and its doc comment. Approximately lines 1210–1255 (~45 lines).

### Step 16.2 — Remove the call site

Find the call to `validateUserdataAgainstSchema` (around line 178 in the same file). Remove it. The surrounding control flow should stay intact.

### Step 16.3 — Remove the `@blessed-invariant 11` reference

The function's doc comment likely cites `@blessed-invariant 11`. Removed alongside the function. Sweep neighboring comments (lines 143, 308) for any remaining `@blessed-invariant 11` references and remove them too.

### Step 16.4 — Sweep `userdata` mentions

The validator's other functions still reference userdata in error messages and comments. Sweep for:
- `userdata` in error message strings (around line 329, "defaults.userdata.by_executor[...]").
- `userdata` in comments (around lines 92, 142, 624, 628).

Update each to refer to attributes instead. The function around line 624 handles pure-cascade pseudo-nodes warning about userdata — under the collapse, the equivalent warning is about declared attributes (or remove the warning entirely if pure-cascade nodes can't legitimately declare attributes; the implementer's call).

Specifically the validator at line 329 (defaults validation): change `defaults.userdata.by_executor[%q]` to `defaults.attributes.by_executor[%q]`. The function it lives in (around line 312, annotated `@concept: userdata`) renames or has its annotation updated to `@concept: attribute`.

### Step 16.5 — Verify

```
go build ./graph/node/... && go test ./graph/node/... -count=1
```

Existing tests of `validateUserdataAgainstSchema` will fail compile (no such function); delete those tests at Task 26.

---

## Task 17 — Runtime dispatch: extend `substituteAttributesSchema` for static defaults

**Files:** `runtime/runner_dispatch.go`.

### Step 17.0 — Plumb executor schema bytes into dispatch

Today's dispatch path reaches executor capability bytes through a closure (`observability.NewUserdataValidator(disc)`) captured in `RunArgs.UserdataValidator`. The closure threads through four structs (one config per layer, each carrying the closure as a field):

- `code:control/config/supervisor.go::SupervisorConfig.UserdataValidator` (field at line 62; wired into `runtime.Config` at line 137).
- `code:runtime/supervisor.go::Config.UserdataValidator` (field at line 136; wired into `RunArgs` at lines 245 and 445, and into `CallbackServer` indirectly).
- `code:runtime/callback.go::CallbackServer.UserdataValidator` (field at line 123; wired into `RunArgs` at line 375).
- `code:runtime/runner.go::RunArgs.UserdataValidator` (field at line 200; the consumption end — read at `runner_dispatch.go:664`).

Task 21 retires the field on all four; this step adds a replacement closure on all four.

Plumb the replacement:

- **`runtime/runner.go`** — add a new field on `RunArgs`:

```go
// ExpectedAttributesSchemaFor returns the executor's advertised
// expected_attributes_schema bytes (JSON Schema) plus an ok flag
// (false for unknown executors). Used by substituteAttributesSchema
// to compute the effective per-node attribute schema at dispatch.
// Wired in cmd/rimsky-supervisor/main.go from the discovery cache
// (the same `disc` value that previously fed UserdataValidator).
//
// @concept: attribute
ExpectedAttributesSchemaFor func(executor string) (schema []byte, ok bool)
```

- **`runtime/callback.go`** — add the same field on `CallbackServer` (mirroring today's `UserdataValidator` field at line 123; thread it into `RunArgs` at the line 375 wire-up).

- **`runtime/supervisor.go`** — add the same field on `Config` (mirroring `UserdataValidator` at line 136; thread it into `RunArgs` at lines 245 and 445).

- **`control/config/supervisor.go`** — add the same field on `SupervisorConfig` (mirroring `UserdataValidator` at line 62; thread it into `runtime.Config` at line 137 in the wire-up).

- **`cmd/rimsky-supervisor/main.go`** — replace the construction site at line 217 (`UserdataValidator: observability.NewUserdataValidator(disc),`) with `ExpectedAttributesSchemaFor: observability.NewExpectedAttributesSchemaResolver(disc),`. The new constructor lives in `control/observability/` — see Step 17.0.5 below.

### Step 17.0.5 — Add `observability.NewExpectedAttributesSchemaResolver`

Add a new file `control/observability/expected_attributes_schema_resolver.go`:

```go
// expected_attributes_schema_resolver.go — schema-bytes lookup for
// dispatch-time effective-schema computation. Replaces the role
// userdata_validator.go played pre-collapse (the validator was a
// schema check; the new resolver returns the schema bytes for the
// merge upstream of the check, which now happens inside
// graph/attribute.Validate).
//
// Per spec
// .ok-planner/specs/2026-05-20-userdata-collapse-into-attributes-design.md
// §"Executor side".

package observability

// NewExpectedAttributesSchemaResolver constructs a closure that, given
// an executor name, returns the executor's advertised
// expected_attributes_schema bytes from the discovery cache. ok=false
// when the discovery cache has no record for the named executor.
func NewExpectedAttributesSchemaResolver(disc *Discovery) func(executor string) ([]byte, bool) {
    return func(executor string) ([]byte, bool) {
        // Discovery cache lookup — same path the retiring
        // userdata_validator.go used. Read the existing
        // NewUserdataValidator for the exact lookup mechanics; reuse
        // them here.
        // ...
    }
}
```

The exact implementation depends on the existing `Discovery` type's API (read `code:control/observability/discovery.go` and the retiring `code:control/observability/userdata_validator.go` to see how the old closure pulled schema bytes — the new resolver does the same lookup, just returns the bytes rather than running validation against them).

This constructor lives in `control/observability/` even though `userdata_validator.go` retires in Task 22 — the package itself stays.

### Step 17.0.6 — Verify

```
go build ./runtime/... ./control/...
```

Compile must succeed once Tasks 17.0 through 17.0.5 land together. (The retiring `UserdataValidator` fields stay alongside the new fields until Task 21 removes them.)

### Step 17.1 — Inspect current function

`substituteAttributesSchema` lives around line 500 of `runtime/runner_dispatch.go`. Current behavior: walks `schema.properties`, for each property with a `source:` directive, calls `SubstituteValue`. If missing-source on required, fails dispatch; if missing on non-required, skips.

### Step 17.2 — Compute the effective schema at dispatch

Per Task 15.4 (recompute at dispatch), the runtime merges:
- Executor's `expected_attributes_schema` bytes, fetched via `dctx.Args.ExpectedAttributesSchemaFor(acq.Executor)` (the closure plumbed at Step 17.0).
- Template L1 defaults: `acq.TemplateAttributeDefaults` (after Task 19 renames the acquisition field).
- Node L2 schema: `def.Attributes.Schema`.

Compute the effective schema via the same `mergeAttributeDefaults` helper from Task 15. Move that helper to a shared location (`foundation/spec/` is a natural home — both `graph/node` and `runtime` can import from `foundation/`) so both registration and dispatch share one implementation.

### Step 17.3 — Emit static-default property values

After resolving source-bound properties (existing logic), iterate the effective schema's properties and for each property with a `default:` value and no `source:`, emit the default into the output map. The post-resolution map then contains: source-resolved values + static-default values.

### Step 17.4 — Handle null-from-lenient correctly

Tasks 12 and 13 introduce the `?` marker. The `SubstituteValue` call returns `nil` for a lenient directive that misses (Task 12.2). The current required-field check (`if _, isReq := required[name]; isReq { return nil, err }`) handled the strict ErrMissingSource case. Under the new model, ErrMissingSource still means strict-failure; nil from `SubstituteValue` means lenient-resolved-to-null.

Update the loop: only continue/fail on `ErrMissingSource`. A `nil` return value (with no error) lands in the output map as JSON `null`, satisfying the required-presence check.

### Step 17.5 — Apply L3 + L4 overrides on the resolved set

Currently `runner_dispatch.go` applies userdata overrides separately (Tasks 19–21 below cover the rename). For attributes, after `substituteAttributesSchema` returns its resolved values, call `applyAttributeOverrides(resolved, l3ByExec, l4ByNode, executor, nodeType)` to merge instance overrides. The result is what populates `ExecuteRequest.attributes`.

The L3 + L4 merge is structurally the same as today's userdata-override merge; Task 18 renames the helper.

### Step 17.6 — Validate the final merged attribute set

The dispatch path already JSON-Schema-validates resolved attribute values at `runner_dispatch.go:364` via `attributes.Validate(dispatchSchema, resolved, attributes.PhaseDispatch)`. Under the collapse:

- The `dispatchSchema` input switches from the bare per-node attribute schema to the effective schema (executor + L1 + L2 merged per Step 17.2).
- The validation runs on the *merged-with-L3+L4* attribute bag, not just the substitution output. Reorder the call so the L3/L4 merge from Step 17.5 happens before this validation pass.

Do **not** import `github.com/santhosh-tekuri/jsonschema/v5` directly from this site. The graph layer already encapsulates the JSON Schema validator behind `attributes.Validate`; reuse the existing call (DRY) rather than introducing a second validator in the runtime package.

### Step 17.7 — Drop the userdata block in `buildExecuteRequest`

Find `buildExecuteRequest` (around line 637–727 of `runner_dispatch.go`). Remove the entire userdata block (lines 641–685):

```go
// Build per-node userdata, then deep-merge in four layers...
var baseUserdata map[string]any
if def != nil && len(def.Userdata) > 0 {
    baseUserdata = def.Userdata
}
merged := applyUserdataOverrides(acq.TemplateUserdataDefaults, baseUserdata, acq.InstanceUserdataOverrides, acq.Executor, acq.NodeType, dctx.Args.Logger)
acq.MergedUserdata = merged
if dctx.Args.UserdataValidator != nil && acq.Executor != "" {
    if err := dctx.Args.UserdataValidator(acq.Executor, merged); err != nil {
        return nil, &userdataValidationError{cause: err}
    }
}
userdataStruct := &structpb.Struct{Fields: map[string]*structpb.Value{}}
if len(merged) > 0 {
    s, err := structpb.NewStruct(merged)
    ...
}
```

Replace with: no userdata struct construction. The `ExecuteRequest` builder later in the function no longer sets `Userdata`. Drop the entire `req.Userdata = userdataStruct` line.

### Step 17.8 — Remove `userdataValidationError`

The wrapped error type `userdataValidationError` (around line 27 of `runner_dispatch.go`) retires. Locate its definition and remove. Remove the error-routing block that maps it to `error_class: userdata_validation_failed` (around line 164). The post-merge JSON Schema validation in Step 17.6 emits a clearer error class (`template_resolution_failed` or `attribute_validation_failed` — pick one consistent with the existing error vocabulary; `template_validation_failed` is most accurate since attribute schemas are part of the template).

### Step 17.9 — Sweep `@blessed-invariant 11` cites

Lines 647 and 657 of `runner_dispatch.go` cite `@blessed-invariant 11`. Remove those references (they're inside the userdata-block comment that's being deleted at Step 17.7).

### Step 17.10 — Verify (build only)

```
go build ./runtime/...
```

Test coverage covered by Task 27.

---

## Task 18 — Runtime: rename `userdata_overrides.go` → `attribute_overrides.go`

**Files:**
- Old: `runtime/userdata_overrides.go`
- New: `runtime/attribute_overrides.go`
- Old: `runtime/userdata_overrides_test.go`
- New: `runtime/attribute_overrides_test.go`

### Step 18.1 — Rename files

```
git mv runtime/userdata_overrides.go runtime/attribute_overrides.go
git mv runtime/userdata_overrides_test.go runtime/attribute_overrides_test.go
```

### Step 18.2 — Update file contents

In the renamed `.go` file:

- Rename `applyUserdataOverrides` → `applyAttributeOverrides`.
- Update the function's signature and doc comment. The function now operates on the post-resolution attribute bag, taking L3 (`InstanceAttributeOverrides.by_executor`) and L4 (`InstanceAttributeOverrides.by_node`) inputs. L1 has moved to registration (Task 15), so no longer passed here.

New signature (the implementer's call on parameter naming):

```go
// applyAttributeOverrides applies L3 + L4 instance-level overrides
// onto the post-resolution attribute bag, returning the final values
// shipped to the executor.
//
// L1 (template.defaults.attributes.by_executor) is folded into the
// effective schema at template registration and produces `default:`
// values inside the resolved attribute set already — not passed here.
//
// Per spec
// .ok-planner/specs/2026-05-20-userdata-collapse-into-attributes-design.md
// §"Override layering".
//
// @concept: attribute
func applyAttributeOverrides(
    resolved map[string]any,
    instanceOverrides map[string]any,  // {by_executor: {...}, by_node: {...}}
    executor string,
    nodeType string,
    logger *slog.Logger,
) map[string]any
```

The deep-merge mechanics stay the same (use `DeepMergeJSON` from `foundation/shared/jsonmerge.go`).

- Update the `@concept: userdata` annotation to `@concept: attribute`.
- Remove the `@blessed-invariant 11` cite (around line 36) and the related comment.

### Step 18.3 — Update the renamed test file

In `attribute_overrides_test.go`: rename test functions from `TestApplyUserdataOverrides*` to `TestApplyAttributeOverrides*`. Update the test fixtures: `instance.userdata_overrides` → `instance.attribute_overrides` (in test data setup), and the assertion targets.

### Step 18.4 — Update callers

Find every call to `applyUserdataOverrides` in `runtime/`. Currently just `runner_dispatch.go:653`. Update the call to `applyAttributeOverrides` with the new signature. Note this call site was largely removed in Task 17 (the userdata block deletion); the new call to `applyAttributeOverrides` lives where the new attribute-flow lives — at the end of `substituteAttributesSchema`'s resolution loop (or just after, in `buildExecuteRequest`).

### Step 18.5 — Verify (build only)

```
go build ./runtime/...
```

---

## Task 19 — Runtime: rename `acquisition.MergedUserdata` → `acquisition.MergedAttributes`

**Files:** `runtime/runner_acquire.go`, `runtime/runner_acquire_helpers.go`.

### Step 19.1 — Find the field

The field is on the `acquisition` struct in `runtime/runner_acquire.go` (around line 100+ based on the spec's annotation reference at line 104). Rename `MergedUserdata` → `MergedAttributes`. Update the doc comment to describe the merged attribute snapshot's purpose.

### Step 19.2 — Rename `TemplateUserdataDefaults` and `InstanceUserdataOverrides`

Same file, same struct. The acquisition struct carries:
- `TemplateUserdataDefaults` — the source of L1. After Task 11's spec changes, this is the template's `defaults.attributes.by_executor.<exec>` map. Rename → `TemplateAttributeDefaults`.
- `InstanceUserdataOverrides` — the source of L3 + L4 from the instance row's `attribute_overrides` column. Rename → `InstanceAttributeOverrides`.

Update doc comments accordingly.

### Step 19.2.5 — Update the derivation site in the acquisition body

The acquisition body itself populates these fields. At `runtime/runner_acquire.go:383`, the local variable `templateUserdataDefaults := templateUserdataDefaultsFor(tmpl, nd.Executor)` derives L1 from the template's `Defaults.Userdata.ByExecutor[executor]`. Post-Task 11, that source field is gone — replaced by `Defaults.Attributes.ByExecutor[executor]`.

Rename:
- Local variable `templateUserdataDefaults` → `templateAttributeDefaults`.
- Helper function `templateUserdataDefaultsFor` → `templateAttributeDefaultsFor` (find its definition — likely in the same file or a sibling — and update the function body to read from `tmpl.Defaults.Attributes.ByExecutor[executor]` rather than `tmpl.Defaults.Userdata.ByExecutor[executor]`).
- Both call sites at lines 431 and 454 (assignment of `TemplateUserdataDefaults: templateUserdataDefaults` to the acquisition struct) update to use the new field name and new local variable name.

### Step 19.3 — Sweep `@blessed-invariant 11` cites + `@concept: userdata`

Per the codebase scan: lines 59, 92, 102, 151 of `runtime/runner_acquire.go` cite `@blessed-invariant 11`. Line 104 has `@concept: userdata`. Remove all of these.

Replace any surviving cite with `@concept:inertness` plus an attribute-value noun if the surrounding rationale still applies to attribute values.

### Step 19.4 — Update callers

Use a broad grep to enumerate every reference; the per-task listing is illustrative, not exhaustive:

```
rg 'MergedUserdata|TemplateUserdataDefaults|InstanceUserdataOverrides' runtime/ foundation/ control/ cmd/
```

Known sites (the implementer should still verify with the grep above — the list may have drifted):
- `runtime/runner_dispatch.go` (already touched in Task 17).
- `runtime/lineage_writer.go` (Task 20).
- `runtime/runner_acquire.go` itself (where the fields populate).
- `runtime/runner_terminal.go:311`.
- `runtime/runner_locks.go:402`.
- `runtime/runner_terminal_handlers.go` (around line 146).
- `runtime/runner_error_policy.go` (around line 174).
- `runtime/runner_terminal_park.go` (around line 201).
- `runtime/subgraph_dispatch.go` (around line 591).
- `runtime/subgraph_caller_lineage_test.go` (around lines 191, 238).

Each call site applies the field rename per Steps 19.1 and 19.2.

### Step 19.5 — Sweep `runner_acquire_helpers.go`

Open `runtime/runner_acquire_helpers.go`. Sweep for any userdata references (none expected per the earlier grep, but verify).

### Step 19.6 — Verify

```
go build ./runtime/...
```

---

## Task 20 — Runtime: lineage writer rename

**Files:** `runtime/lineage_writer.go`.

### Step 20.1 — Find the field

The lineage writer hashes the merged userdata (now attributes) snapshot at dispatch for the lineage record. Find references to `MergedUserdata` (per Task 19, now `MergedAttributes`). Also find any string-literal mentions of "userdata" in comments or log fields.

### Step 20.2 — Rename references

`MergedUserdata` → `MergedAttributes` (already done in Task 19, but the lineage writer accesses it). Comment strings and log-field names that say "userdata" become "attributes."

If the lineage writer emits a structured log or event field named `userdata_hash` or `merged_userdata_hash`, rename to `attribute_hash` or `merged_attributes_hash`.

### Step 20.3 — Verify

```
go build ./runtime/...
```

---

## Task 21 — Runtime: retire `UserdataValidator` across all four config structs

**Files:** `runtime/runner.go`, `runtime/callback.go`, `runtime/supervisor.go`.

### Step 21.1 — Remove the field on `RunArgs`

`runtime/runner.go`: remove the `UserdataValidator` field at line 200 and its doc comment block (lines ~184–200). The cite of `@blessed-invariant 11` at line 186 goes with the comment.

### Step 21.2 — Remove the field on `CallbackServer`

`runtime/callback.go`: remove the `UserdataValidator` field at line 123 and its doc comment at line 119. Remove the threading at line 375 (`UserdataValidator: c.UserdataValidator,`).

### Step 21.3 — Remove the field on `runtime.Config`

`runtime/supervisor.go`: remove the `UserdataValidator` field at line 136 and its doc comment at line 131. Remove the threading at lines 245 and 445.

### Step 21.4 — Sweep stragglers

```
rg 'UserdataValidator' runtime/ control/config/
```

Expect zero hits after this task (`control/config/supervisor.go::SupervisorConfig.UserdataValidator` retires in Task 23).

### Step 21.5 — Verify

```
go build ./runtime/...
```

---

## Task 22 — Retire `control/observability/userdata_validator.go`

**Files:** `control/observability/userdata_validator.go` and any sibling test file.

### Step 22.1 — Delete the file

```
git rm control/observability/userdata_validator.go
git rm control/observability/userdata_validator_test.go  # if it exists
```

Run `ls control/observability/` first to confirm exact filenames; the test file may not exist.

### Step 22.2 — Remove imports

Any file importing the package's `NewUserdataValidator` constructor fails to compile. The known site is `cmd/rimsky-supervisor/main.go:217`; cleanup happens in Task 23.

### Step 22.3 — Verify (will fail until Task 23)

```
go build ./control/observability/...
```

Expected to pass — the file is gone; nothing in `control/observability/` should still reference it.

```
go build ./cmd/rimsky-supervisor/...
```

Expected to fail. Task 23 fixes.

---

## Task 23 — Retire supervisor wiring of `UserdataValidator`

**Files:** `cmd/rimsky-supervisor/main.go`, `control/config/supervisor.go`.

### Step 23.1 — Remove the construction site

In `cmd/rimsky-supervisor/main.go`, find lines 180–217 (the comment block about UserdataValidator and the construction site at line 217: `UserdataValidator: observability.NewUserdataValidator(disc),`). Delete the comment block AND the line that passes `UserdataValidator` into the config struct.

### Step 23.2 — Remove the supervisor config field

In `control/config/supervisor.go`, find lines 56–62 (the `UserdataValidator` field on the `SupervisorConfig` struct). Delete the field declaration and its doc comment.

Find line 137 (where the field threads into `runtime.Config` — `UserdataValidator: cfg.UserdataValidator,`). Delete the line. (Note: line 137 reads into `runtime.Config`, not `runtime.RunArgs` directly; the runtime then routes the value into `RunArgs` per Task 21.3.)

### Step 23.3 — Sweep imports

If `control/config/supervisor.go` imported `observability` solely for the `UserdataValidator` type, remove the import.

### Step 23.4 — Verify

```
go build ./cmd/rimsky-supervisor/... ./control/config/...
```

---

## Task 24 — Control API: rename `userdata_overrides` HTTP/JSON surface

**Files:** `control/controlapi/instances.go`, `control/controlapi/userdata_overrides.go`.

### Step 24.1 — Rename the file

```
git mv control/controlapi/userdata_overrides.go control/controlapi/attribute_overrides.go
```

### Step 24.2 — Update file contents

In the renamed file: sweep for `userdata_overrides` (JSON field name), `UserdataOverrides` (Go field), and userdata mentions in comments. Replace with attribute counterparts.

Function names that include `Userdata` rename to `Attribute` (e.g., `validateUserdataOverrides` → `validateAttributeOverrides`).

### Step 24.3 — Update `instances.go`

`code:control/controlapi/instances.go` handles `POST /instances` and `GET /instances/{id}`. The request body has a `userdata_overrides` field today; rename to `attribute_overrides` in the JSON-tagged Go struct and in the body-parse logic. Same for the response shape.

### Step 24.4 — Remove `@blessed-invariant 11` cite

Sweep both files for the cite.

### Step 24.5 — Verify

```
go build ./control/controlapi/... && go test ./control/controlapi/... -count=1
```

---

## Task 25 — Cross-cutting: `@blessed-invariant 11` reference sweep

**Files:** all files in the repo containing `invariant 11` references in any form.

### Step 25.1 — Discover references

Run a broad regex to catch all forms (canonical `@blessed-invariant 11`, plus prose forms like `invariant 11`, `blessed-invariant 11`, `§blessed-invariant 11`, `§4.10 invariant 11`):

```
rg -i 'invariant\s*(no\.?\s*)?11|blessed-invariant\s*11' . --type-add 'src:*.go' --type-add 'src:*.ts' --type-add 'src:*.proto' --type-add 'src:*.md' --type src 2>/dev/null
```

Expected hit count: ~39 sites across the runtime, foundation, graph, control, and executor packages plus the test scenarios. Specific files surfaced during planning: `code:graph/attribute/substitution.go` (lines 20-24, 106, 409, 421, 448), `code:graph/node/template_validator.go` (lines 143, 308), `code:runtime/runner.go` (line 186), `code:runtime/runner_dispatch.go` (lines 647, 657), `code:runtime/runner_acquire.go` (lines 59, 92, 102, 151), `code:runtime/userdata_overrides.go` (line 36 — already gone after Task 18), `code:runtime/message_delivery.go` (line 34), `code:runtime/cascade_invalidate.go` (line 83), `code:runtime/backfill.go` (line 75), `code:foundation/shared/jsonmerge.go` (line 13), `code:foundation/spec/template.go` (line 67), `code:foundation/persistence/node_runs.go` (line 321), `code:foundation/persistence/instances.go` (line 20), `code:foundation/persistence/messages.go`, `code:graph/attribute/doc.go` (line 32), `code:executors/http-node/observability.go`, `code:control/controlapi/messages.go`, `code:control/controlapi/instances.go`, `code:test/scenarios/backfill/partition_selector_override_test.go` (line 58). Some of these have already been handled in earlier tasks (12, 16, 17, 18, 19, 21, 24). The sweep catches the remainder.

### Step 25.2 — Address each hit

For each reference:
- If the surrounding code retires (e.g., file moves), the reference goes with it (already handled by earlier tasks).
- If the surrounding code stays and the invariant cite is about userdata, remove the cite. If the surrounding rationale still applies to attribute values, replace with `@concept:inertness` plus an attribute-value noun.
- Tests that cite the invariant for documentation purposes update to cite `@concept:inertness` instead.

The canonical invariant block at `code:graph/attribute/substitution.go:20-24` is removed (the four-line comment block beginning `// @blessed-invariant 11 — Userdata is inert in Rimsky.`). Other `@blessed-invariant` blocks in the file (e.g., 20, 21) are unaffected.

### Step 25.3 — Verify

```
rg -i 'invariant\s*(no\.?\s*)?11|blessed-invariant\s*11' . 2>/dev/null
```

Expect zero hits in source files (CHANGELOG.md history may retain mentions; that's fine — the CHANGELOG is an append-only journal).

```
go build ./... && go test ./... && make lint
```

---

## Task 26 — Cross-cutting: `@concept: userdata` annotation sweep

**Files:** all source files in the repo containing `@concept: userdata`.

### Step 26.1 — Discover references

```
rg '@concept: userdata' . 2>/dev/null
```

Expected hits: `code:graph/node/template_validator.go:312`, `code:runtime/userdata_overrides.go:41` (already gone after Task 18), `code:runtime/runner_locks.go:404`, `code:runtime/runner_acquire.go:104` (already gone after Task 19), `code:foundation/spec/template.go:53, 70, 80` (already gone after Task 11).

Remaining sites are mostly handled by earlier tasks. The sweep catches any stragglers.

### Step 26.2 — Address each remaining hit

- `graph/node/template_validator.go:312`: the annotation is on a function validating `defaults.userdata.by_executor`. After Task 16 swept the function's user-facing text, the annotation likely changed to `@concept: attribute` (or the function retired). Verify and update.
- `runtime/runner_locks.go:404`: the annotation is on a function threading userdata defaults through. The function either retires alongside userdata defaults or is renamed to thread the attribute defaults through. Determine which by reading the function body — if its only purpose was userdata (e.g., a helper that merged userdata into the acquisition struct), it retires; if it does something else and just had a `userdata` annotation mis-applied, the annotation updates to `@concept: attribute`.

### Step 26.3 — Verify

```
rg '@concept: userdata' . 2>/dev/null
```

Expect zero hits.

```
go build ./... && go test ./...
```

---

## Task 27 — Substitution engine tests: `?` marker

**Files:** `graph/attribute/substitution_test.go`.

### Step 27.1 — Add `?` marker tests

After the existing `TestSubstitute` table-driven tests, add a new test (or new sub-tests under an existing function) covering:

```go
// TestSubstitute_LenientMarker — per spec 2026-05-20-userdata-collapse:
// the `?` marker opts a directive into lenient-on-missing resolution.
// Missing source with `?` returns JSON null; missing source without
// `?` returns ErrMissingSource.
func TestSubstitute_LenientMarker(t *testing.T) {
    // (1) Strict directive on missing source → ErrMissingSource.
    _, err := Substitute("{{nodes.x.attribute.y}}", ResolveContext{Deps: map[string]json.RawMessage{}})
    if !IsMissingSource(err) {
        t.Fatalf("strict missing source: want ErrMissingSource, got %v", err)
    }
    // (2) Lenient directive on missing source → empty string (embedded mode).
    s, err := Substitute("{{nodes.x.attribute.y?}}", ResolveContext{Deps: map[string]json.RawMessage{}})
    if err != nil {
        t.Fatalf("lenient missing source: want nil error, got %v", err)
    }
    if s != "" {
        t.Fatalf("lenient missing source: want empty string, got %q", s)
    }
    // (3) Lenient with present value → the value.
    deps := map[string]json.RawMessage{"x": json.RawMessage(`{"y": "hello"}`)}
    s, err = Substitute("{{nodes.x.attribute.y?}}", ResolveContext{Deps: deps})
    if err != nil || s != "hello" {
        t.Fatalf("lenient present source: want %q nil, got %q %v", "hello", s, err)
    }
}

// TestSubstituteValue_LenientMarker — whole-directive mode null lift.
func TestSubstituteValue_LenientMarker(t *testing.T) {
    // Missing source + `?` → nil (JSON null).
    v, err := SubstituteValue("{{nodes.x.attribute.y?}}", ResolveContext{Deps: map[string]json.RawMessage{}})
    if err != nil {
        t.Fatalf("lenient missing: want nil error, got %v", err)
    }
    if v != nil {
        t.Fatalf("lenient missing: want nil, got %v", v)
    }
}
```

Add a third test covering embedded sources (text + multiple directives, each independently strict or lenient):

```go
func TestSubstitute_EmbeddedSourceWithMarkers(t *testing.T) {
    deps := map[string]json.RawMessage{"x": json.RawMessage(`{"y": "hello"}`)}
    s, err := Substitute("greeting: {{nodes.x.attribute.y}}, optional: {{nodes.z.attribute.q?}}", ResolveContext{Deps: deps})
    if err != nil {
        t.Fatalf("want nil error, got %v", err)
    }
    if s != "greeting: hello, optional: " {
        t.Fatalf("want %q, got %q", "greeting: hello, optional: ", s)
    }
}
```

### Step 27.2 — Verify

```
go test ./graph/attribute/... -count=1
```

---

## Task 28 — Template validator tests: relaxed grammar + `checkAttributesSchema`

**Files:** `graph/node/template_validator_test.go`.

### Step 28.1 — Add tests for the relaxed source grammar

Add tests covering:

- Source with literal text + one directive (accepted): `"Generate config for {{params.domain}}."`.
- Source with multiple directives separated by text (accepted): `"Hello {{params.x}}, world {{params.y}}."`.
- Source with `?` marker on a single directive (accepted): `"{{nodes.x.attribute.y?}}"`.
- Source with `?` marker on one directive in an embedded source (accepted): `"warnings: {{nodes.x.attribute.warnings_block?}}"`.
- Source with `?` and `|` on the same directive (rejected): `"{{X? | \"y\"}}"`.
- Source as an array (rejected, declined form): `source: ["{{X}}", "{{Y}}"]`.
- Source with multi-pipe chain (rejected, declined form): `"{{X | Y | Z}}"` — the existing test should still pass.

Use the existing `TestCheckAttributeSource_*` test function as the template; follow its `TemplateSpec` construction pattern.

### Step 28.2 — Add tests for `checkAttributesSchema`

Add tests covering:

- Property with `source:` and no `default:` (accepted).
- Property with `default:` and no `source:` (accepted).
- Property with neither, marked `readOnly: true` in the executor's expected schema (accepted) — the test setup needs to register a fake executor with a `readOnly: true` property.
- Property with neither, not marked `readOnly: true` (rejected).
- Property with both `source:` and `default:` (rejected).
- Property where the template marks `readOnly: true` but the executor does not (rejected).

### Step 28.3 — Delete tests for retired `validateUserdataAgainstSchema`

Find tests named like `TestValidateUserdataAgainstSchema*`. Delete them — the function retired in Task 16.

### Step 28.4 — Verify

```
go test ./graph/node/... -count=1
```

---

## Task 29 — Runtime dispatch tests: new `runner_dispatch_test.go`

**Files:** `runtime/runner_dispatch_test.go` (new file — `runtime/runner_dispatch.go` has no test file today).

### Step 29.1 — Create the file

Follow the conventions of `runtime/attribute_overrides_test.go` (renamed in Task 18). Use `t.Run` table-driven style.

```go
// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed per LICENSE.agpl + COPYRIGHT.

package runtime

import (
    "context"
    "encoding/json"
    "testing"

    "github.com/fallguyconsulting/rimsky/graph/attribute"
)

// TestSubstituteAttributesSchema_StaticDefaults — properties without
// `source:` but with `default:` emit the default value in the output.
func TestSubstituteAttributesSchema_StaticDefaults(t *testing.T) {
    schema := map[string]any{
        "type": "object",
        "properties": map[string]any{
            "model": map[string]any{
                "type":    "string",
                "default": "claude-sonnet-4-5",
            },
        },
    }
    out, err := substituteAttributesSchema(schema, attribute.ResolveContext{})
    if err != nil {
        t.Fatalf("substituteAttributesSchema: %v", err)
    }
    if got, want := out["model"], "claude-sonnet-4-5"; got != want {
        t.Fatalf("model default: want %q, got %v", want, got)
    }
}

// TestSubstituteAttributesSchema_EmbeddedSource — source with embedded
// text + directives resolves to a concatenated string.
func TestSubstituteAttributesSchema_EmbeddedSource(t *testing.T) {
    schema := map[string]any{
        "type": "object",
        "properties": map[string]any{
            "prompt": map[string]any{
                "type":   "string",
                "source": "Hello {{params.name}}",
            },
        },
    }
    ctx := attribute.ResolveContext{Params: json.RawMessage(`{"name": "world"}`)}
    out, err := substituteAttributesSchema(schema, ctx)
    if err != nil {
        t.Fatalf("substituteAttributesSchema: %v", err)
    }
    if got, want := out["prompt"], "Hello world"; got != want {
        t.Fatalf("prompt: want %q, got %v", want, got)
    }
}

// TestSubstituteAttributesSchema_LenientNullEmit — `?` marker on a
// missing source emits JSON null, not an error.
func TestSubstituteAttributesSchema_LenientNullEmit(t *testing.T) {
    schema := map[string]any{
        "type": "object",
        "properties": map[string]any{
            "warnings": map[string]any{
                "type":   "string",
                "source": "{{nodes.verify.attribute.warnings_block?}}",
            },
        },
    }
    out, err := substituteAttributesSchema(schema, attribute.ResolveContext{Deps: map[string]json.RawMessage{}})
    if err != nil {
        t.Fatalf("substituteAttributesSchema: %v", err)
    }
    if v, ok := out["warnings"]; !ok || v != nil {
        t.Fatalf("warnings: want present nil, got (present=%v, value=%v)", ok, v)
    }
}
```

Add at least one test for the merged-with-L3+L4 case if the test scaffolding allows; otherwise leave that to the scenario tests (Tasks 31–33).

### Step 29.2 — Verify

```
go test ./runtime/... -count=1
```

---

## Task 30 — Claude-agent executor: rename `userdata-schema.ts` → `expected-attributes-schema.ts`

**Files:**
- Old: `executors/claude-agent/src/userdata-schema.ts`
- New: `executors/claude-agent/src/expected-attributes-schema.ts`

### Step 30.1 — Rename the file

```
git mv executors/claude-agent/src/userdata-schema.ts executors/claude-agent/src/expected-attributes-schema.ts
```

### Step 30.2 — Update file contents

In the renamed file:

- Export name `userdataSchema` → `expectedAttributesSchema`.
- Export name `userdataSchemaBytes` → `expectedAttributesSchemaBytes`.
- Rename `user_prompt_template` property in the schema to `user_prompt` (signaling it's the resolved prompt, not a template).
- Add `default:` entries for fields with natural defaults:

```ts
model: { type: "string", default: "claude-sonnet-4-5" },
```

- Flip `additionalProperties: false` to `additionalProperties: true` to admit author-declared extension attributes used purely for inter-node dataflow.
- Update the file header comment to reflect the renamed advertised surface (`Capabilities.expected_attributes_schema` rather than `Capabilities.userdata_schema`).

The doc comment at the top of the file currently cites `@blessed-invariant 11`; remove the cite.

### Step 30.3 — Sweep importers

Find every file that imports from `./userdata-schema.js`. Update import paths:

```
rg "userdata-schema" executors/claude-agent/src/
```

Expected hits: `index.ts`, `agent-run.ts`, `server.ts`, `http-bridge.ts`, the test files. Update import paths to `./expected-attributes-schema.js`.

Update import bindings: `import { userdataSchema, userdataSchemaBytes } from ...` → `import { expectedAttributesSchema, expectedAttributesSchemaBytes } from ...`.

### Step 30.4 — Update Capabilities response

Find where the executor advertises Capabilities. The handler responds to the `Capabilities` RPC on `ExecutorObservability`. The advertised field changes from `userdata_schema` to `expected_attributes_schema`. The proto change in Task 4 already renamed the field; the TS gen will reflect that. Update the caller to use the new field name on the proto-generated message.

### Step 30.5 — Verify

```
cd executors/claude-agent && npm install && npm run build
```

Tests in Task 31.

---

## Task 31 — Claude-agent executor: retire `renderTemplate`; add metadata-footer append

**Files:** `executors/claude-agent/src/agent-run.ts`.

### Step 31.1 — Remove `renderTemplate`

Find the `renderTemplate` function (around line 1023). Delete the entire function definition and its doc comment. Also delete the `renderTemplate` export from `executors/claude-agent/src/index.ts` if it's there (the spec mentions exporting it for tests; remove the export).

### Step 31.2 — Simplify `AgentRunOptions`

Find the `AgentRunOptions` interface (around line 69). Update the fields:

- Drop `userPromptTemplate: string`.
- Add `userPrompt: string` (already resolved by rimsky).
- Keep `systemPrompt: string` (already resolved, no footer appended).
- Drop `templateVars` if present (no template rendering needed).

### Step 31.3 — Update the dispatch entrypoint

Find `runAgentReal` (around line 279). Remove the `renderTemplate(systemPrompt, promptVars)` and `renderTemplate(userPromptTemplate, promptVars)` calls (lines 339–340). Replace with direct assignment + footer append:

```ts
const callbackToken = randomUUID();
// Resume-context vars come from the executor's parseResumeContext
// path; under the userdata collapse they're no longer template
// substitution candidates — they live in the appended footer.
const resumePayload = resumeContext?.payload
  ? Buffer.from(resumeContext.payload).toString("utf8")
  : "";
const resumeReason = resumeContext?.resumeReason ?? "";

const renderedSystem = systemPrompt;
const renderedUser =
  userPrompt +
  "\n\n---\n" +
  `callback_token: ${callbackToken}\n` +
  `resume_payload: ${resumePayload}\n` +
  `resume_reason: ${resumeReason}\n` +
  "---\n";
```

The footer format is the one the spec mandates (always-emitted, fixed shape, on user prompt only).

### Step 31.4 — Verify

```
cd executors/claude-agent && npm run build
```

Tests in Task 32.

---

## Task 32 — Claude-agent executor: update server.ts and http-bridge.ts

**Files:** `executors/claude-agent/src/server.ts`, `executors/claude-agent/src/http-bridge.ts`.

### Step 32.1 — Read from `req.attributes` instead of `req.userdata`

In `server.ts` around line 389:
- `const userdata = toRecord(req.userdata);` deletes.
- `const attributes = toRecord(req.attributes);` stays (already there).
- All `userdata.model`, `userdata.system_prompt`, `userdata.user_prompt_template`, `userdata.cli` references switch to `attributes.model`, `attributes.system_prompt`, `attributes.user_prompt`, `attributes.cli`.
- `cwd_from_store`, `cwd` references (`userdata.cwd_from_store`, `userdata.cwd`) switch to `attributes.cwd_from_store`, `attributes.cwd`.

Same set of substitutions in `http-bridge.ts` around line 178+.

The proto generated TS may not yet expose `req.attributes` — verify the generated TS has the field; if not, regeneration may be required (TypeScript `make proto-gen` equivalent).

### Step 32.2 — `parseCliConfig(userdata.cli)` → `parseCliConfig(attributes.cli)`

Both files. Same call signature, different source.

### Step 32.3 — Verify

```
cd executors/claude-agent && npm run build
```

---

## Task 33 — Claude-agent executor: update tests

**Files:**
- `executors/claude-agent/src/agent-run.test.ts`
- `executors/claude-agent/src/server.test.ts`
- `executors/claude-agent/src/http-bridge.test.ts`
- `executors/claude-agent/src/lifecycle.e2e.test.ts`

### Step 33.1 — Delete `renderTemplate` tests

In `agent-run.test.ts`, find the `describe("renderTemplate", ...)` block (around line 19). Delete the entire block.

### Step 33.2 — Add metadata-footer tests

```ts
describe("metadata footer", () => {
  it("appends callback_token + resume metadata to the user prompt", async () => {
    // Construct a runAgent invocation with known userPrompt + callback context.
    // Capture the prompt sent to the CLI runner via a fake cliRunner.
    // Assert: rendered prompt starts with userPrompt, ends with the
    // fixed `---` delimited footer block, contains the expected keys.
  });

  it("emits empty resume_payload/resume_reason when no resume context", async () => {
    // Same shape, but with no resumeContext on the run.
    // Assert footer has callback_token (always) and empty resume_payload/resume_reason.
  });

  it("does not append a footer to the system prompt", async () => {
    // Assert the system prompt passed to the CLI runner equals the
    // input systemPrompt verbatim.
  });
});
```

Use existing test fixtures and patterns from the file as the template (the existing tests have a `fakeCliRunner` or equivalent). If creating new mocks, follow the file's conventions.

### Step 33.3 — Update server.test.ts and http-bridge.test.ts

Sweep for `userdata` in test fixtures. Each test that constructs an ExecuteRequest with `userdata: {...}` switches to `attributes: {...}` with the same field values.

Tests that exercise validation failure on userdata schema mismatch retire (the validator retired in Task 16); replace with attribute-schema-mismatch tests where applicable.

### Step 33.4 — Update lifecycle.e2e.test.ts

Same sweep — userdata in fixtures becomes attributes.

### Step 33.5 — Verify

```
cd executors/claude-agent && npm test && npm run build
```

---

## Task 34 — Other in-tree executors: sweep

**Files:** `executors/http-node/server.go`, `executors/http-node/observability.go`, `executors/verifier-shape-checks/server.go`, `executors/verifier-shape-checks/validation.go`, `executors/verifier-http/*` (sweep), `executors/stub/*` (sweep if any).

### Step 34.1 — http-node

In `executors/http-node/server.go`:
- Line 134: `req.GetUserdata().AsMap()` → `req.GetAttributes().AsMap()`. Same shape — JSON struct unmarshalled to map.
- Sweep for "userdata" in error strings (line 141: `invalid_userdata`, line 151: `userdata.url required`, etc.). Decide whether the error class names should change (`invalid_userdata` → `invalid_attribute`?) — pick consistent with the rest of the codebase. For now, rename the error class strings to `invalid_attribute` since "userdata" no longer exists as a concept.
- Line 240: comment about "buildRequestBody picks the upstream request body. `userdata.body` is an...". Update prose.
- Line 295–304: same.

In `executors/http-node/observability.go`:
- Sweep for advertised schema field — rename `userdata_schema` → `expected_attributes_schema` in the Capabilities response (mechanical).
- Sweep for `@blessed-invariant 11` reference (line ~35).

### Step 34.2 — verifier-shape-checks

In `executors/verifier-shape-checks/server.go` (the `parseChecks` function — `ud["checks"]` read site is at line 111; the error message at line 113 mentions `userdata.checks` and needs updating too):
- Replace `userdata` extraction with `attributes` extraction. The field path inside attributes stays `checks` (per the spec, executor-expected attributes carry the same shape; just sourced from the attribute bag now).

In `executors/verifier-shape-checks/validation.go` (the `validateExecutor` function — `exec.GetUserdata()` calls at lines 84 and 85; error messages at lines 99, 107, 118, 127 reference `userdata.checks`):
- Both `exec.GetUserdata()` calls → `exec.GetAttributes()`.
- Error messages substituting "userdata.checks" → "attributes.checks".

### Step 34.3 — verifier-http and stub

Run `rg 'userdata|GetUserdata|userdata_schema' executors/verifier-http/ executors/stub/`. Address each hit — read site, advertised schema, comments.

### Step 34.4 — Verify

```
go build ./executors/... && go test ./executors/... -count=1
```

---

## Task 35 — Scenario tests

**Files:**
- `test/scenarios/userdata_collapse/static_attributes_test.go` (new)
- `test/scenarios/userdata_collapse/embedded_source_test.go` (new)
- `test/scenarios/userdata_collapse/z_pattern_producer_recovery_test.go` (new)

### Step 35.1 — Static-attribute scenario

Create `test/scenarios/userdata_collapse/static_attributes_test.go`. Pattern after existing scenarios in `test/scenarios/` for setup. Test flow:

1. Register a template with one node, executor = a stub agent, attributes:
   ```yaml
   model:
     type: string
     default: "claude-opus-4-7"
   ```
2. Create an instance with no overrides; dispatch the node.
3. Assert the dispatched ExecuteRequest carries `attributes.model = "claude-opus-4-7"`.
4. Repeat with an instance override:
   ```yaml
   attribute_overrides:
     by_node:
       <node-name>:
         model: "claude-sonnet-4-5"
   ```
5. Assert the dispatched ExecuteRequest carries `attributes.model = "claude-sonnet-4-5"` (L4 wins over L1/L2).

Use `testcontainers-go` Postgres per the existing scenario pattern.

### Step 35.2 — Embedded-source scenario

Create `test/scenarios/userdata_collapse/embedded_source_test.go`. Test flow:

1. Register a template with attributes:
   ```yaml
   user_prompt:
     type: string
     source: "Generate {{params.what}} for {{params.domain | \"unknown\"}}."
   ```
2. Create an instance with `params: {what: "config", domain: "example.com"}`; dispatch.
3. Assert `attributes.user_prompt = "Generate config for example.com."`.
4. Create another instance with `params: {what: "config"}` (no `domain`); dispatch.
5. Assert `attributes.user_prompt = "Generate config for unknown."` (fallback fires).

### Step 35.3 — Z-pattern producer-recovery scenario

Create `test/scenarios/userdata_collapse/z_pattern_producer_recovery_test.go`. Test flow:

1. Register a template with two nodes:
   - `generate-config` (stub executor) — declares attribute `warnings_block` with `source: "{{nodes.verify-config.attribute.warnings_block?}}"`.
   - `verify-config` (stub executor) — declares attribute `warnings_block`, emits it as part of its output. Configured to fail-and-retry on the first run, succeed on the second.
2. Create an instance; trigger dispatch.
3. Assert: first dispatch of `generate-config` sees `attributes.warnings_block = null` (verify hasn't run).
4. `verify-config` runs, fails, emits `warnings_block` as a populated string attribute, propagates cascade.
5. Assert: second dispatch of `generate-config` sees the populated warnings_block.
6. Cycle terminates per `max_retries_without_progress`.

The stub executor is the easiest harness — use the existing scenario-test stub mechanisms.

### Step 35.4 — Verify

```
go test ./test/scenarios/userdata_collapse/... -count=1
```

Docker must be running. Tests use testcontainers-go.

---

## Task 36 — Concept doc updates

**Files:** all under `.ok-planner/design/concepts/` and `.ok-planner/design/tensions/`.

### Step 36.1 — Retire `concepts/userdata.md`

```
mkdir -p .ok-planner/design/concepts/_retired
git mv .ok-planner/design/concepts/userdata.md .ok-planner/design/concepts/_retired/userdata.md
```

Open the moved file. Prepend a new `## Retirement` section to the top (immediately after the front-matter YAML block):

```markdown
## Retirement

2026-05-21 — Userdata retires as a distinct concept. The role userdata played (per-node executor configuration, structurally inert, with template-level and instance-level overrides) is now covered by attributes with `default:` properties (static-default attributes); see `concept:attribute`. Override mechanism renamed: `userdata_overrides` → `attribute_overrides`. Wire field removed: `proto:executor.proto::ExecuteRequest.userdata` is gone. `@blessed-invariant 11` retires. See `.ok-planner/specs/2026-05-20-userdata-collapse-into-attributes-design.md`.
```

Do not modify content below the new section; that's the historical record.

### Step 36.2 — Update `concepts/attribute.md`

Open `.ok-planner/design/concepts/attribute.md`.

Replace the `## What it is` section content (the paragraph from line 11–14 in the existing file) with:

> Attributes are the typed inputs, outputs, and configuration of a node, declared by a JSON Schema in the template's `attributes:` block. Each schema property is one of three shapes: source-bound (`source:` directive resolved at dispatch), static-default (`default:` value resolved at registration), or executor-written (populated at commit by the executor; marked `readOnly: true` in the executor's `expected_attributes_schema`). Persisted writeback lives in `table:rimsky_node_attributes.data`. Validation runs twice (dispatch post-substitution + commit post-writeback).

Update `## Boundaries` first sentence to:

> Owns: the schema, the substitution grammar, the three property shapes, the override merge across the four layers, the two validation gates, the writeback ledger. Does NOT own: claim payload (lives on `claim`), assets (assets are claims, not attributes — see `concept:asset`), semantic validation (the retired `quality-rule` concept; today the verifier-executor pattern covers that surface — see `executors/verifier-shape-checks/`). Adjacent: `node`, `named-event`, `inertness`, `asset`.

Replace the existing per-field-arity invariant (line 50 of the existing file, starts "Per-field `source:` arity is 1...") with:

> Per-field `source:` admits literal text and one or more `{{...}}` directives. Each directive resolves independently against its source kind (`nodes`, `claim`, `params`, `trigger`, `child`). Per-directive strict-default with `?` opt-in to lenient (missing → null); mutually exclusive with `| <literal>` fallback. Multi-source array form (`source: [...]`) and multi-pipe chains (`{{X | Y | Z}}`) are not admitted. Many-to-many fan-in across upstreams lives in the cascade vocabulary (subscriptions over multiple senders, plus optional schema fields whose dispatch-time `ErrMissingSource` is silently omitted at `code:runtime/runner_dispatch.go::substituteAttributesSchema`). Enforced at registration by `code:graph/node/template_validator.go::checkAttributeSource` (rejects the declined forms). The arity asymmetry between subscriptions (many-to-many) and per-field substitution (1:1 per directive) is intentional: subscriptions sum signals across upstreams; substitution names a single value per field. Per-directive composition within a source string concatenates, it does not sum.

Append two new bullets to `## Invariants` (immediately after the per-field-arity invariant):

> Each property has at most one of `source:` or `default:`. Each property satisfies one of: has `source:`, has `default:`, or is marked `readOnly: true` in the executor's `expected_attributes_schema` (executor-write-back populates at commit). Properties failing all three checks are rejected at registration with `template_validation_failed`. Enforced at `code:graph/node/template_validator.go::checkAttributesSchema`.
>
> The template-author L2 declaration cannot set `readOnly: true` on a property the executor's schema does not also mark `readOnly: true`. Rejected at registration. The executor is authoritative on which of its attributes it produces vs consumes.

Replace the `## Non-goals` `Multi-directive fallback chains` bullet (line 36 of the existing file) with:

> **Multi-pipe fallback chains.** A single directive admits at most one `| <literal>` fallback. Multi-directive chains (`{{X | Y | Z}}`) and composite literals (`{}`, `[]` as fallbacks) are not admitted. Per-directive `?` marker and `| <literal>` fallback are mutually exclusive (incoherent: `?` says null on missing, `|` says literal on missing — pick one).

Add a new `## Static-default properties` section between `## Open within this concept` and `## Notes`:

```markdown
## Static-default properties

A schema property declared with `default: <value>` and no `source:` is a static-default property. Its value is set from the effective schema at registration; instance-level overrides (`attribute_overrides.by_executor.<exec>.<attr>` or `attribute_overrides.by_node.<node>.<attr>`) replace the default at dispatch.

Static-default properties replace the role userdata played pre-2026-05-21: per-node executor configuration (model selection, CLI flags, fixed prompts) declared by template authors and overridable by operators at instance time. The substitution grammar does not apply to default values; an operator-supplied `"{{X}}"` in an override is a literal string.

Static-default values are persisted per node-run alongside source-resolved and executor-written values in `table:rimsky_node_attributes.data`, providing dispatch-time forensic clarity. Template-default mutations do not retroactively rewrite history.
```

Append a new entry at the bottom of `## Notes`:

> 2026-05-21 — Userdata collapse. `concept:userdata` retires; its role moves to `default:` properties on the unified attribute schema. Substitution grammar relaxes (embedded text + multi-directive) per `code:graph/node/template_validator.go::checkAttributeSource`. Per-directive strict-default with `?` for lenient. New `checkAttributesSchema` validator enforces the "source or default or executor-write-back" rule. `@blessed-invariant 11` retires. See `.ok-planner/specs/2026-05-20-userdata-collapse-into-attributes-design.md`.

### Step 36.3 — Update `concepts/inertness.md`

Open `.ok-planner/design/concepts/inertness.md`.

In the `## What it is` opening paragraph (line 17), replace `"A uniform discipline applied to six byte streams: userdata, claim scope, claim payload, blob content, named-event payloads, and message payloads (post-2026-05-15)."` with `"A uniform discipline applied to five byte streams: claim scope, claim payload, blob content, named-event payloads, and message payloads (post-2026-05-15)."`.

In the structural-inertness bullet (line 22), replace `"Applies to: userdata, attribute values, named-event payloads, `Error.payload`."` with `"Applies to: attribute values, named-event payloads, `Error.payload`."`.

In `## Invariants` opening sentence (line 34), replace `"Four `@blessed-invariant`s codify the discipline"` with `"Three `@blessed-invariant`s codify the discipline"`.

Remove the `**§11**` bullet (around line 36) entirely. The list reduces to §20, §21, §24.

In `## Boundaries` adjacency list, remove `concept:userdata`.

In `## Auth audit log: verbatim request_params` section (around line 58), update `"rimsky's userdata-inert invariant"` to `"rimsky's structural-inertness discipline"`.

In `## Notes` [2026-05-15] bullet (around line 63), update the parenthetical `"(justified by userdata-inert + claim/payload-inert invariants — no secrets in any control-plane request body)"` to `"(justified by structural-inertness + claim/payload-inert invariants — no secrets in any control-plane request body)"`.

Append a new entry at the bottom of `## Notes`:

> 2026-05-21 — Userdata collapse. `concept:userdata` retires; `@blessed-invariant 11` retires. Attribute-value inertness covered by the structural-inertness discipline. See `.ok-planner/specs/2026-05-20-userdata-collapse-into-attributes-design.md`.

### Step 36.4 — Update `concepts/instance.md`

Open `.ok-planner/design/concepts/instance.md`.

In `## What it is`: replace the description of the `POST /instances` body, changing `userdata_overrides?` to `attribute_overrides? (per-instance per-node attribute fragments)`.

In `## Boundaries`: update the "Owns" line — replace `userdata_overrides` with `attribute_overrides`. Remove `userdata` from the adjacency list.

In `## Invariants`: replace the `userdata_overrides` validation bullet with: `"attribute_overrides validation inspects only routing keys (by_executor/by_node plus executor/node names); fragment values are never inspected (preserves structural-inertness for attribute values)."`.

Add a new `## Notes` section at the end of the file (after `## Open within this concept`):

```markdown
## Notes

2026-05-21 — `userdata_overrides` → `attribute_overrides`. Same merge shape (`by_executor` + `by_node`), applied to attribute values rather than userdata bytes. Persisted as `col:rimsky_instances.attribute_overrides`. See `.ok-planner/specs/2026-05-20-userdata-collapse-into-attributes-design.md`.
```

### Step 36.5 — Update `concepts/validation.md`

Open `.ok-planner/design/concepts/validation.md`.

In `## Definition` (line 11 onwards): the protobuf shape block around line 17 carries `bytes node_userdata = 1; // opaque`. Replace this field with a description of the attribute-bag input — specifically, the merged effective attribute schema and resolved attribute values. Also update the sentence at line 35 (`"Used at template-registration time to give services a say in whether a node's userdata + bindings make sense in their domain."`): change `"userdata + bindings"` to `"attributes + bindings"`.

In `## Boundaries` (line 37 onwards): the sentence at line 39 references `"registration-time pipeline integration (validation_pipeline.go after the static userdata_schema JSON-Schema check)"`. Update to `"registration-time pipeline integration (validation_pipeline.go after the static expected_attributes_schema JSON-Schema check against the merged effective schema)"`.

In `## Invariants` (line 41 onwards): the pipeline-order bullet at line 43 references `"static userdata_schema JSON-Schema check from the executor's Capabilities"`. Update to `"static expected_attributes_schema JSON-Schema check from the executor's ObservabilityCapabilities, applied against the merged effective attribute schema"`.

Remove the `@blessed-invariant 11` bullet at line 45 entirely.

In `## Notes` (line 55 onwards): the existing introduction at line 57 says `"the method name is plain Validate (not ValidateUserdata) because the request carries more than userdata: claim bindings, attribute schemas, sensor config, etc."`. Update: replace `"more than userdata"` with `"more than the executor's expected-attributes schema"` and remove the `ValidateUserdata` parenthetical.

Append a new entry at the bottom of `## Notes`:

> 2026-05-21 — Userdata collapse. Validation pipeline input changes from `node_userdata` bytes to the merged effective attribute set. Schema check now against `expected_attributes_schema` (the executor's contribution to the effective schema). `@blessed-invariant 11` reference removed; attribute-value inertness covered by `concept:inertness`. See `.ok-planner/specs/2026-05-20-userdata-collapse-into-attributes-design.md`.

### Step 36.6 — Update `concepts.md` (TOC)

Open `.ok-planner/design/concepts.md`.

- Remove the `userdata` row entirely.
- Update the `attribute` row's one-line definition to: `"Typed inputs, outputs, and configuration of a node, declared by JSON Schema. Each property is source-bound (rimsky substitution), static-default (resolved at registration), or executor-written (populated at commit). Persisted per-run; overrides via attribute_overrides."`.

### Step 36.7 — Resolve `tensions/userdata-schema-as-opacity-exception.md`

```
mkdir -p .ok-planner/design/tensions/_resolved
git mv .ok-planner/design/tensions/userdata-schema-as-opacity-exception.md .ok-planner/design/tensions/_resolved/userdata-schema-as-opacity-exception.md
```

Open the moved file. Update the front-matter `status: open` to `status: resolved`. Insert a new `## Resolution` section between the existing `## Resolution candidates (do NOT pick)` section and the `## Evidence` section:

```markdown
## Resolution

2026-05-21 — Resolved by userdata collapse. `concept:userdata` retires; `@blessed-invariant 11` retires. The opacity-exception muddiness was specifically about userdata-schema validation being a sanctioned but unnamed exception to the opacity invariant. With userdata gone, the exception is gone. The schema-validation surface that remains (attribute schema validation against the executor's `expected_attributes_schema`) is part of `concept:attribute`'s validation gate discipline, not an exception. See `.ok-planner/specs/2026-05-20-userdata-collapse-into-attributes-design.md`.
```

### Step 36.8 — Verify

```
ls .ok-planner/design/concepts/_retired/ | grep userdata
ls .ok-planner/design/tensions/_resolved/ | grep userdata-schema-as-opacity-exception
rg '^- \[userdata\]' .ok-planner/design/concepts.md && echo "FAIL: userdata row still in TOC" || echo "OK: TOC clean"
```

---

## Task 37 — CHANGELOG entry

**Files:** `CHANGELOG.md`.

### Step 37.1 — Append entry under `## Unreleased`

Open `CHANGELOG.md`. Under the `## Unreleased` heading, append:

```markdown
- **Userdata collapse into attributes.** `concept:userdata` retires; `@blessed-invariant 11` retires. The role userdata played (per-node executor configuration with template + instance overrides) moves to `default:` properties on the unified attribute schema. `proto:executor.proto::ExecuteRequest.userdata` field removed. `ObservabilityCapabilities.userdata_schema` renamed to `expected_attributes_schema`. `col:rimsky_instances.userdata_overrides` renamed to `attribute_overrides`. The attribute-source grammar relaxes to admit embedded text + multiple directives; per-directive strict-default with `?` opt-in to lenient. `code:executors/claude-agent/src/agent-run.ts::renderTemplate` retires; the executor reads source-bound prompt attributes verbatim and appends a fixed metadata footer (callback_token + resume context). Pre-v1 break-freely; no migration shim. See `.ok-planner/specs/2026-05-20-userdata-collapse-into-attributes-design.md`.
```

### Step 37.2 — Verify

```
head -50 CHANGELOG.md | grep -A 2 "Userdata collapse"
```

---

## Task 38 — docs/executors/claude-agent: rename + update

**Files:**
- Old: `docs/executors/claude-agent/userdata.md`
- New: `docs/executors/claude-agent/expected-attributes.md`

### Step 38.1 — Rename

```
git mv docs/executors/claude-agent/userdata.md docs/executors/claude-agent/expected-attributes.md
```

### Step 38.2 — Update content

Sweep the renamed file for `userdata` references. Update to describe:
- The unified attribute-schema shape.
- The `{{...}}` substitution grammar relaxation (embedded text + multi-directive + `?` marker).
- The metadata-footer behavior of the claude-agent executor.
- The deprecation of `{{userdata.X}}` placeholders (no longer recognized — author uses `{{attributes.X}}` substitution at the rimsky layer instead).

The exact prose is the implementer's call; preserve the file's existing structure (purpose, schema fields, examples).

### Step 38.3 — Update any cross-doc references

```
rg "executors/claude-agent/userdata\.md" docs/
```

Any other doc referencing the old path updates.

### Step 38.4 — Verify

```
test -f docs/executors/claude-agent/expected-attributes.md && echo "OK"
test ! -f docs/executors/claude-agent/userdata.md && echo "OK: old path gone"
```

---

## Task 39 — Final verification (full battery)

### Step 39.1 — Build everything

```
go build ./...
```

Must succeed with zero compile errors.

### Step 39.2 — Run all Go tests

```
go test ./... -count=1
```

Must pass.

### Step 39.3 — Race-sensitive paths with `-race`

```
go test ./runtime/... ./foundation/persistence/... ./graph/scheduler/... -race -count=3
```

Must pass.

### Step 39.4 — Lint

```
make lint
```

Must pass.

### Step 39.5 — Scenario tests

```
go test ./test/scenarios/... ./foundation/persistence/... -count=1
```

Docker must be running. Must pass.

### Step 39.6 — TypeScript executor

```
cd executors/claude-agent && npm test && npm run build
```

Must pass.

### Step 39.7 — Proto regeneration check

Re-run `make proto-gen` to confirm the generated bytes are stable:

```
make proto-gen
git diff protocols/proto/v1/gen/
```

`git diff` should show no further changes beyond what Task 6 produced. Any new diff here indicates a `.proto` edit happened after Task 6 without re-running `make proto-gen`.

### Step 39.8 — Final sweep for stale references

```
rg 'userdata' --type-add 'src:*.go' --type-add 'src:*.ts' --type-add 'src:*.proto' --type src 2>/dev/null | grep -v '^test/' | grep -v 'CHANGELOG.md' | grep -v '\.ok-planner/' | grep -v 'docs/.*history' | head -30
```

Review remaining hits. Acceptable: occurrences in concept-doc retirement notes, CHANGELOG history, test fixtures intentionally testing the retired behavior, archived `.ok-planner/history/` content. Unacceptable: any live source code, live concept docs, live executor advertisements, or live API surfaces still emitting "userdata" prose. Address any unacceptable hits.

```
rg '@blessed-invariant 11|invariant 11|blessed-invariant 11' . --type-add 'src:*.go' --type-add 'src:*.ts' --type-add 'src:*.proto' --type src 2>/dev/null | grep -v '^CHANGELOG' | grep -v '\.ok-planner/'
```

Expect zero hits.

```
rg '@concept: userdata' . 2>/dev/null
```

Expect zero hits.

```
rg 'userdata_overrides\|UserdataOverrides' . --type-add 'src:*.go' --type-add 'src:*.ts' --type src 2>/dev/null | grep -v 'CHANGELOG' | grep -v '\.ok-planner/'
```

Expect zero hits.

---

## Manual checks after completion

(None — every verification in this plan is automated. The executor agent can drive the plan to completion without human intervention.)
