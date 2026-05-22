# Divergences — 2026-05-21 userdata collapse into attributes

This document records meaningful divergences between the plan
(`.ok-planner/plans/2026-05-21-userdata-collapse-into-attributes.md`) and
the implementation as it lands on the working tree (committed + staged +
unstaged). It is not a critique; correctness review belongs to a separate
pass.

---

## 1. Missing scenario tests under `test/scenarios/userdata_collapse/`

**What the plan said (Task 35):** Create three new scenario tests under a
new `test/scenarios/userdata_collapse/` directory:

- `static_attributes_test.go` — static attribute defaults + L4 override
- `embedded_source_test.go` — embedded-source-with-directives resolution
  including `| <literal>` fallback
- `z_pattern_producer_recovery_test.go` — producer-recovery cycle using
  the new `?` lenient marker on a `nodes.<x>.attribute.<y>` source

**What was implemented:** A single new scenario landed at
`test/scenarios/attribute_overrides_e2e_test.go` (at the top of
`test/scenarios/`, not under a `userdata_collapse/` subdirectory). It
covers the L1 + L3 + L4 override merge end-to-end through the harness.
The plan-named `embedded_source_test.go` and
`z_pattern_producer_recovery_test.go` are not present anywhere under
`test/scenarios/`. The `userdata_collapse/` directory was not created.

The corresponding embedded-source + lenient-`?` behavior is exercised by
unit tests in `runtime/runner_dispatch_test.go` (covering
`substituteAttributesSchema` shapes) and
`graph/attribute/substitution_test.go` (the `?` marker grammar), so the
behavior is tested at the unit level — just not end-to-end through a
full harness.

**Inferred reason:** Implementer's call that the unit tests + the single
e2e override scenario cover the bulk of the dispatch path; the
z-pattern recovery cycle would require a stub executor mode the existing
harness doesn't cleanly support. No mention in implementer's notes.

---

## 2. `applyAttributeOverrides` signature collapses L3 + L4 into one input

**What the plan said (Task 18, Step 18.2):** Suggested signature:

```go
func applyAttributeOverrides(
    resolved map[string]any,
    instanceOverrides map[string]any,  // {by_executor: {...}, by_node: {...}}
    executor string,
    nodeType string,
    logger *slog.Logger,
) map[string]any
```

The plan's comment said the function "takes L3
(`InstanceAttributeOverrides.by_executor`) and L4
(`InstanceAttributeOverrides.by_node`) inputs", but the suggested
signature actually accepts a single combined `instanceOverrides` map.

**What was implemented:** Matches the suggested signature shape, with two
differences:

- The `logger` parameter is typed `shared.Logger` (the project's logger
  interface from `foundation/shared`), not `*slog.Logger` directly.
- The parameter is named `overrides` (not `instanceOverrides`) and
  `nodeName` (not `nodeType`).

See `code:runtime/attribute_overrides.go::applyAttributeOverrides`.

**Inferred reason:** Use of the project's logger interface keeps the
function consistent with the rest of the runtime package, which already
passes `shared.Logger` between most functions. The plan's
`*slog.Logger` would have forced a callsite type-conversion.

---

## 3. `checkAttributesSchema` gains an `execSchemaVisible` boolean to soften enforcement

**What the plan said (Task 14, Steps 14.1–14.4):** The validator's
`checkAttributesSchema` should enforce three things unconditionally:

- Reject properties that declare both `source:` and `default:`.
- Reject properties with neither `source:`/`default:` nor `readOnly: true`
  in the executor's expected schema.
- Reject template-side `readOnly: true` on a property the executor does
  not mark `readOnly: true`.

There is no provision in the plan for skipping any of these when the
executor schema isn't visible.

**What was implemented:** The validator at
`code:graph/node/template_validator.go::checkAttributesSchema` (line
1324) takes an extra `execSchemaVisible bool` parameter:

- The "at most one of source/default" rule still fires unconditionally.
- The "source or default or readOnly" rule fires **only when
  `execSchemaVisible == true`**. When the executor's schema isn't
  visible (no hook wired, hook returns ok=false, or empty bytes), this
  leg is skipped — the validator can't tell whether a
  sourceless+defaultless property is one the executor produces.
- The L2-readOnly-authorship rule is similarly gated on
  `execSchemaVisible`.

Comments in `validateAttributesSchema` (lines 897–913) note that the
runtime's dispatch path applies the same recompute under the same lookup
and the `readOnly`-fallback then fires correctly once the executor
schema lands.

**Inferred reason:** The implementer flagged this explicitly: "test
fixtures that don't bother to wire a hook deployable." Without the
softening, many existing tests (which never wired
`RegistryHooks.ExecutorExpectedAttributesSchema`) would fail at
registration. The implementer's stated argument is that the executor's
schema is the authoritative source for `readOnly`, so deferring that
check to dispatch (when the schema is known) preserves spec intent
while keeping unwired tests deployable. Worth a reviewer eye: this
permits a template that's structurally broken under the unified-surface
rule (sourceless+defaultless+non-executor-output property) to pass
registration when its executor's capabilities aren't in the discovery
cache yet.

