# Multi-instance template ergonomics — Implementation Plan

**Spec:** `.ok-planner/specs/2026-05-19-multi-instance-template-ergonomics-design.md`
**Goal:** Ship five quality-of-life improvements to rimsky's template DSL, substitution grammar, CLI, and `stores/postgres/`, plus the design-doc updates that codify them.
**Architecture:** Five items land additively. Item 1 extends `TemplateSpec` with a `Defaults` field and adds a new layer underneath the existing `userdata_overrides` merge. Item 2 adds a YAML-tree resolution pass in the CLI before typed-spec decode. Item 3 introduces a value-returning entry point in the substitution engine alongside the existing string-returning one, and relaxes per-resolver length checks for the kinds that admit a JSON-root pull. Item 4 adds a `tags` field on `TemplateNodeDef`, a new column on `rimsky_nodes`, materialization-time substitution, and a `?tag=` filter. Item 6 fuses the executor protocol into the existing `stores/postgres/` binary using a shared check-compiler package at `stores/shared/sql-checks/`. The Design Changes section of the spec is executed as a bundle of concept-doc and tension-file edits at the end of the plan.
**Tech Stack:** Go 1.x (modules); `gopkg.in/yaml.v3` for YAML decode; `google.golang.org/grpc` for the gRPC server; `jackc/pgx/v5` for Postgres; `modernc.org/sqlite` for SQLite; `github.com/santhosh-tekuri/jsonschema/v5` for schema validation.

This plan is executed against the **rimsky repo root** as the working directory: `/Users/patrick/Documents/projects/research/zonebase/submodules/rimsky/`. All paths below are relative to that root.

---

## Context the implementer needs (read before starting)

This plan touches several files whose shape matters; the implementer should skim them once before starting.

- `foundation/spec/template.go` — `TemplateSpec`, `TemplateNodeDef`. Item 1 adds `Defaults *TemplateDefaults` to `TemplateSpec`; Item 4 adds `Tags []string` to `TemplateNodeDef`.
- `foundation/shared/jsonmerge.go` — `DeepMergeJSON(left, right any) any`. The deep-merge primitive used by Item 1.
- `runtime/userdata_overrides.go` — `applyUserdataOverrides(base, overrides map[string]any, executor, nodeName string, logger shared.Logger) map[string]any`. Item 1 extends this with a new bottom layer for template defaults.
- `graph/attribute/substitution.go` — `Substitute(rawValue string, ctx ResolveContext) (string, error)`, `resolveDirective`, `resolveNodes`, `resolveClaim`, `resolveTrigger`, `walkPath`, `directivePattern`. Item 3 adds `SubstituteValue` and relaxes inner-length checks. The universal guard at `resolveDirective` (rejects `len(parts) < 2`) is NOT relaxed.
- `control/cli/templates.go` — `readSpecFile`. Item 2 adds a resolution pass before `yaml.Unmarshal`.
- `foundation/persistence/nodes.go` — `NodeRow`, `NodeCreateInput`, `NodeTable` interface. Item 4 adds `Tags []string` to the row and create input.
- `foundation/persistence/postgres/nodes.go` and `foundation/persistence/sqlite/nodes.go` — implementations; Item 4 adds `tags` to INSERT and SELECT statements.
- `foundation/persistence/postgres/migrations/001-baseline.sql` and `foundation/persistence/sqlite/migrations/001-baseline.sql` — Item 4 adds a `002-tags.sql` append in each tree.
- `control/controlapi/instances.go` — node materialization at `deps.Persist.Nodes().Create(ctx, persistence.NodeCreateInput{...})` around line 706. Item 4 reads tags from `TemplateNodeDef.Tags`, runs materialization-time substitution, and threads resolved tags into `NodeCreateInput`.
- `control/controlapi/nodes.go` — `GET /instances/{id}/nodes`. Item 4 adds the `?tag=` filter and includes `tags` in the JSON response.
- `graph/node/template_validator.go` — registration validator. Item 4 extends the `params.<key>` shape check (currently around line 892) with a `ParamsSchema` key-existence cross-check.
- `stores/postgres/server/server.go` — the postgres-store gRPC server. Item 6 registers `Executor` alongside `ClaimProducer` at the same site that today registers `LifecycleSubscriber` (line 60-62).
- `stores/postgres/cmd/main.go` — the binary entry. May need an `enable_executor:` config flag depending on Item 6's wiring decision.
- `executors/verifier-shape-checks/checks/checks.go` — reference for the check vocabulary. Item 6's shared package mirrors `runRowCountAbsolute`, `runNoNulls`, `runPKUnique` shape, with SQL-side `no_nulls` adding an optional `threshold` config key.

The spec at `.ok-planner/specs/2026-05-19-multi-instance-template-ergonomics-design.md` is the source of truth for shape decisions. When in doubt, the spec wins.

---

## Item 1 — Template-level userdata defaults

### Task 1.1 — Add the `TemplateDefaults` type to `foundation/spec/template.go`

**Files:** `foundation/spec/template.go`

**Steps:**

1. Add the following types at an appropriate point in `foundation/spec/template.go` (e.g. immediately after `TemplateSpec`, before `TemplateNodeDef`):

   ```go
   // TemplateDefaults declares template-author baselines applied
   // underneath per-node userdata and per-instance overrides. See
   // concept:userdata's per-instance-overrides section for the
   // four-layer merge order.
   //
   // @blessed-invariant 11: validation inspects only routing keys
   // (`by_executor` plus executor names), never fragment values.
   type TemplateDefaults struct {
       Userdata *TemplateUserdataDefaults `yaml:"userdata,omitempty" json:"userdata,omitempty"`
   }

   // TemplateUserdataDefaults carries per-executor userdata baselines.
   // Only by_executor is supported; per-node defaults are expressed by
   // declaring userdata: on the node itself.
   type TemplateUserdataDefaults struct {
       ByExecutor map[string]map[string]any `yaml:"by_executor,omitempty" json:"by_executor,omitempty"`
   }
   ```

2. Add the `Defaults` field to `TemplateSpec` (preserve all existing fields and their ordering otherwise):

   ```go
   // Defaults holds template-author userdata baselines, merged
   // underneath per-node userdata and per-instance overrides per
   // concept:userdata's documented order.
   Defaults *TemplateDefaults `yaml:"defaults,omitempty" json:"defaults,omitempty"`
   ```

**Verification:**

```bash
go build ./foundation/spec/...
```

### Task 1.2 — Extend `applyUserdataOverrides` to layer template defaults underneath

**Files:** `runtime/userdata_overrides.go`, `runtime/userdata_overrides_test.go`

**Steps:**

1. Read `runtime/userdata_overrides.go::applyUserdataOverrides`. The current signature is `applyUserdataOverrides(base map[string]any, overrides map[string]any, executor string, nodeName string, logger shared.Logger) map[string]any` and the merge order is `base → by_executor[executor] → by_node[nodeName]` (more specific wins).

2. Change the signature to accept `templateDefaults map[string]any` as a new first argument:

   ```go
   func applyUserdataOverrides(
       templateDefaults map[string]any, // template.defaults.userdata.by_executor[executor]; may be nil
       base map[string]any,             // node.userdata
       overrides map[string]any,        // instance.userdata_overrides
       executor string,
       nodeName string,
       logger shared.Logger,
   ) map[string]any
   ```

3. At the top of the body, if `templateDefaults` is non-nil, fold it under `base` via `DeepMergeJSON`:

   ```go
   merged := any(templateDefaults)
   if base != nil {
       merged = shared.DeepMergeJSON(merged, base)
   } else if templateDefaults == nil {
       // Both nil — fall through to existing overrides logic with base=nil.
   }
   // Replace existing `merged := any(base)` initialization with `if merged == nil { merged = any(base) }`
   // (or restructure so that templateDefaults-base becomes the new starting layer).
   ```

   Then preserve the existing `by_executor` / `by_node` override layering on top of this new merged starting point. The final return type stays `map[string]any` (clone via `DeepMergeJSON(result, nil)` on the no-override fast path, matching existing behavior).

4. Add the substituted "fast path" handling for the case where all three input layers (`templateDefaults`, `base`, `overrides`) are empty: return an empty `map[string]any{}` (or a clone of base if base is non-nil).

**Verification:**

```bash
go build ./runtime/...
```

### Task 1.3 — Update existing callers of `applyUserdataOverrides`

**Files:** `runtime/runner_acquire.go`, `runtime/runner_dispatch.go`, plus any other caller; also any test files

**Steps:**

1. Find all callers:

   ```bash
   rg 'applyUserdataOverrides\(' --type=go
   ```