---

## 4. `MergeAttributeDefaults` is exported; lives in `graph/node`, not `foundation/spec`

**What the plan said (Task 17, Step 17.2):** "Move that helper to a
shared location (`foundation/spec/` is a natural home — both `graph/node`
and `runtime` can import from `foundation/`) so both registration and
dispatch share one implementation."

**What was implemented:** The helper lives in `graph/node` (private
package name remains `template_validator.go`) and was exported as
`node.MergeAttributeDefaults` (capital `M`). The runtime calls it via
`graph/node` import:

```go
// runtime/runner_dispatch.go:512
return node.MergeAttributeDefaults(execSchema, acq.TemplateAttributeDefaults, nodeSchema)
```

`foundation/spec` was not touched.

**Inferred reason:** `graph/node` already houses the validator code that
needed the helper, and the runtime already imports `graph/node` for
other dispatch-time types (`TemplateNodeDef`, etc.). Moving it to
`foundation/spec` would have added a new cross-package edge and forced
the validator to import a `foundation/spec` symbol. Keeping it on
`graph/node` avoids both. The `graph/node` package already has the
right purity boundary (no DB / no runtime deps) per `.golangci.yml`.

---

## 5. `attribute_overrides.go` collapses to a single `overrides` parameter rather than separate L3/L4

**What the plan said (Task 17, Step 17.5):** "After
`substituteAttributesSchema` returns its resolved values, call
`applyAttributeOverrides(resolved, l3ByExec, l4ByNode, executor, nodeType)`
to merge instance overrides."

**What was implemented:** The function takes a single `overrides
map[string]any` parameter (the entire `attribute_overrides` blob) and
looks up `by_executor[executor]` and `by_node[nodeName]` inside it via
the private `lookupFragment` helper. There is no separation between L3
and L4 at the call boundary; both layers come from the same root JSON
blob.

This matches the plan's own Step 18.2 suggested signature (which already
combined the two), so this divergence is internal-to-the-plan: the plan
described the call shape inconsistently across Task 17 and Task 18, and
the implementation followed Task 18.

**Inferred reason:** The shape of `rimsky_instances.attribute_overrides`
in JSON is `{by_executor: ..., by_node: ...}` — pulling them apart only
to recombine them at the merge site adds no value.

---

## 6. Harness now serializes `Defaults.Attributes`

**What the plan said:** No explicit step covers wiring
`Defaults.Attributes` through the test harness. The plan assumes the
harness's existing template-serialization path will pick it up.

**What was implemented:**
`code:graph/scenario/harness.go::templateSpecToJSON` (lines 567–573) was
extended to emit `defaults.attributes.by_executor` when
`spec.Defaults.Attributes.ByExecutor` is non-empty. Without this, L1
defaults wouldn't reach the supervisor's template registration path
through scenario tests, and the `attribute_overrides_e2e_test.go`
scenario would never see the L1 layer materialise as effective-schema
`default:` values.

**Inferred reason:** Implementer flagged this explicitly: the prior
harness "only handled the flat `Nodes` list" and didn't serialize
template-level `Defaults`. Required for the new e2e scenario to
exercise the L1 → effective-schema → dispatch-time default path.

---

## 7. http-node executor subtracts known transport-config keys from the implicit body

**What the plan said (Task 34, Step 34.1):** "Line 134:
`req.GetUserdata().AsMap()` → `req.GetAttributes().AsMap()`. Same shape
— JSON struct unmarshalled to map." The plan does not contemplate any
behavior change to the implicit-body composition, only the proto field
rename.

**What was implemented:** The http-node executor introduces a new
`configAttributeKeys` set (`url`, `method`, `headers`, `body`,
`expect_status`, `stub_probe`, `stub_response`) and subtracts those keys
from the implicit request body when `attributes.body` is absent. See
`code:executors/http-node/server.go::buildRequestBody` (lines 245–293).