2. The natural data source for `templateDefaults` is the dispatched node's bound template's `Defaults.Userdata.ByExecutor[executor]`. The runtime path already fetches the bound template at `runtime/runner_acquire.go::acquisition` (`tmpl := lookupTemplate(...)` around line 370). Pick this approach for plumbing:

   - Add a `TemplateDefaults map[string]any` field to the `acquisition` struct in `runtime/runner_acquire.go` (or whatever struct already carries `NodeDef` and is passed to the dispatcher).
   - Populate it at the point where `tmpl` is fetched: `if tmpl.Defaults != nil && tmpl.Defaults.Userdata != nil { acq.TemplateDefaults = tmpl.Defaults.Userdata.ByExecutor[executor] }` (where `executor` is the dispatched node's executor name). If any of the path is nil, leave `TemplateDefaults` as nil.
   - At `runtime/runner_dispatch.go` (where `applyUserdataOverrides` is called), pass `acq.TemplateDefaults` as the new first argument.

3. For test files that construct fake `acquisition` values, the `TemplateDefaults` field defaults to nil — existing test fixtures pass through unchanged. New tests for the four-layer merge (Task 1.4) set the field explicitly.

4. Do NOT re-look up the template at dispatch time (that would be an extra DB hit on a hot path). The acquisition struct is the right plumbing site.

**Verification:**

```bash
go build ./...
```

### Task 1.4 — Write failing test for the four-layer merge order

**Files:** `runtime/userdata_overrides_test.go`

**Steps:**

1. Add a test `TestApplyUserdataOverrides_TemplateDefaultsLayered` that covers:
   - `templateDefaults` applied when base, overrides are nil.
   - `node.userdata` beats `templateDefaults` on key collision.
   - `instance.userdata_overrides.by_executor` beats `templateDefaults` on key collision.
   - `instance.userdata_overrides.by_node` beats `instance.userdata_overrides.by_executor` (existing behavior, regression-check).
   - Nested objects deep-merge across all four layers.
   - Arrays at any layer replace (do not concatenate).

2. Run the test, confirm it fails (or passes) in line with the current implementation.

**Verification:**

```bash
go test ./runtime/ -run TestApplyUserdataOverrides_TemplateDefaultsLayered
```

If the implementation from Task 1.2 is correct, the test passes. If it fails, fix the implementation, not the test.

### Task 1.5 — Add registration validation for `defaults.userdata.by_executor.<name>`

**Files:** `graph/node/template_validator.go` (or wherever template-registration validation lives — find via `rg 'validateTemplate' --type=go`)

**Steps:**

1. Find the registration-validation entry point. Reads `TemplateSpec` and produces validation errors.

2. Add a validation pass: for each key `<name>` under `TemplateSpec.Defaults.Userdata.ByExecutor`, check that `<name>` matches at least one node's `Executor` (in `TemplateSpec.Nodes` or the equivalent post-2026-05-15 graphs structure). If no match, emit a validation error citing the unknown executor name.

3. The check inspects only the routing key (`<name>`), never the fragment values under it. This preserves `@blessed-invariant 11`.

**Verification:**

```bash
go test ./graph/node/...
```

### Task 1.6 — Write failing test for the registration validator's defaults check

**Files:** `graph/node/template_validator_test.go` (or sibling)

**Steps:**

1. Add a test that registers a `TemplateSpec` with `Defaults.Userdata.ByExecutor["unknown-executor"]` set, while no node has `Executor: "unknown-executor"`. Expect the validator to reject with a precise error.

2. Add a positive test where `Defaults.Userdata.ByExecutor["claude-agent"]` is set and at least one node has `Executor: "claude-agent"`. Expect the validator to accept.

3. Add a test that fragment values inside `ByExecutor` are not inspected: stuff arbitrary garbage in there; the validator should still accept (because it only checks routing keys).

**Verification:**

```bash
go test ./graph/node/ -run TestTemplateValidator_DefaultsByExecutor
```

---

## Item 2 — `source_file:` references in templates

### Task 2.1 — Add `resolveSourceFileRefs` to the CLI

**Files:** `control/cli/templates.go`

**Steps:**

1. Read the current `readSpecFile` function. It does `os.ReadFile(path)` then `yaml.Unmarshal(raw, &spec)`.

2. Add a new helper, alongside `readSpecFile`:

   ```go
   // resolveSourceFileRefs walks an already-decoded YAML tree (typed as
   // map[string]any, possibly containing []any) and replaces every
   // object of the exact shape {source_file: "<path>"} with the
   // referenced file's text content as a plain string. Path resolution
   // is relative to baseDir; resolved paths that escape baseDir (via ..)
   // are rejected. Absolute paths are rejected. Returns the transformed
   // tree.
   //
   // Single-pass: a file's contents are inlined as plain text and are
   // NOT re-walked for further source_file references.
   func resolveSourceFileRefs(node any, baseDir string) (any, error)
   ```

3. Implementation outline:
   - Recursive walk over `map[string]any` and `[]any`.
   - At each `map[string]any`, check if it has exactly one entry with key `source_file` and a string value. If so, resolve the path (see step 4), read the file, return the file's content as a `string`.
   - Otherwise recurse into each value (for maps) or each element (for slices).
   - For non-container values, return as-is.

4. Path resolution:
   - Reject if the input path starts with `/` (absolute). Return an error with a clear message.
   - Use `filepath.Join(baseDir, inputPath)` then `filepath.Clean`.
   - Compute `rel, err := filepath.Rel(baseDir, cleaned)`. If `err != nil` or `rel` starts with `..` or is `..`, reject.
   - `os.ReadFile(cleaned)`. If error (including file-not-exist), wrap with a clear message naming the input path.

5. Update `readSpecFile` to call `resolveSourceFileRefs` between read and unmarshal:

   ```go
   func readSpecFile(path string) (node.TemplateSpec, error) {
       raw, err := os.ReadFile(path)
       if err != nil {
           return node.TemplateSpec{}, err
       }
       // Decode to generic structure first so we can resolve source_file
       // references before typed-spec decode.
       var generic any
       if err := yaml.Unmarshal(raw, &generic); err != nil {
           return node.TemplateSpec{}, fmt.Errorf("parse %s: %w", path, err)
       }
       baseDir := filepath.Dir(path)
       resolved, err := resolveSourceFileRefs(generic, baseDir)
       if err != nil {
           return node.TemplateSpec{}, fmt.Errorf("resolve source_file in %s: %w", path, err)
       }
       // Re-marshal and decode into typed spec.
       resolvedBytes, err := yaml.Marshal(resolved)
       if err != nil {
           return node.TemplateSpec{}, fmt.Errorf("marshal resolved %s: %w", path, err)
       }
       var spec node.TemplateSpec
       if err := yaml.Unmarshal(resolvedBytes, &spec); err != nil {
           return node.TemplateSpec{}, fmt.Errorf("parse %s: %w", path, err)
       }
       return spec, nil
   }
   ```

**Verification:**

```bash
go build ./control/cli/...
```

### Task 2.2 — Write failing tests for `resolveSourceFileRefs`

**Files:** `control/cli/templates_test.go` (or sibling)

**Steps:**

1. Use `t.TempDir()` to create test directories with template YAMLs and prompt files.

2. Test cases (each as its own subtest):
   - **Simple inline:** YAML with `system_prompt: { source_file: "prompts/foo.md" }`, file exists. Resolved spec has `system_prompt: "<file content>"`.
   - **Nested inside userdata:** the source_file ref appears under `nodes[0].userdata.cli.system_prompt`.
   - **Nested inside attribute schema:** the source_file ref appears under `nodes[0].attributes.schema.description`.
   - **Multiple files in one template:** two refs resolve independently.
   - **Missing file:** error message names the input path.
   - **Path escape (`../../etc/passwd`):** error names the escape attempt.
   - **Absolute path (`/etc/passwd`):** rejected.
   - **No source_file ref at all:** tree returned unchanged (regression check).
   - **`source_file` with siblings:** e.g. `{source_file: "x", foo: "bar"}` — NOT recognized as a ref; left alone (the rule is "exactly one key").
   - **`source_file` with non-string value:** `{source_file: 42}` — NOT recognized as a ref; left alone (the value must be a string).

3. Each test runs `resolveSourceFileRefs` directly on the decoded YAML and asserts on the transformed tree, OR runs `readSpecFile` on a temp YAML and asserts on the resulting `TemplateSpec`.

**Verification:**

```bash
go test ./control/cli/ -run TestResolveSourceFileRefs
```

### Task 2.3 — End-to-end test: register, GET back, hash stability

**Files:** existing CLI/integration test directory (find via `rg 'func TestRegisterTemplate' --type=go`)

**Steps:**

1. Write a test that:
   - Creates a template YAML with two `source_file:` refs pointing at sibling files.
   - Registers the template via the CLI test helper.
   - Calls `GET /templates/{hash}` (or the equivalent client function) and asserts the returned spec carries the resolved bytes, not the references.

2. Hash-stability test: create two templates whose source files have identical contents. Register both. Assert the returned hashes are identical (per content-addressing).

3. Hash-update test: edit one of the source files. Re-register. Assert the new hash differs from the original.

**Verification:**

```bash
go test ./control/cli/... -run TestTemplateSourceFile
```

---

## Item 3 — Whole-directive value lift in substitution

### Task 3.1 — Add `SubstituteValue` entry point

**Files:** `graph/attribute/substitution.go`

**Steps:**

1. Read the existing `Substitute` function and the helpers it calls (`resolveDirective`, `resolveNodes`, `resolveClaim`, `resolveTrigger`, `resolveChild`, `resolveParams`, `walkPath`, `stringify`, `stringifyRaw`, `directivePattern`).

2. Add a value-returning helper that resolves one directive's content to its raw JSON value:

   ```go
   // resolveDirectiveValue is the value-returning sibling of resolveDirective.
   // Resolves the directive's content (the bytes between `{{` and `}}`)
   // to a Go value matching the directive's leaf:
   //   - object → map[string]any
   //   - array  → []any
   //   - string → string
   //   - number → float64
   //   - bool   → bool
   //
   // Returns ErrMissingSource for unresolved references, unknown kinds,
   // or JSON nulls along the path (consistent with walkPath's existing
   // null-as-missing behavior at lines 434, 439).
   func resolveDirectiveValue(directive string, ctx ResolveContext) (any, error)
   ```

   Implementation: refactor `resolveDirective` into a private core that returns `(any, error)`, with `resolveDirective` (the string-returning version) wrapping it via `stringify` for primitives or `json.Marshal` for composites (matching current behavior). The new `resolveDirectiveValue` is the value-returning entry to the core.

3. Add the public entry point:

   ```go
   // SubstituteValue is the value-returning sibling of Substitute. When
   // the input is exactly one {{...}} directive (modulo whitespace), the
   // resolved JSON value is returned as-is. Otherwise, the input is
   // treated as an embedded-mode template: each directive's resolution
   // is stringified and concatenated, and the result is a Go string.
   //
   // The discriminator is the input string's shape, not the directive's
   // kind or the resolved value's type.
   //
   // Returns:
   //   - (any, nil) on success — caller must type-assert based on context.
   //   - (nil, ErrMissingSource) when a required source cannot resolve.
   //   - (nil, error) for malformed directives or schema-level errors.
   func SubstituteValue(rawValue string, ctx ResolveContext) (any, error) {
       trimmed := strings.TrimSpace(rawValue)
       if directivePattern.FindString(trimmed) == trimmed && trimmed != "" {
           // Whole-directive mode: the trimmed input is exactly one directive.
           inside := strings.TrimSpace(trimmed[2 : len(trimmed)-2])
           if inside == "" {
               return nil, &ErrMissingSource{Directive: inside, Reason: "empty directive"}
           }
           return resolveDirectiveValue(inside, ctx)
       }
       // Embedded mode: fall through to the existing string-returning path.
       s, err := Substitute(rawValue, ctx)
       if err != nil {
           return nil, err
       }
       return s, nil
   }
   ```

4. Keep `Substitute` unchanged (existing callers continue to get strings). A separate task sweeps call sites that should switch to `SubstituteValue`; the plan defers that decision to Task 3.5.

**Verification:**

```bash
go build ./graph/attribute/...
```

### Task 3.2 — Relax the inner length checks in `resolveNodes`, `resolveClaim`, `resolveTrigger`

**Files:** `graph/attribute/substitution.go`

**Steps:**

1. **`resolveNodes` (attribute kind branch):** change `fieldPath := rest[2:]` to be valid even when `len(rest) == 2` (i.e. `fieldPath = []string{}`). The current `len(rest) < 3` guard at the top of the function rejects this case; relax it to `len(rest) < 2` (since the minimum is now `<node>.attribute`). Inside the `attribute` switch arm, also drop the explicit minimum-length check if any.

2. **`resolveNodes` (event kind branch):** the current minimum is `len(rest) < 4` (i.e. `<node>.event.<name>.<field>`). Relax to `len(rest) < 3` (i.e. allow `<node>.event.<name>` with empty field path).

3. **`resolveClaim`:**
   - For the `payload` branch: current guard is `len(rest) < 3` (i.e. `<alias>.payload.<field>`). Relax to allow `len(rest) == 2` (just `<alias>.payload`). When `len(rest) == 2`, `walkPath(cr.Payload, []string{})` returns the payload root.
   - `address` and `scope` already take no trailing path; unchanged.

4. **`resolveTrigger`:** current guard is `len(rest) < 3` (`message.payload.<field>`). Relax to allow `len(rest) == 2` (just `message.payload`).

5. **`resolveParams`, `resolveChild`:** unchanged.
   - `resolveParams` already requires `len(rest) >= 1` (i.e. `params.<key>`). The universal `len(parts) < 2` guard at `resolveDirective#202` rejects bare `params` (1 part). This is intentional per the spec; do NOT relax the universal guard.
   - `resolveChild` only accepts `child.partition_key` (single field); unchanged.

6. Each branch's empty-trailing-path case routes through `walkPath` with an empty `path` slice; `walkPath` already returns the JSON root cleanly in that case.

**Verification:**

```bash
go build ./graph/attribute/...
```

### Task 3.3 — Tests for `SubstituteValue` whole-directive mode

**Files:** `graph/attribute/substitution_test.go`

**Steps:**

1. Add tests `TestSubstituteValue_WholeDirective` with subtests for each leaf JSON type:
   - **Object:** `{{nodes.X.attribute}}` against `Deps["X"] = json.RawMessage(\`{"a": 1, "b": [2,3]}\`)`. Expect `map[string]any{"a": float64(1), "b": []any{float64(2), float64(3)}}`.
   - **Array:** `{{nodes.X.attribute.list}}` against an attribute with a list field. Expect `[]any{...}`.
   - **String:** `{{params.region}}` against `params: {"region": "us-west"}`. Expect `"us-west"` (string).
   - **Number:** `{{params.count}}` against `params: {"count": 42}`. Expect `float64(42)`.
   - **Bool:** `{{params.enabled}}` against `params: {"enabled": true}`. Expect `true`.
   - **Whitespace tolerated:** `"   {{params.region}}\n"` resolves to `"us-west"`.

2. Test embedded mode falls through to `Substitute`:
   - `"prefix-{{params.region}}-suffix"` → `"prefix-us-west-suffix"`.
   - `"{{a}}{{b}}"` against params with `a=1, b=2` → `"12"` (string).

3. Test `{{params}}` (no field path) returns `ErrMissingSource` per the universal `len(parts) < 2` guard. Cite the rationale in a comment on the test.

4. Test null-as-missing: a directive whose path resolves to a JSON null returns `ErrMissingSource` (existing `walkPath` behavior, unchanged).

**Verification:**

```bash
go test ./graph/attribute/ -run TestSubstituteValue_WholeDirective
```

### Task 3.4 — Tests for empty-trailing-path bare-form pulls

**Files:** `graph/attribute/substitution_test.go`

**Steps:**

1. Add tests `TestSubstituteValue_BareForm` with subtests:
   - `{{nodes.X.attribute}}` returns the whole attribute object (already covered in Task 3.3; verify here too).
   - `{{claim.alias.payload}}` returns the whole payload object.
   - `{{nodes.X.event.eventname}}` returns the whole named-event payload.
   - `{{trigger.message.payload}}` returns the whole trigger message payload.
   - `{{params}}` returns `ErrMissingSource` (NOT a bare-form admission; explicit per the spec).

2. Each bare-form test uses opaque JSON bytes for the relevant `ResolveContext` field and asserts the returned `any` matches the expected decoded shape.

**Verification:**

```bash
go test ./graph/attribute/ -run TestSubstituteValue_BareForm
```

### Task 3.5 — Decide on `Substitute` vs `SubstituteValue` migration

**Files:** every caller of `Substitute`

**Steps:**

1. Find all callers:

   ```bash
   rg '\.Substitute\(|attributes\.Substitute\(|attribute\.Substitute\(' --type=go
   ```

2. Decision rule: if the caller's downstream use is "set a string field on a schema-typed value," keep using `Substitute`. If the caller's downstream use is "set a typed value (possibly object/array/number/bool) on a schema property," switch to `SubstituteValue`.

3. The dominant call site that needs to switch is the attribute-schema evaluator — the code that walks `NodeAttributesDef.Schema`, finds `source:` directives, and assigns resolved values into the attribute data map. This caller should produce a typed value, not a string, so it switches to `SubstituteValue`.

4. Update call sites accordingly. Where unsure, prefer `SubstituteValue` (it handles both modes correctly).

**Verification:**

```bash
go build ./...
go test ./graph/attribute/...
```

### Task 3.6 — Sweep the existing substitution tests for the behavior change

**Files:** `graph/attribute/substitution_test.go`, any other `*_test.go` that asserts on substitution output

**Steps:**

1. Find tests that today assert on string forms of numeric/bool/object substitution results:

   ```bash
   rg 'Substitute\(' --type=go --files-with-matches | xargs rg -l '"42"|"true"|"false"' 2>/dev/null
   ```

2. For each such test, decide if it's covering whole-directive mode (input was just `"{{...}}"`) or embedded mode (input had surrounding text). Whole-directive tests need to be updated to expect the lifted JSON value; embedded-mode tests stay as-is.

3. The dispatch-time schema-validation paths may need adjustment too: a test that today validates `{count: "42"}` against `{type: integer}` and passes via JSON Schema coercion now produces `{count: 42}` and validates without coercion. Either is fine; the test assertions just need to match the new shape.

**Verification:**

```bash
go test ./graph/attribute/... ./runtime/... ./control/...
```

If any test fails because of the new whole-directive lift, the test was relying on stringified coercion and needs updating per step 2.

### Task 3.7 — Dispatch-time schema-validation tests for whole-object pull

**Files:** `graph/attribute/validate_test.go` (or sibling)

**Steps:**

1. Add a test that uses `SubstituteValue` to produce a whole-object value for a property whose schema is `{type: object, properties: {a: {type: integer}}}`. Validate the resulting populated attribute data. Expect success.

2. Add a test where the upstream attribute's resolved type doesn't match the receiver schema (e.g. upstream is a string, receiver expects an object). Validate; expect `ErrSchemaValidation`.

**Verification:**

```bash
go test ./graph/attribute/ -run TestValidate_WholeDirectiveLift
```

---

## Item 4 — Node-level tags

### Task 4.1 — Add `Tags []string` to `TemplateNodeDef`

**Files:** `foundation/spec/template.go`

**Steps:**

1. Add the field to `TemplateNodeDef`:

   ```go
   // Tags is operator-facing metadata: free-form strings used for
   // filtering at the dashboard / events surface. Tag values admit
   // `{{params.<key>}}` substitution at materialization time. Tags
   // do not gate dispatch, cascade, or validation.
   //
   //  @concept: node
   Tags []string `yaml:"tags,omitempty" json:"tags,omitempty"`
   ```

**Verification:**

```bash
go build ./foundation/spec/...
```

### Task 4.2 — Postgres migration: add `tags` column to `rimsky_nodes`

**Files:** `foundation/persistence/postgres/migrations/002-tags.sql` (NEW)

**Steps:**

1. Create the file with:

   ```sql
   -- 002-tags.sql
   -- Per spec .ok-planner/specs/2026-05-19-multi-instance-template-ergonomics-design.md
   -- Item 4: Operator-facing tags on rimsky_nodes.

   ALTER TABLE rimsky_nodes
       ADD COLUMN tags TEXT[] NOT NULL DEFAULT '{}';

   CREATE INDEX rimsky_nodes_tags_idx ON rimsky_nodes USING GIN (tags);
   ```

2. Update `foundation/persistence/postgres/migrations/embed.go` if it requires manual listing of migrations. Check via:

   ```bash
   cat foundation/persistence/postgres/migrations/embed.go
   ```

   If the embed uses `//go:embed *.sql`, no change needed. If it lists files explicitly, add `002-tags.sql`.

**Verification:**

```bash
go build ./foundation/persistence/postgres/...
```

### Task 4.3 — SQLite migration: add `tags` column to `rimsky_nodes`

**Files:** `foundation/persistence/sqlite/migrations/002-tags.sql` (NEW)

**Steps:**

1. Create the file with:

   ```sql
   -- 002-tags.sql
   -- Per spec .ok-planner/specs/2026-05-19-multi-instance-template-ergonomics-design.md
   -- Item 4: Operator-facing tags on rimsky_nodes.
   -- SQLite stores the array as JSON-encoded TEXT, following the convention
   -- documented at 001-baseline.sql#17 (sibling arrays: accepted_stores #116,
   -- required_stores #134).

   ALTER TABLE rimsky_nodes
       ADD COLUMN tags TEXT NOT NULL DEFAULT '[]';
   ```

2. SQLite does not support indexing JSON arrays efficiently; no GIN equivalent. Skip the index on sqlite.

3. Update `foundation/persistence/sqlite/migrations/embed.go` if needed (same check as Task 4.2).

**Verification:**

```bash
go build ./foundation/persistence/sqlite/...
```

### Task 4.4 — Extend `NodeRow` and `NodeCreateInput`

**Files:** `foundation/persistence/nodes.go`

**Steps:**

1. Add `Tags []string` to `NodeRow`:

   ```go
   Tags []string `json:"tags"`
   ```

   (No `omitempty` — the field is always emitted; empty array means "no tags," not "unknown.")

2. Add `Tags []string` to `NodeCreateInput`:

   ```go
   Tags []string
   ```

**Verification:**

```bash
go build ./foundation/persistence/...
```

### Task 4.5 — Update postgres and sqlite `NodeTable` implementations

**Files:** `foundation/persistence/postgres/nodes.go`, `foundation/persistence/sqlite/nodes.go`

**Steps:**

1. **Postgres (`foundation/persistence/postgres/nodes.go`):**
   - Update the INSERT statement in `Create` to include `tags`. Use `pgx`'s native array binding: `pgtype.FlatArray[string]` or pass `[]string` directly (pgx v5 handles `[]string` against `TEXT[]`).
   - Update every SELECT in `Get`, `ListByInstance`, `ListByInstancePaged`, `ListRunning`, `ListReadyForDispatch`, `ListRunningBySupervisor`, `ListWithStaleHeartbeat`, `ListPureCascadeReady` to include `tags` in the column list.
   - Update the row-scanning helper that maps Postgres rows to `NodeRow` to scan `tags` into `NodeRow.Tags`.

2. **SQLite (`foundation/persistence/sqlite/nodes.go`):**
   - Same pattern as postgres, with the tags column stored/scanned as a JSON-encoded TEXT. Use the same JSON-encoding helper that handles `accepted_stores` and `required_stores` (find via `rg 'accepted_stores' foundation/persistence/sqlite/`).
   - Marshal `[]string` to JSON on insert; unmarshal JSON string to `[]string` on scan.

3. Conformance tests under `foundation/persistence/conformance/` may also need updates — check for existing tests that touch `NodeRow` and add tags-coverage variants:

   ```bash
   rg 'NodeRow|NodeCreateInput' foundation/persistence/conformance/
   ```

**Verification:**

```bash
go build ./foundation/persistence/...
go test ./foundation/persistence/postgres/... ./foundation/persistence/sqlite/... -count=1
```

The conformance tests use testcontainers-go and require Docker — confirm Docker is running before testing the postgres path.

### Task 4.6 — Add `Tags` substitution at instance creation

**Files:** `control/controlapi/instances.go`, possibly a new helper file

**Steps:**

1. Read the instance-creation handler around line 706 (`deps.Persist.Nodes().Create(ctx, persistence.NodeCreateInput{...})`). It currently materializes nodes from `TemplateSpec.Nodes` (or the graphs equivalent) and writes one `rimsky_nodes` row per template node.

2. Before calling `Create`, for each node:
   - Read `nodeDef.Tags` (the raw, possibly-templated tag strings).
   - Build a `ResolveContext` with only `Params` populated. Set every other field (`Deps`, `Claim`, `EventLookup`, `TriggerMessagePayload`, `ChildPartitionKey`) to its nil/zero value. The params bytes are the instance's `params` blob.
   - For each raw tag, call `attributes.SubstituteValue(rawTag, ctx)`. The resolved value must be a string:
     - If `SubstituteValue` returns a non-string lifted value (e.g. an object), fail instance creation with a typed control-api error citing the tag and the resolved Go type.
     - If `SubstituteValue` returns `ErrMissingSource`, fail instance creation with a typed error citing the directive.
   - Collect the resolved strings into `[]string` and assign to `NodeCreateInput.Tags`.

3. Wrap the substitution loop in a helper for testability:

   ```go
   // resolveNodeTags resolves a node's tag strings against the instance's
   // params. Returns the resolved tags or an error citing which tag /
   // which directive failed. Per spec §Item 4 — substitution scope is
   // params-only at materialization time.
   func resolveNodeTags(rawTags []string, paramsBytes json.RawMessage) ([]string, error)
   ```

4. The control-api error surface should distinguish "missing param" from "non-string lift" — both are operator-actionable. Map both to `400 Bad Request` with a structured body.

**Verification:**

```bash
go test ./control/controlapi/ -run TestInstanceCreate_TagSubstitution
```

(Test added in Task 4.8.)

### Task 4.7 — Tag-validation extension in template-registration validator

**Files:** `graph/node/template_validator.go`

**Steps:**

1. Find the existing `params.<key>` placeholder shape check (around line 892 per the spec; verify with `rg 'params directive' graph/node/`).

2. Walk every node's `Tags`. For each tag string, extract any `{{params.<key>}}` directives using the existing directive-pattern regex.

3. For each extracted `<key>`, look it up in `TemplateSpec.ParamsSchema` (a JSON Schema map). The schema's `properties` map is the source of declared param keys. If `<key>` is not in `properties`, emit a validation error citing the tag, the directive, and the unknown key.

4. The spec also calls out that this extension can apply symmetrically to substitution refs in attribute schemas. Decision left to implementer: if the existing attribute-schema validator already does this cross-check, no extension needed there; if not, extending it is optional in this cycle. Plan instruction: implement the cross-check for tags only in this task; do not expand to attribute schemas unless straightforward.

5. Other substitution kinds (`claim.<...>`, `nodes.<...>`, `trigger.<...>`, etc.) referenced in a tag string should be rejected at registration — tags only admit `params.<key>` substitution (per spec §Item 4). The registration validator emits an error citing the unsupported kind.

**Verification:**

```bash
go test ./graph/node/ -run TestTemplateValidator_Tags
```

(Test added in Task 4.9.)

### Task 4.8 — Tests for instance-creation tag substitution

**Files:** `control/controlapi/instances_test.go` (or sibling)

**Steps:**

1. Test cases:
   - **Static tag:** template has `tags: [setup, recurring]`, instance materializes with the same tags.
   - **Substituted tag:** template has `tags: ["domain:{{params.domain}}"]`, instance with `params: {domain: "alpha.example.com"}` materializes with `tags: ["domain:alpha.example.com"]`.
   - **Whole-directive lift, string param:** template has `tags: ["{{params.region}}"]`, params `{region: "us-west"}`, materializes with `tags: ["us-west"]`.
   - **Whole-directive lift, non-string param:** template has `tags: ["{{params.config}}"]`, params `{config: {a: 1}}`. Materialization fails with a typed error citing the tag and Go type.
   - **Missing param:** template has `tags: ["{{params.missing}}"]`, params `{}`. Materialization fails with `ErrMissingSource`-surfaced error.

**Verification:**

```bash
go test ./control/controlapi/ -run TestInstanceCreate_TagSubstitution
```

### Task 4.9 — Tests for tag-validation at template registration

**Files:** `graph/node/template_validator_test.go`

**Steps:**

1. Test cases:
   - **Valid:** template with `tags: ["{{params.domain}}"]` and `params_schema: {properties: {domain: ...}}` — accepted.
   - **Unknown param key:** template with `tags: ["{{params.unknown}}"]` and `params_schema: {properties: {domain: ...}}` — rejected with error citing the tag and `unknown` key.
   - **Unsupported kind in tag:** `tags: ["{{claim.staging.address}}"]` — rejected with error citing the unsupported kind.
   - **Plain string tag:** `tags: ["setup"]` — accepted (no directive to validate).

**Verification:**

```bash
go test ./graph/node/ -run TestTemplateValidator_Tags
```

### Task 4.10 — `?tag=` filter on `GET /instances/{idOrKey}/nodes`

**Files:** `control/controlapi/nodes.go`

**Steps:**

1. Find the route handler for `GET /instances/{idOrKey}/nodes` (registered around line 87 of `control/controlapi/nodes.go`; the path parameter is `idOrKey`, accepting both the UUID and the instance_key). It currently parses pagination / state filters and calls `ListByInstancePaged` (or similar).

2. Add support for a `tag` query parameter:

   ```go
   tag := r.URL.Query().Get("tag")
   ```

3. If `tag != ""`, apply server-side filtering. Two options depending on how the persistence layer is structured:
   - **Add a parameter to `ListByInstancePaged`:** e.g. `ListByInstancePaged(ctx, instanceID, pag ListPagination, tagFilter string, tx Tx)`. Implementation: postgres uses `WHERE ... AND '<tag>' = ANY(tags)`, sqlite uses a JSON-containment check (or fetch and filter in-memory if simple).
   - **Filter in the handler in-memory:** load the page, then drop rows where `tag` isn't in `row.Tags`. Simpler but interacts badly with pagination.

   Prefer the first option (DB-side filter). Add a new method or extend the existing one with an optional filter parameter.

4. The JSON response shape already includes the row via `NodeRow`'s JSON tags; since `Tags` is now a field, it appears in the response automatically. Verify with a test (next task).

**Verification:**

```bash
go test ./control/controlapi/ -run TestListNodes_TagFilter
```

### Task 4.11 — Tests for the `?tag=` filter and JSON-response shape

**Files:** `control/controlapi/nodes_test.go` (or sibling)

**Steps:**

1. Test cases:
   - **No filter:** all nodes returned, each carrying its `tags` array (including empty arrays).
   - **Filter match:** `?tag=setup` returns only nodes whose tags contain `setup`.
   - **Filter no-match:** `?tag=nonexistent` returns an empty result set.
   - **Filter combines with state:** `?tag=setup&state=fresh` applies both.

**Verification:**

```bash
go test ./control/controlapi/ -run TestListNodes_TagFilter
```

### Task 4.12 — Migration test: existing rows get empty tags

**Files:** `foundation/persistence/postgres/migrations_test.go` (or sibling); same for sqlite

**Steps:**

1. Test that after applying migration `002-tags.sql` to a database with pre-existing `rimsky_nodes` rows (zero rows is fine for a fresh test DB), the new column is populated with the default `'{}'` (postgres) or `'[]'` (sqlite).

2. Also test that the index on `rimsky_nodes.tags` exists post-migration (postgres only).

**Verification:**

```bash
go test ./foundation/persistence/postgres/... ./foundation/persistence/sqlite/... -count=1
```

---

## Item 6 — Verifier role in `stores/postgres/`

### Task 6.1 — Create the shared `stores/shared/sql-checks/` package

**Files:** `stores/shared/sql-checks/checks.go` (NEW), `stores/shared/sql-checks/compile.go` (NEW), `stores/shared/sql-checks/run.go` (NEW)

**Note on directory placement:** the spec specifies `stores/shared/sql-checks/`. The existing sibling subdirectories under `stores/` are `stores/common/` (Apache-licensed shared helpers, including the action-policy types) and `stores/internal/` (HTTP bridge wiring). `stores/shared/` is a NEW sibling directory introduced by this work; match the LICENSE header convention (Apache-2.0) and import style of `stores/common/`. If on closer reading of the codebase the implementer determines that `stores/common/sql-checks/` is the more natural home (because the helpers are general-purpose shared substrate utilities), surface that as a discovered divergence at the end of execution rather than silently reshaping the path — the spec was explicit about the location.

**Steps:**

1. Create the directory and the package files. License header: Apache-2.0 (matches `stores/postgres/` and `stores/common/`).

2. Define the types in `checks.go`:

   ```go
   // Package sqlchecks compiles a declarative check vocabulary into
   // aggregate-only SQL queries for verifier-style read-only checks
   // against a substrate.
   //
   // Vocabulary mirrors executors/verifier-shape-checks/checks/ where
   // semantics carry over. See spec
   // .ok-planner/specs/2026-05-19-multi-instance-template-ergonomics-design.md
   // Item 6 for the contract.
   package sqlchecks

   // CheckSpec is one declarative check, decoded from userdata.
   type CheckSpec struct {
       Kind   string         `json:"kind" yaml:"kind"`
       Config map[string]any `json:"config" yaml:"config"`
   }

   // Result is the per-check outcome.
   type Result struct {
       Kind    string         `json:"kind"`
       Pass    bool           `json:"pass"`
       Counts  map[string]any `json:"counts,omitempty"`
       Message string         `json:"message,omitempty"`
   }
   ```

3. Define the compilation interface in `compile.go`:

   ```go
   // Compiled is a single check ready to execute against a substrate.
   type Compiled struct {
       Kind    string
       SQL     string
       // Interpret takes the raw scanned values from the query and
       // produces a Result. Each check kind has its own interpreter.
       Interpret func(scanned any) Result
   }

   // Compile takes a check spec, a schema name, and a table name and
   // produces a Compiled with the aggregate SQL and a result
   // interpreter.
   //
   // Schema and table names are validated as SQL identifiers (matching
   // the convention in stores/postgres/store: lowercase letters,
   // digits, underscores; not starting with a digit).
   //
   // Supported kinds: no_nulls, row_count_absolute, pk_unique.
   func Compile(spec CheckSpec, schema, table string) (Compiled, error)
   ```

4. Implement each kind's compiler. SQL shapes (per spec §Item 6 check vocabulary table):

   - **`no_nulls`:** config `fields: [c1, c2, ...]`, optional `threshold: N` (default 0).
     SQL: `SELECT count(*) FILTER (WHERE c1 IS NULL) + count(*) FILTER (WHERE c2 IS NULL) + ... FROM <schema>.<table>`. Result fails if sum > threshold.

   - **`row_count_absolute`:** config `min: N`, optional `max: N`.
     SQL: `SELECT count(*) FROM <schema>.<table>`. Result fails if count < min or (when max set) count > max.

   - **`pk_unique`:** config `fields: [c1, c2, ...]`.
     SQL: `SELECT c1, c2, ..., count(*) FROM <schema>.<table> GROUP BY c1, c2, ... HAVING count(*) > 1 LIMIT 1`. Result fails if any row returned.

5. Define `Run` in `run.go`:

   ```go
   // Conn is the minimal interface needed to run a check.
   //
   // pgx.Conn and *sql.DB both satisfy a wrapping adapter; the package
   // takes Conn to stay backend-agnostic.
   type Conn interface {
       Query(ctx context.Context, sql string, args ...any) (Rows, error)
   }

   type Rows interface {
       Next() bool
       Scan(dest ...any) error
       Close()
       Err() error
   }

   // Run compiles and executes each check against conn, returning the
   // per-check results in spec order.
   func Run(ctx context.Context, conn Conn, schema, table string, specs []CheckSpec) ([]Result, error)
   ```

6. Add a SELECT-only enforcement check inside `Compile`: after generating the SQL, assert it matches `^\s*SELECT ` (case-insensitive). If not, return an error. This is belt-and-suspenders; the compilers should produce SELECT-only SQL by construction, but the check pins the invariant for the test suite.

**Verification:**

```bash
go build ./stores/shared/sql-checks/...
```

### Task 6.2 — Tests for `stores/shared/sql-checks/`

**Files:** `stores/shared/sql-checks/checks_test.go` (NEW)

**Steps:**

1. Test cases:
   - **`no_nulls`:** verify the generated SQL matches the expected pattern for `fields: [id, payload]`. Test the interpreter on synthetic scanned values (sum=0 → pass; sum=3 → fail).
   - **`row_count_absolute`:** verify SQL is `SELECT count(*) FROM <schema>.<table>`. Test interpreter for `{min: 1000}` (count=999 → fail; count=1000 → pass) and `{min: 1000, max: 2000}` (count=2500 → fail).
   - **`pk_unique`:** verify SQL is the GROUP BY ... HAVING ... LIMIT 1 shape. Test interpreter (zero rows scanned → pass; one row → fail).
   - **SELECT-only enforcement:** construct a `CheckSpec` whose compilation would somehow produce a non-SELECT (e.g. by mocking the kind compilers); confirm `Compile` rejects.
   - **Invalid identifier:** schema or table names containing characters outside `[a-z0-9_]` are rejected at compile time.

**Verification:**

```bash
go test ./stores/shared/sql-checks/ -count=1
```

### Task 6.3 — Register the Executor protocol in `stores/postgres/server/server.go`

**Files:** `stores/postgres/server/server.go`

**Steps:**

1. Read the current `Run` function (around line 47 onwards). It currently registers `ClaimProducer` (line 59), optionally `LifecycleSubscriber` (line 60-62), and observability (line 63).

2. Add a new field to `Config`:

   ```go
   // EnableExecutor, when true, registers the Executor service alongside
   // ClaimProducer for the verifier role. The executor consumes a
   // userdata `checks: [...]` DSL and runs read-only aggregate SQL
   // against the schema named by {{claim.staging.address}}. Per spec
   // .ok-planner/specs/2026-05-19-multi-instance-template-ergonomics-design.md
   // Item 6.
   EnableExecutor bool
   ```

3. Add the registration after `LifecycleSubscriber`:

   ```go
   if cfg.EnableExecutor {
       genv1.RegisterExecutorServer(grpcSrv, NewExecutorServer(st))
   }
   ```

4. Add a constructor and the executor implementation in a new file (Task 6.4).

**Verification:**

```bash
go build ./stores/postgres/...
```

### Task 6.4 — Implement the Executor service for the postgres store

**Files:** `stores/postgres/server/executor.go` (NEW)

**Steps:**

1. Create the file with license header (Apache-2.0).

2. Define `ExecutorServer`:

   ```go
   package server

   import (
       "context"
       "encoding/json"

       "google.golang.org/protobuf/types/known/structpb"

       sqlchecks "github.com/fallguyconsulting/rimsky/stores/shared/sql-checks"
       pgsstore "github.com/fallguyconsulting/rimsky/stores/postgres/store"

       genv1 "github.com/fallguyconsulting/rimsky/protocols/proto/v1/gen"
   )

   // ExecutorServer implements proto:executor.proto::Executor for the
   // postgres store's verifier role. Consumes userdata of the form:
   //
   //   {
   //     "schema": "<schema-name>",
   //     "table":  "<table-name>",
   //     "checks": [{kind, config}, ...]
   //   }
   //
   // Runs aggregate-only SQL per stores/shared/sql-checks/. Returns
   // Success on all-pass; Error{error_class: "verifier_failed"} on any
   // failure. Per spec §Item 6.
   type ExecutorServer struct {
       genv1.UnimplementedExecutorServer
       store *pgsstore.Store
   }

   func NewExecutorServer(st *pgsstore.Store) *ExecutorServer {
       return &ExecutorServer{store: st}
   }

   // Execute is the gRPC entrypoint.
   func (e *ExecutorServer) Execute(req *genv1.ExecuteRequest, stream genv1.Executor_ExecuteServer) error {
       return e.executeCore(stream.Context(), req, stream.Send)
   }

   type sendFunc func(*genv1.ExecuteEvent) error

   func (e *ExecutorServer) executeCore(ctx context.Context, req *genv1.ExecuteRequest, send sendFunc) error
   ```

3. `executeCore` outline:
   - Parse `userdata`:
     - `schema` (string, required)
     - `table` (string, required)
     - `checks` ([]CheckSpec, required, non-empty)
   - Validate `schema` and `table` as SQL identifiers (reuse `pgsstore.ItemsTableIdentRegex` or similar).
   - Acquire a connection from the store's pool. Run each compiled check via `sqlchecks.Run`.
   - If all checks pass → send `StreamClose.Success` with a Struct payload summarizing per-check counts (use `structpb.NewStruct`).
   - If any check fails → send `StreamClose.Error{error_class: "verifier_failed", payload: <per-check failure summary>}`.
   - On parse error → send `StreamClose.Error{error_class: "invalid_userdata", payload: ...}`.

4. Refer to `executors/verifier-shape-checks/server.go::executeCore` for the terminal-emission pattern (the `StreamClose.Success` / `StreamClose.Error{error_class}` send shape). Note: the reference function's signature is `(req, send) error` without a context parameter; this implementation includes `ctx context.Context` because the SQL queries need it. The reference is a template for the terminal-event shape, not for the parameter list.

**Verification:**

```bash
go build ./stores/postgres/...
```

### Task 6.5 — Wire `EnableExecutor` into the postgres-store binary

**Files:** `stores/postgres/cmd/main.go`

**Steps:**

1. Add `EnableExecutor bool \`yaml:"enable_executor"\`` to the `yamlConfig` struct (after `EnableLifecycle`).

2. Pass it through into the `server.Config`:

   ```go
   server.Run(ctx, server.Config{
       // ... existing fields ...
       EnableExecutor: cfg.EnableExecutor,
   }, grpcLis, httpLis, adminLis)
   ```

3. Update `stores/postgres/config-example.yml` to document the new flag.

**Verification:**

```bash
go build ./stores/postgres/...
```

### Task 6.6 — Scenario test: atomic-staging end-to-end with fused store

**Files:** `test/scenarios/atomic_staging/pg_verifier_test.go` (NEW)

**Steps:**

1. The existing atomic-staging scenario tests live as a subdirectory package at `test/scenarios/atomic_staging/` (`abandon_on_any_failure_test.go`, `commit_on_all_success_test.go`, `sub_stage_verifier_failure_test.go`). The new test should be added to that same package, matching the existing layout convention. Skim those tests to pick up the testcontainers-go + control-api + supervisor setup pattern.

2. Write a new scenario that:
   - Boots a real Postgres via testcontainers.
   - Boots the rimsky control-api + supervisor + the postgres store binary (with `enable_executor: true`).
   - Registers a template containing the example fragment from spec §Item 6 (`stage-items` + `verify-staged-table`).
   - Creates an instance.
   - Drives the staging-then-verify-then-commit cycle: producer Open creates a staging schema, downstream writer node inserts rows, verifier node runs checks, aggregation fires Commit, data lands in production schema.
   - Asserts that all rows the writer produced end up in the production schema after Commit.
   - Repeat with one check failing (e.g. row_count too low): aggregation fires Abandon; staging schema is dropped; production schema is unchanged.

**Verification:**

```bash
go test ./test/scenarios/atomic_staging/ -run TestPGVerifier -count=1
```

(Docker must be running.)

### Task 6.7 — Conformance probe against the fused store

**Files:** existing conformance binaries at `cmd/rimsky-executor-conformance/` and `cmd/rimsky-claim-producer-conformance/`; new sibling test file at `test/scenarios/atomic_staging/pg_verifier_conformance_test.go` (NEW)

**Steps:**

1. The conformance binaries take an executor / claim-producer endpoint and run a battery of protocol checks. Run each against the fused postgres-store binary (with `enable_executor: true`) in a scenario test or local dev environment.

2. Add a sibling scenario test at `test/scenarios/atomic_staging/pg_verifier_conformance_test.go` that boots the fused store and invokes both conformance binaries against its endpoint, asserting pass on both.

**Verification:**

```bash
go test ./test/scenarios/atomic_staging/ -run TestPGVerifierConformance -count=1
```

---

## Design Changes — concept doc and tension mutations

Each task below edits a doc file under `.ok-planner/design/`. These are first-class plan tasks, executed alongside the code changes. The spec's `## Design changes` section is the source of truth for the content; the tasks here translate it into mechanical edits.

### Task DC.1 — Update `concepts/userdata.md`

**Files:** `.ok-planner/design/concepts/userdata.md`

**Steps:**

1. Under "Per-instance overrides", update the merge-order text from three layers (`template → by_executor[<executor>] → by_node[<node>]`) to four:

   ```
   template.defaults.userdata.by_executor[<executor>]
     → node.userdata
     → instance.userdata_overrides.by_executor[<executor>]
     → instance.userdata_overrides.by_node[<node>]
   ```

   More specific wins; operator-level overrides win over template-author defaults.

2. Fix citation drift at lines 37 and 45: replace `code:graph/shared/jsonmerge.go::DeepMergeJSON` and `graph/shared.DeepMergeJSON` with `code:foundation/shared/jsonmerge.go::DeepMergeJSON`.

3. Append a Notes entry:

   > 2026-05-19 — Template-level userdata defaults added per spec 2026-05-19-multi-instance-template-ergonomics-design. @blessed-invariant 11 unchanged: only routing keys (by_executor plus executor names) are inspected; fragment values are never read.

**Verification:** Pure doc edit; no automatable check. The full-pass `## Verification — final pass` battery at the end covers the code that this doc describes. Same applies to every DC.* task below.

### Task DC.2 — Update `concepts/attribute.md`

**Files:** `.ok-planner/design/concepts/attribute.md`

**Steps:**

1. Under "Invariants", replace the bullet that currently reads "The substitution grammar is a closed enumeration: six source kinds (`deps.<node>.<field>`, ...)" with:

   > The substitution grammar is a closed enumeration of source kinds: `nodes.<X>.attribute.<field-path>`, `nodes.<X>.event.<name>.<field-path>`, `claim.<alias>.{address|scope|payload.<field-path>}`, `params.<field-path>`, `trigger.message.payload.<field-path>`, `child.partition_key`. Each path-walking kind admits an optional-empty trailing path; with an empty trailing path the directive resolves to the kind's JSON root. Resolution is either whole-directive (the input is exactly one `{{...}}` directive modulo whitespace; returns the JSON value verbatim) or embedded (the input has literal text alongside directives; stringifies and concatenates). The legacy `deps.<X>.<Y>` form is retired and rejected with a migration-pointer error.

2. Under "Open within this concept", update the bullet referencing the three introspection sites: replace `makeStoreHandle` with `code:runtime/runner_dispatch.go::makeClaimHandle`.

3. Append a Notes entry (creating a `## Notes` section at the bottom if absent):

   > 2026-05-19 — Grammar text corrected (retired deps.*, added live trigger.* and child.*) and whole-directive value-lift documented per spec 2026-05-19-multi-instance-template-ergonomics-design. Adjacent tensions/substitution-grammar-count-drift.md partly addressed by this update; the cross-doc-prose sweep (CLAUDE.md, docs/concepts/attributes.md) remains open.

### Task DC.3 — Update `concepts/node.md`

**Files:** `.ok-planner/design/concepts/node.md`

**Steps:**

1. Under "What it is" (line 16), the enumeration currently lists "zero-or-more dependencies on other nodes, zero-or-more required claims/locks, optional attributes JSON schema, optional userdata, optional lifecycle-handler block, optional `on_event` map, optional `quality_rules`, and (for non-claim-only nodes) a target executor."

   Replace this enumeration with one that:
   - Drops `on_event map` (retired).
   - Drops `quality_rules` (retired).
   - Adds `subscribes:` (the receiver-side subscription declaration).
   - Adds `holds:` (claim co-holdership).
   - Adds `tags:` (operator-facing metadata, new per this spec).

2. Under "Boundaries" (line 24), the current text reads "its dispatch / terminal lifecycle, its claim spec list, its handler resolutions, its quality-rule evaluations, its attribute writeback."

   - Drop `its quality-rule evaluations`.
   - Add `operator-facing tags`.

3. Under "Boundaries" Adjacent list (same line), the current text reads "Adjacent: `node-state`, `last-outcome`, `frame`, `cascade`, `attribute`, `lifecycle-handler`, `on-event-handler`, `claim`, `named-lock`."

   - Drop `node-state` and `on-event-handler` (both retired).
   - Keep the rest.

4. Add a new Invariants bullet:

   > Tag values admit `{{params.<key>}}` substitution at materialization time (instance creation); no other substitution source kinds are available at that phase. Tag substitution failures are fatal at instance creation, matching the dispatch-time discipline for required-attribute substitution. Tags do not gate dispatch, cascade, or validation — they are operator-facing metadata.

5. Append a Notes entry:

   > 2026-05-19 — Tags added per spec 2026-05-19-multi-instance-template-ergonomics-design. Pre-existing drift cleaned up in same pass: dropped retired `on_event`/`quality_rules` from "What it is", dropped `its quality-rule evaluations` from Boundaries, dropped retired `node-state`/`on-event-handler` from Adjacent list.

### Task DC.4 — Update `concepts/claim-producer.md`

**Files:** `.ok-planner/design/concepts/claim-producer.md`

**Steps:**

1. Under "Boundaries", append:

   > The bundled SQL-based store `stores/postgres/` additionally registers `proto:executor.proto::Executor` to support verification of its own staged content; see `concept:executor`. The same binary plays both roles via separate gRPC service registrations on a single endpoint. The pattern is open to future SQL-substrate stores adopting the same fusion.

2. Append a Notes entry:

   > 2026-05-19 — stores/postgres/ extends to the executor role per spec 2026-05-19-multi-instance-template-ergonomics-design.

### Task DC.5 — Update `concepts/executor.md`

**Files:** `.ok-planner/design/concepts/executor.md`

**Steps:**

1. Under "Boundaries", append:

   > The bundled SQL-based store `stores/postgres/` registers this protocol alongside `concept:claim-producer`. The same binary plays both roles via separate gRPC service registrations on a single endpoint. Future SQL-substrate stores may adopt the same pattern.

2. Append a Notes entry:

   > 2026-05-19 — stores/postgres/ extends to the executor role per spec 2026-05-19-multi-instance-template-ergonomics-design.

### Task DC.6 — Update `concepts/atomic-staging.md`

**Files:** `.ok-planner/design/concepts/atomic-staging.md`

**Steps:**

1. Append a Notes entry:

   > 2026-05-19 — Reference impl set extends from `examples/atomic-staging-fs-producer/` (POSIX filesystem) to the SQL-backed pattern demonstrated end-to-end by the fused `stores/postgres/` per spec 2026-05-19-multi-instance-template-ergonomics-design. Substrate-atomicity table unchanged.

### Task DC.7 — Update `concepts/rimsky.md`

**Files:** `.ok-planner/design/concepts/rimsky.md`

**Steps:**

1. Under "Boundaries" → "Owns", append:

   > Resolution of `source_file:` references in spec YAML at `rimsky template register`, before the wire call to `POST /templates`. The wire-side spec is always resolved bytes.

2. Append a Notes entry:

   > 2026-05-19 — source_file: client-side resolution added per spec 2026-05-19-multi-instance-template-ergonomics-design.

### Task DC.8 — Update `concepts/template.md`

**Files:** `.ok-planner/design/concepts/template.md`

**Steps:**

1. Append a Notes entry (creating `## Notes` section if absent):

   > 2026-05-19 — `TemplateSpec` gains optional `Defaults *TemplateDefaults` carrying template-author userdata baselines (`defaults.userdata.by_executor.<name>`). `TemplateNodeDef` gains optional `Tags []string` for operator-facing metadata (with materialization-time `{{params.<key>}}` substitution support). Both extensions are additive; hash semantics unchanged. Per spec 2026-05-19-multi-instance-template-ergonomics-design.

### Task DC.9 — Update `concepts/rimsky-yml.md`

**Files:** `.ok-planner/design/concepts/rimsky-yml.md`

**Steps:**

1. Under "What it is" (line 16), the current enumeration of top-level blocks is `persistence:, named_locks:, claim_producers:, executors:`. Add `publishers:` to the enumeration so it matches the canonical shape documented in `concepts.md` and used by `deploy/rimsky.yml`.

2. Append a Notes entry:

   > 2026-05-19 — A single service binary that plays multiple protocol roles (e.g. `stores/postgres/` as both `concept:claim-producer` and `concept:executor`) is registered under each role's namespace in this file. Reusing the same logical name across `claim_producers:` and `executors:` blocks for one binary is the canonical pattern; the entries' YAML shapes differ per the existing per-namespace conventions (URL-scheme endpoint for claim-producers, `transport:` + bare host:port for executors). Per-namespace `protocols:` enumerations are unchanged by this addition: `claim_producers:` entries continue to advertise `[claim_producer]` (plus optional mix-ins); `executors:` entries advertise `[executor]`. The new pattern is "same binary registered in both namespaces," not "new protocol values in either namespace." Per spec 2026-05-19-multi-instance-template-ergonomics-design.

### Task DC.10 — Update `concepts/claim-co-holdership.md`

**Files:** `.ok-planner/design/concepts/claim-co-holdership.md`

**Steps:**

1. Around line 22, the current example uses the retired `dependencies: [load-data]` shape (post-2026-05-14 retired in favor of `subscribes:`). Update the example to use the current subscription form. The exact replacement depends on the example's surrounding context — read the file first; if the example is purely illustrative, `subscribes: [{node: load-data, on: state, when: fresh}]` is the natural shape. If the `dependencies:` line is not load-bearing for the example, removing it is acceptable.

2. No Notes entry required — this is a typo-class fix being made in the course of this spec's work.

### Task DC.11 — Update `concepts/service.md`

**Files:** `.ok-planner/design/concepts/service.md`

**Steps:**

1. At line 54, the current text references rimsky.yml's `sensors:` block. Replace `sensors:` with `publishers:` (post-2026-05-17 rename per the publisher / publisher-subscription unification).

2. In the same paragraph, replace `cmd/rimsky-sensor-conformance` with `cmd/rimsky-publisher-conformance`.

3. Adjust the surrounding phrasing so the paragraph reflects "publisher" as the umbrella concept and "sensor" as one class within it.

4. At line 45, the Adjacent list currently includes `concept:sensor`. Replace with `concept:publisher` (the umbrella concept post-rename).

5. No Notes entry required.

### Task DC.12 — Update `tensions/substitution-grammar-count-drift.md`

**Files:** `.ok-planner/design/tensions/substitution-grammar-count-drift.md`

**Steps:**

1. Append a Notes entry at the bottom of the file:

   > 2026-05-19 — Partly addressed by spec 2026-05-19-multi-instance-template-ergonomics-design: `concepts/attribute.md`'s Invariants section now reflects the current grammar (retired `deps.*`, added live `trigger.*`/`child.*`). The cross-doc sweep (CLAUDE.md, docs/concepts/attributes.md) remains open.

2. Status stays `open` (not all surfaces are reconciled).

### Task DC.13 — Update `tensions/substitution-introspection-site-count.md`

**Files:** `.ok-planner/design/tensions/substitution-introspection-site-count.md`

**Steps:**

1. Around line 18, the current text names the third introspection site as `makeStoreHandle` in `foundation/integration/runner_dispatch.go`. Update to `code:runtime/runner_dispatch.go::makeClaimHandle` (the function was renamed and moved).

2. Append a Notes entry:

   > 2026-05-19 — Stale name `makeStoreHandle` updated to `makeClaimHandle` per spec 2026-05-19-multi-instance-template-ergonomics-design. The "single sanctioned introspection site" framing remains drifted from three actual sites; this is a name fix only. Tension stays open.

3. Status stays `open`.

---

## Verification — final pass

After all tasks above are complete, run the full verification battery:

```bash
go build ./...
go test ./... -count=1
make lint
```

Race-sensitive paths get an additional pass:

```bash
go test ./foundation/persistence/postgres/... ./runtime/... ./graph/scheduler/... -race -count=3
```

The atomic-staging scenario tests (Tasks 6.6 and 6.7) are gated on Docker availability:

```bash
go test ./test/scenarios/atomic_staging/ -run 'TestPGVerifier|TestPGVerifierConformance' -count=1
```

---

## Documentation updates (per `.claude/rules/rules.md` After-Code-Changes)

These are not coverable by tests but are part of the project's required completion checklist.

### Task DOC.1 — CHANGELOG.md entry

**Files:** `CHANGELOG.md`

**Steps:**

1. Add a bullet under `## Unreleased` summarizing the five items shipped and the design-doc updates. One bullet for each item plus one for the design changes is appropriate.

### Task DOC.2 — Cold-read annotation updates

**Files:** any file with `@source:` / `@concept:` / `@blessed-invariant` annotations touched by the changes above.

**Steps:**

1. Find annotations on touched code:

   ```bash
   rg '@source:|@concept:|@blessed-invariant' graph/attribute/substitution.go runtime/userdata_overrides.go foundation/spec/template.go foundation/persistence/nodes.go control/cli/templates.go stores/postgres/server/
   ```

2. For each annotation, verify it remains accurate. Update as needed.

3. Add new annotations where the spec's design changes introduce stable cross-cutting surfaces:
   - `@concept: userdata` on `TemplateDefaults` and `TemplateUserdataDefaults` in `foundation/spec/template.go`.
   - `@concept: node` on the new `Tags` field on `TemplateNodeDef`.
   - `@concept: executor` on `stores/postgres/server/executor.go::ExecutorServer` (mark this as the SQL-store executor-role enforcement site).

### Task DOC.3 — feature-index.md update

**Files:** `feature-index.md` (project root or wherever it lives — check via `ls feature-index.md` first)

**Steps:**

1. If the file exists, add or update entries for the five items so the index reflects:
   - Template defaults (Item 1) — owner: `runtime/userdata_overrides.go`, `foundation/spec/template.go`.
   - source_file: resolution (Item 2) — owner: `control/cli/templates.go`.
   - Whole-directive substitution (Item 3) — owner: `graph/attribute/substitution.go`.
   - Node tags (Item 4) — owner: `foundation/spec/template.go`, `foundation/persistence/nodes.go`, `control/controlapi/instances.go`.
   - Postgres-store verifier role (Item 6) — owner: `stores/postgres/server/`, `stores/shared/sql-checks/`.

2. If the file does not exist, this task is a no-op (no need to create it for this work).

---

## Manual checks after completion

None — every verification in this plan is automatable. The implementer runs the full battery from the "Verification — final pass" section and reports.