**Inferred reason:** Implementer flagged this explicitly. Pre-collapse,
config and inputs lived in different namespaces (`userdata` vs.
`attributes`), so the implicit body could safely serialize all of
`attributes`. Post-collapse, both live in the same bag — without the
subtraction, transport config like `url`, `method`, and `headers` would
leak into the upstream request body. Spec intent is preserved
(executor's "inputs" go to the upstream; "config" stays local), but the
mechanism requires this new key set.

---

## 8. `crimefinder` consumer not migrated to the new validator shape

**What the plan said:** `apps/crimefinder/` is in-tree code; the plan
does not enumerate it under its "MODIFIED" file lists, but Task 34's
sweep ("`executors/stub/* (if any references)`, ...") and the broader
`@blessed-invariant 11` sweep (Task 25) imply that any live in-tree
consumer surfaces the rename.

**What was implemented:** Crimefinder is partially migrated:

- `apps/crimefinder/executor/src/userdata-schema.ts` was renamed and its
  contents updated (now
  `apps/crimefinder/executor/src/expected-attributes-schema.ts`).
- `apps/crimefinder/executor/src/agent-run.ts`,
  `apps/crimefinder/executor/src/server.ts`,
  `apps/crimefinder/executor/src/capabilities.ts`, and several test
  files were updated.

But:

- `apps/crimefinder/templates/code-review-pass.yml` still declares 4
  YAML `userdata:` blocks (lines 67, 108, 240, 267). The new template
  validator would reject these on deploy.
- `apps/crimefinder/feature-index.md`, `apps/crimefinder/CHANGELOG.md`,
  `apps/crimefinder/producer/src/claim-producer/split-scope.ts`,
  `apps/crimefinder/producer/src/claim-producer/open.ts`,
  `apps/crimefinder/shared/src/scope-addresses.ts`, and
  `apps/crimefinder/test/integration/*.ts` still contain `userdata`
  prose / comment references.

**Inferred reason:** Implementer flagged this explicitly: "crimefinder
... templates still declare YAML `userdata:` blocks and would need a
follow-up migration to deploy against the new validator. Its tests
typecheck; its templates were not exercised in this run." Treated as
out-of-scope for this plan execution.

---

## 9. Conformance scenario `malformed_attributes` was created as a new file rather than renaming

**What the plan said:** The plan didn't enumerate
`conformance/scenarios/malformed_userdata.go` as a file to be renamed.

**What was implemented:** `conformance/scenarios/malformed_userdata.go`
was deleted; `conformance/scenarios/malformed_attributes.go` was added
as a new file (under the
`Run: runMalformedAttributes` scenario name `malformed_attributes`).
`conformance/runner.go` registers the new scenario name.

**Inferred reason:** A conformance scenario name change is part of the
proto-rename ripple; the plan only covered it implicitly via Task 25
(broad `userdata` sweep) and Task 39.8 (final sweep). Implementer
treated it the same way as the runtime / control-api renames.

---

## 10. Plan-named tension/concept moves landed but with `_retired` / `_resolved` directory creation steps inline

**What the plan said (Task 36, Steps 36.1, 36.7):** `mkdir -p
.ok-planner/design/concepts/_retired` followed by `git mv`.

**What was implemented:** Both directories existed already from prior
plans (the discovery scaffolding under `.ok-planner/design/_discover/`
established the pattern); the userdata concept moved to
`.ok-planner/design/concepts/_retired/userdata.md` and the tension to
`.ok-planner/design/tensions/_resolved/userdata-schema-as-opacity-exception.md`.
No divergence in outcome, just no need to create the parent directories.

(Recording this for completeness; not meaningful.)

---

## 11. Migration adds `ALTER TABLE` only — postgres + sqlite both bypass typical "create new column + backfill" pattern

**What the plan said (Tasks 1 and 2):** Use `ALTER TABLE … RENAME
COLUMN` directly. The plan recognises this is the pre-v1 break-freely
path.

**What was implemented:** Matches. Both
`foundation/persistence/postgres/migrations/005-attribute-overrides-rename.sql`
and `foundation/persistence/sqlite/migrations/005-attribute-overrides-rename.sql`
do a single `ALTER TABLE rimsky_instances RENAME COLUMN
userdata_overrides TO attribute_overrides;` — no shim, no dual-read
phase. Per the rimsky pre-v1 rules this is the expected shape.

(No divergence — recorded only because the change is structurally
load-bearing and could be a surprise to a reviewer scanning for a
classical Rails-style migration.)

---

## Summary

The implementation tracks the plan closely on the load-bearing
mechanical items (proto changes, persistence renames, validator
restructuring, override merge, claude-agent executor rewrite). The
meaningful divergences are:

- **Lenient validator on missing executor schema** (item 3) — softens
  the unified-attribute-surface rule when the executor's capability
  bytes aren't visible. The runtime dispatch path reapplies the rule.
- **Two of three planned scenario tests were not written** (item 1) —
  unit-test coverage exists; full end-to-end coverage of the
  embedded-source and z-pattern-recovery paths does not.
- **Crimefinder consumer is half-migrated** (item 8) — TypeScript
  typechecks, but YAML templates still declare `userdata:` blocks and
  would not deploy against the new validator.
- **`MergeAttributeDefaults` lives in `graph/node`, exported, rather
  than moved to `foundation/spec`** (item 4) — avoids a new
  cross-package edge.
- **http-node implicit body subtracts a known-config-keys set**
  (item 7) — necessary because the collapse merges config and inputs
  into one bag.
- **Harness now serializes `Defaults.Attributes`** (item 6) — needed
  for the new e2e scenario to see L1 defaults.
