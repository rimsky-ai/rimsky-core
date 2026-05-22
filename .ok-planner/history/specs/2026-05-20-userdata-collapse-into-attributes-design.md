# Userdata collapse into attributes

**Date:** 2026-05-20
**Status:** Spec — design approved
**Origin:** Brainstorm that began as a prompt-composer feature for producer-owned recovery (the Z-pattern from `.ok-planner/sketches/2026-05-20-substitution-fallback-and-invalidate-payload.md`), expanded once we walked the actual code for the userdata + attribute model and concluded that the two concepts collapse cleanly into one. The fallback operator (`{{X | <literal>}}`) from the sketch's Feature 1 has already landed via the 2026-05-20 attribute-pull-resolution work; this spec assumes it in place and adds the strict/lenient marker on top.

## Context

The pre-collapse model carries two parallel concepts: `attributes` (typed, schema-validated, substitution-bearing per-node I/O) and `userdata` (structurally-inert, schema-validated, per-node executor configuration). Each has its own:

- Wire field on `ExecuteRequest`.
- Schema-validation surface (executor advertises `Capabilities.userdata_schema`; templates declare `attributes.schema`).
- Override mechanism (`userdata_overrides`).
- Persistence (none for userdata; per-run snapshot for attributes).
- Concept doc (`concept:userdata`, `concept:attribute`).
- Discipline annotation (`@blessed-invariant 11` for userdata; structural inertness for both).

The brainstorm's forcing observation: userdata is best understood as a constant attribute that doesn't get substitution. Once you see it that way, the two-concept model is redundant. One concept can carry both jobs — a unified attribute surface where some properties have substitution sources (today's attributes), some have static defaults (yesterday's userdata), and some are populated by executor write-back at commit.

The prompt-composer use case is the surface-level forcing function: an author writing a producer-owned-recovery template wants to express a prompt that conditionally references upstream warnings, with empty-string fallback when no warnings have been produced yet. Under the old model that required two-stage substitution (rimsky resolves attribute sources; executor renders prompt templates against the attribute bag) and either ugly literal defaults in the prompt or a separate composer feature in userdata. Under the unified model the prompt is a single source-bound attribute; rimsky resolves it; the executor consumes the resolved string and appends a fixed metadata footer for executor-private vars. The two-stage substitution collapses to one stage plus a fixed wrap.

But the collapse stands on its own merits beyond the prompt-composer case: a single substitution grammar, a single override surface, a single schema-validation pass, a single concept to learn.

Pre-v1 break-freely. No migration shim; no deprecated path; treat as if rimsky has always been this way.

## Summary

Userdata retires as a distinct concept. Attributes absorb its jobs:

- Each schema property is one of three shapes: source-bound (rimsky resolves at dispatch), static-default (resolved at registration), or executor-written (populated at commit).
- The substitution grammar in `source:` relaxes to admit embedded text + multiple directives in a single source string.
- Per-directive strict-default semantics with a new `?` opt-in marker for lenient (null on missing).
- A four-layer override merge mirroring today's userdata overrides, with L1 (template-level cross-cutting defaults) at registration and L3 + L4 (instance-level overrides) at runtime.

Wire and concept-level changes:

- `proto:executor.proto::ExecuteRequest.userdata` field removed; all values flow through `attributes`.
- `proto:executor_observability.proto::ObservabilityCapabilities.userdata_schema` renamed to `expected_attributes_schema`.
- `proto:validation.proto::ExecutorContext.userdata` field removed; the validation pipeline operates against the resolved attribute set.
- `concept:userdata` retires; `@blessed-invariant 11` retires; `concept:attribute` absorbs the unified surface; `concept:inertness` drops userdata from its structural-inert stream list; `concept:validation` updates its pipeline description.
- `tensions/userdata-schema-as-opacity-exception.md` resolves (no more userdata = no opacity-exception muddiness).
- `code:executors/claude-agent/src/agent-run.ts::renderTemplate` retires. The executor reads source-bound prompt attributes verbatim and appends a fixed metadata footer for the per-run callback token + resume context.

## Architecture

### Attribute as the unified surface

Each node declares an attribute schema. Each property is one of three shapes:

```yaml
attributes:
  schema:
    properties:
      # Source-bound: rimsky resolves at dispatch
      warnings_block:
        type: string
        source: "{{nodes.verify-config.attribute.warnings_block?}}"

      # Static-default: resolved at registration from the effective schema
      model:
        type: string
        default: "claude-sonnet-4-5"

      # Executor-written: no source, no default; populated at commit by the executor's write-back
      response_summary:
        type: string
```

Constraints enforced at registration (a new validator pass, `checkAttributesSchema`):

- A property has at most one of `source:` or `default:`. Declaring both is rejected.
- A property must satisfy one of: has a `source:`, has a `default:`, or is marked `readOnly: true` in the executor's `expected_attributes_schema` (signalling "the executor produces this at write-back; no input needed"). `readOnly: true` is JSON Schema's standard keyword for "applications should not modify the value" — in our context that means template-supplied input is not expected.
- Properties failing all three checks are rejected at registration with `template_validation_failed`.

The `readOnly` distinction matters because the executor's `expected_attributes_schema` declares two kinds of properties: input fields the executor *consumes* at dispatch (`system_prompt`, `model`, `cli.*` for claude-agent) and output fields the executor *produces* at write-back (e.g., a `response_summary`). Input fields must have a `source:` or `default:` in the template; output fields can be left without either.

The template-author L2 declaration cannot override `readOnly: true` from the executor's schema. Claiming "the executor will write this" for a property the executor doesn't actually write is rejected at registration.

### Substitution grammar (`source:`)

The grammar relaxes to admit:

- Literal text before, after, and between directives.
- Multiple directives in a single source string.

Each directive carries an independent marker:

```
{{X}}              # strict (default) — missing fails dispatch
{{X?}}             # lenient — missing resolves to null
{{X | "literal"}}  # lenient with fallback — missing resolves to the literal
```

`X` is any directive body recognized today: `nodes.<N>.attribute.<path>`, `nodes.<N>.event.<name>.<path>`, `claim.<alias>.{address|scope|payload[.<path>]}`, `params.<path>`, `trigger.message.payload[.<path>]`, `child.partition_key`.

Rejected at registration:

- `{{X? | "literal"}}` — incoherent. `?` says "null on missing"; `| "literal"` says "literal on missing." Pick one.
- Multi-pipe chains (`{{X | Y | Z}}`) — previously declined; still rejected.
- Array-form multi-source (`source: [{{X}}, {{Y}}]`) — previously declined; still rejected.

The substitution engine itself (`code:graph/attribute/substitution.go::Substitute` and `SubstituteValue`) already handles embedded text + multi-directive resolution. The change at the grammar layer is in the validator (`code:graph/node/template_validator.go::checkAttributeSource`) plus a small extension in `resolveDirectiveValue` to recognize the `?` marker.

### Resolution waterfall

For a source-bound property:

1. Each directive resolves against the dispatch context (wait-set, claim, params, trigger, child).
2. A resolved value (including JSON `null` from an upstream emit) is used as-is.
3. A missing directive with `?` resolves to JSON `null`.
4. A missing directive with `| <literal>` resolves to the literal.
5. A missing directive with no marker fails dispatch with `template_resolution_failed`, citing the directive verbatim and the path tokens (per `@blessed-invariant 20`).
6. Embedded mode concatenates each directive's resolved value (stringified for composites via `json.Marshal`) with intervening literal text; whole-directive mode returns the typed value.

For a static-default property:

- The `default:` value is read from the effective schema (post-L1 merge) and used verbatim.
- No substitution is applied to default values. Operator-supplied literal text matching the substitution grammar inside a default is preserved as-is.

For an executor-written property:

- The property is absent from the dispatch-time attribute bag until the executor's commit write-back populates it.
- If the executor's commit does not populate it and the schema marks the property `required`, commit fails via the post-write-back JSON Schema validation gate.

### Required-field semantics

JSON Schema's `required:` keyword retains its standard meaning ("this property must be present in the validated object") but loses its dispatch-gating role:

- Source-bound properties with no marker that miss at runtime fail dispatch directly (step 5 above) — `required:` doesn't have a chance to fire.
- Source-bound properties with `?` or `| <literal>` always emit a present value (null or the literal) — `required:` is satisfied automatically.
- Static-default properties always have a value — `required:` is satisfied automatically.
- Executor-written properties: `required:` fires at the commit-time validation gate if the executor's write-back didn't populate the property.

Authors who want "this value must be a real string, not null" declare `type: "string"` (or any non-nullable type). The post-substitution JSON Schema validation pass at the end of `substituteAttributesSchema` rejects null values against non-nullable types.

### Override layering

Four layers, with L1 at registration and L3 + L4 at runtime. L1, L3, and L4 carry *value fragments* keyed by attribute name (same shape as today's userdata fragments — opaque key-value with attribute paths as keys). L2 is the per-node schema declaration, where `default:` values appear inline alongside types, `source:` directives, and `required:` constraints:

```
L1 (registration) — template.defaults.attributes.by_executor[<exec>]
                    Map of <attr> → <value>. Merges into per-node effective
                    schema as the `default:` for that property. Applies to
                    all nodes using <exec>.

L2 (registration) — node.attributes.schema.properties.<attr>
                    The per-node declaration. The `default:` here overrides
                    L1's contribution. Can also declare `source:` (which
                    takes priority over any L1 default since `source:` and
                    `default:` are mutually exclusive).

L3 (runtime)      — instance.attribute_overrides.by_executor[<exec>]
                    Map of <attr> → <value>. Operator overrides applied at
                    dispatch to all nodes using <exec>.

L4 (runtime)      — instance.attribute_overrides.by_node[<node>]
                    Map of <attr> → <value>. Operator overrides applied at
                    dispatch to a specific node.
```

Most specific wins (L4 > L3 > effective default from L1/L2). The schema (types, `source:` declarations, `default:` declarations) is locked at registration; only values flow through the runtime merge.

Override values are static — no substitution applied. Operator-supplied `"{{X}}"` is treated as a literal string. This matches the inertness discipline applied to attribute values today (rimsky never inspects override fragment values to make decisions).

### Effective schema computation

At template registration, rimsky computes the per-node effective schema:

```
executor.expected_attributes_schema   (from Capabilities)
  ∪ L1: template.defaults.attributes.by_executor[<exec>]
  ∪ L2: node.attributes.schema
```

JSON Schema merge rules:

- The executor's schema contributes property types and the `additionalProperties` policy.
- L1 contributes `default:` values for properties the executor declared (or new properties if the executor's schema admits `additionalProperties: true`).
- L2 can add properties (admitted iff the executor's `additionalProperties: true`), declare `source:` directives, declare `default:` values, or leave a property unsourced/undefaulted (the executor-written case).
- Most specific wins on `default:` (L2 over L1).
- Type conflicts between executor's declared type and L1/L2's redeclared type are rejected at registration with `template_validation_failed`.
- The `additionalProperties` setting comes from the executor's schema. The template author cannot relax it.

Validation runs once over the effective schema at registration. The "every property has source or default or executor-write-back" check (`checkAttributesSchema`) applies to the merged result.

The effective schema is the input to the template-hash computation. Two templates with the same per-node declarations but different L1 defaults produce different hashes — by design, since L1 changes what the executor sees at dispatch.

### Persistence

`table:rimsky_node_attributes.data` holds the full attribute snapshot at dispatch — source-resolved values + static defaults + post-merge overrides — keyed by `node_run_id`. The executor's commit write-back merges into the same row.

`code:runtime/runner_acquire.go::acquisition.MergedUserdata` renames to `MergedAttributes`. Same purpose: capture the dispatch-time attribute set for lineage hashing.

Static values are snapshotted per run alongside dynamic values. Storage waste is real but small (pre-v1, no production load); the forensic benefit is decisive: every node-run row contains exactly what the executor saw at dispatch, with no read-side reconstruction against historical templates. Template-default mutations don't retroactively rewrite history.

`col:rimsky_instances.userdata_overrides` renames to `col:rimsky_instances.attribute_overrides`. Drop + recreate via new migration, per pre-v1 break-freely.

### Wire protocol

`proto:executor.proto::ExecuteRequest`:

- Remove the `userdata` field.
- All template-supplied values flow through `attributes`.

`proto:executor_observability.proto::ObservabilityCapabilities`:

- Rename `userdata_schema` (field 6) to `expected_attributes_schema`. Same JSON Schema content shape; new field name.
- Update the comment block describing the field's purpose to reflect the unified attribute role.

`proto:validation.proto::ExecutorContext`:

- Remove the `userdata` field. The validation pipeline operates against the resolved attribute set passed via the existing `attributes` field on `ExecutorContext`.
- Remove `userdata` mentions in `FieldRef` documentation (line 105's "/executor/userdata/some_field" example becomes "/executor/attributes/some_field").

Regenerate generated Go via `make proto-gen`.

### Executor side

The executor consumes the dispatched attribute bag directly. There is no executor-side template-rendering pass against author-facing placeholders. Specifically for claude-agent:

- `attributes.user_prompt` is the fully rimsky-resolved user prompt (source-bound or static-default, author's choice per template).
- `attributes.system_prompt` is the fully rimsky-resolved system prompt.
- `attributes.model`, `attributes.cli.*`, and other config fields are read directly as values.

Before sending the user prompt to Claude, the executor generates the per-run callback token (a UUID, executor-internal) and appends a fixed metadata footer to the user prompt:

```
<user prompt content>

---
callback_token: <generated-uuid>
resume_payload: <bytes-as-utf8-or-empty>
resume_reason: <reason-or-empty>
---
```

The footer is always emitted; empty fields render as empty strings rather than being suppressed. The footer is appended to the user prompt only; the system prompt stays clean to preserve prompt caching (per-run mutable content invalidates cache).

`code:executors/claude-agent/src/agent-run.ts::renderTemplate` retires. The two-stage substitution model (rimsky resolves attribute sources; executor renders prompt template against attribute bag + executor-private vars) collapses to one stage (rimsky resolves at dispatch) plus a fixed string append (executor adds private vars).

### Same-node attribute references

The substitution grammar does not include a `{{self.attribute.X}}` or `{{attributes.X}}` source kind for referencing a property's value from another property's source on the same node. This is intentional: same-node cross-property substitution would require ordered resolution of properties within a node's schema and complicates the substitution engine.

Authors who want composition build it inside one source string:

```yaml
attributes:
  schema:
    properties:
      user_prompt:
        source: |
          Generate config for {{params.domain}}.
          {{nodes.verify-config.attribute.warnings_block | ""}}
          Done.
```

The single source resolves to a fully-composed string. No same-node references needed.

## Components

### graph/attribute/substitution.go

No changes to the core substitution functions. `Substitute` (embedded mode) and `SubstituteValue` already handle embedded text + multi-directive sources.

One extension: `resolveDirectiveValue` (private function) gains `?` marker parsing. After stripping the optional `| literal` fallback (existing behavior), check the remaining directive body for a trailing `?`. If present, strip it and set a "lenient" flag. On `ErrMissingSource`:

- Lenient flag set → return JSON `null` (typed as `any` with the nil literal).
- Lenient flag unset and fallback parsed earlier → return the fallback literal (existing).
- Neither → return `ErrMissingSource` (caller handles).

The `?` and `| literal` flags are mutually exclusive at the validator layer; the runtime path doesn't need defensive handling for the combination (it would have been rejected at registration).

### graph/node/template_validator.go

**`checkAttributeSource`**: relaxed validation.

- Accept literal text alongside `{{...}}` directives.
- Accept one or more directives in a single source string.
- For each directive: validate per existing per-kind rules.
- Parse and validate the optional `?` marker (must appear immediately before the closing `}}` or before the `|` fallback delimiter).
- Validate the optional `| <literal>` fallback per existing rules.
- Reject `?` and `| <literal>` on the same directive with a clear error message.
- Reject array-form multi-source sources (already declined; ensure the rejection error is still clear).

The function's signature stays the same; only its internal grammar acceptance changes.

**`checkAttributesSchema`** (new): a new validator pass that runs after the effective schema is computed at registration. Enforces:

- Each property has at most one of `source:` or `default:`. Declaring both fails registration.
- Each property satisfies one of: has `source:`, has `default:`, or is marked `readOnly: true` in the executor's `expected_attributes_schema` (executor-write-back populates at commit). Failing all three fails registration.
- The template-author L2 declaration cannot set `readOnly: true` on a property the executor's schema does not also mark `readOnly: true`. Rejected at registration with a clear message.

**`validateUserdataAgainstSchema`** (existing, at line 1210): retires. Its call site at line 178 also removes. Its job (registration-time validation of merged userdata against the executor's `userdata_schema`) is subsumed by `checkAttributesSchema` running against the effective attribute schema (which now incorporates the executor's `expected_attributes_schema`).

The `@concept: userdata` annotation at line 312 (on the `defaults.userdata.by_executor` validator) is removed; the function it annotates is part of the `TemplateDefaults.Userdata` retirement.

### foundation/spec/template.go

- `NodeDef.Userdata` field retires.
- `TemplateDefaults.Userdata` field retires.
- `TemplateDefaults.Attributes` field introduced — same shape as today's userdata defaults (opaque key-value fragments keyed by `by_executor.<exec>`, with the inner map keyed by attribute name and the value being the default value for that attribute).
- `NodeAttributesDef.Schema` is the per-node JSON Schema (unchanged surface). The JSON Schema's `default:` keyword becomes load-bearing — interpreted by rimsky as the static-default value for the property when no `source:` is set. The JSON Schema's `readOnly:` keyword on the executor's expected schema becomes load-bearing — interpreted by rimsky as "executor produces this; template doesn't supply input."
- The `@concept: userdata` annotations at lines 53, 70, 80 (on `Userdata` and `TemplateDefaults.Userdata` fields) are removed alongside the fields.

### runtime/runner_dispatch.go

**`substituteAttributesSchema`**: extended to:

- Process source-bound properties (existing logic, with `?` marker support inherited from the substitution engine).
- Process static-default properties (new): read the `default:` value from the effective schema and emit it in the output map.
- Apply L3 + L4 overrides on top of the resolved set (calling into the renamed `applyAttributeOverrides`).
- Validate the final merged attribute set against the effective schema via the existing JSON Schema validation gate.

The function's return shape stays the same (`map[string]any, error`). Required-property dispatch-failure logic is removed from this function (the `?` marker on a directive now handles "missing is fine"; required-without-marker missing now fails directly inside the substitution call).

**`buildExecuteRequest`**: simplifies.

- Remove the userdata block (lines ~641–685 of `runner_dispatch.go`). No more four-layer userdata merge here.
- Remove the `UserdataValidator` call. Schema validation now lives entirely in `substituteAttributesSchema` (over the merged effective schema).
- `ExecuteRequest.Userdata` field is gone; no `userdataStruct` construction needed.
- `ExecuteRequest.Attributes` continues to carry the resolved attribute set.

### runtime/userdata_overrides.go → runtime/attribute_overrides.go

- File renames.
- `applyUserdataOverrides` → `applyAttributeOverrides`. Signature accepts the post-substitution attribute bag, the L3 (`by_executor`) overrides, and the L4 (`by_node`) overrides. L1 is folded into the effective schema at registration and is not passed here.
- Returns the post-merge attribute bag.
- Same deep-merge mechanics as today, via `code:foundation/shared/jsonmerge.go::DeepMergeJSON`.

### runtime/runner.go

- `RunArgs.UserdataValidator` field retires. The validation it performed (executor's `userdata_schema` against merged userdata) is now subsumed by the schema validation in `substituteAttributesSchema` against the effective schema (which includes the executor's `expected_attributes_schema`).
- `@blessed-invariant 11` reference at line 186 (in the UserdataValidator doc comment) is removed alongside the field.

### control/observability/userdata_validator.go

- File retires entirely. The functions it exports (`NewUserdataValidator` and adjacent helpers that compile the executor's advertised JSON Schema and validate merged userdata bytes) are no longer wired anywhere — the JSON Schema validation against the effective attribute schema runs inside `substituteAttributesSchema` instead.

### control/config/supervisor.go and cmd/rimsky-supervisor/main.go

- The `UserdataValidator` field on `code:control/config/supervisor.go:62` retires; the pass-through at line 137 retires.
- The construction site at `code:cmd/rimsky-supervisor/main.go:217` (`observability.NewUserdataValidator(disc)`) and the comment block above it (lines 180-183) retire.
- Any test fixtures or wire-up code that constructed `UserdataValidator` instances drops.

### runtime/runner_locks.go

- `@concept: userdata` annotation at line 404 removes alongside its containing function (which threads userdata defaults through). The function itself either retires (if its only purpose was userdata) or is renamed to thread the attribute defaults through.

### runtime/runner_acquire.go

- `acquisition.MergedUserdata` renames to `acquisition.MergedAttributes`. Same lineage-hash flow.
- `TemplateUserdataDefaults` field on the acquisition (whatever its current name is — the L1 source) is folded into the effective schema at registration; this acquisition field can retire entirely if not used elsewhere.
- `InstanceUserdataOverrides` field renames to `InstanceAttributeOverrides`. Same shape (`by_executor` + `by_node`).
- `@blessed-invariant 11` references on this file (lines 59, 92, 102, 151 per the codebase scan) are removed alongside the fields they annotate.
- `@concept: userdata` annotation at line 104 is removed.

### runtime/lineage_writer.go

- Update any field references from "merged userdata hash" to "merged attributes hash." Mechanical rename; the hashing approach is unchanged.

### foundation/persistence/postgres/migrations + foundation/persistence/sqlite/migrations

- New migration in each backend:
  - Drop column `rimsky_instances.userdata_overrides`.
  - Add column `rimsky_instances.attribute_overrides` (same shape: JSONB / JSON).
  - Drop + recreate per pre-v1 break-freely.

### foundation/persistence/instances.go + foundation/persistence/postgres/instances.go + foundation/persistence/sqlite/instances.go + foundation/persistence/conformance/instances_userdata_overrides.go

- Shared interface: `code:foundation/persistence/instances.go::InstanceRow.UserdataOverrides` field (line 33) renames to `AttributeOverrides`; its JSON tag changes from `userdata_overrides` to `attribute_overrides`; the doc comment above it updates accordingly.
- Postgres + SQLite backends: update the accessor methods (`Get`, `Insert`, `List`, etc.) to reference `attribute_overrides` instead of `userdata_overrides`. SQL queries against the renamed column (per the migration above) thread through.
- Conformance file: `code:foundation/persistence/conformance/instances_userdata_overrides.go` renames to `instances_attribute_overrides.go`; its contents (test cases that exercise the override merge) update to use the new field and column names.

### control/controlapi/instances.go

- `POST /instances` request body shape: `userdata_overrides` → `attribute_overrides`.
- `GET /instances/{id}` response shape: same rename.
- Routing-key validation (executor names, node names) unchanged; only the field name changes.

### protocols/proto/v1/{executor,executor_observability,validation}.proto

- `executor.proto`: remove `ExecuteRequest.userdata` field. Update the field's surrounding documentation comments.
- `executor_observability.proto`: rename `ObservabilityCapabilities.userdata_schema` (field 6, line 44) → `expected_attributes_schema`. Update the doc comment block (lines 36–43) to describe expected-attributes semantics.
- `validation.proto`: remove `ExecutorContext.userdata` field (line 45). Replace `userdata` mentions in the comment at line 40 and the field-path example at line 105 with `attributes`.
- Regenerate generated Go via `make proto-gen`.

### Cross-cutting: `@blessed-invariant 11` reference sweep

The `@blessed-invariant 11` invariant retires. Beyond the canonical block at `code:graph/attribute/substitution.go:20-24`, the codebase carries ~39 references to invariant 11 across the runtime, foundation, graph, control, and executor packages (the enumerated list below is illustrative; the broadened regex below is the authoritative sweep). Confirmed reference sites include: additional citations in `code:graph/attribute/substitution.go` (lines 106, 409, 421, 448), `code:graph/node/template_validator.go` (lines 143, 308), `code:runtime/runner.go` (line 186), `code:runtime/runner_dispatch.go` (lines 647, 657), `code:runtime/runner_acquire.go` (lines 59, 92, 102, 151), `code:runtime/userdata_overrides.go` (line 36), `code:runtime/message_delivery.go` (line 34), `code:runtime/cascade_invalidate.go` (line 83), `code:runtime/backfill.go` (line 75), `code:foundation/shared/jsonmerge.go` (line 13), `code:foundation/spec/template.go` (line 67), `code:foundation/persistence/node_runs.go` (line 321), `code:foundation/persistence/instances.go` (line 20), `code:foundation/persistence/messages.go`, `code:graph/attribute/doc.go` (line 32), `code:executors/http-node/observability.go`, `code:control/controlapi/userdata_overrides.go`, `code:control/controlapi/messages.go`, `code:control/controlapi/instances.go`, and `code:test/scenarios/backfill/partition_selector_override_test.go` (line 58).

Some references use the canonical `@blessed-invariant 11` form; others spell the invariant out in prose ("invariant 11", "blessed-invariant 11", "§blessed-invariant 11", "§4.10 invariant 11"). The plan should sweep with a broader regex such as `rg -i 'invariant\s*(no\.?\s*)?11|blessed-invariant\s*11' .` (or equivalent) at execute time to catch both forms, then address each hit:

- If the surrounding code retires (e.g., `userdata_overrides.go` moves to `attribute_overrides.go`), the reference goes with it.
- If the surrounding code stays but the invariant cite is about userdata, remove the cite. Replace with `@concept:inertness` plus a noun-anchored phrase if the structural-inertness discipline still applies to the surrounding code (e.g., attribute values, message payloads).

### Cross-cutting: `@concept: userdata` annotation sweep

The `concept:userdata` retires; all `@concept: userdata` source annotations remove. Confirmed locations: `code:graph/node/template_validator.go:312`, `code:runtime/userdata_overrides.go:41`, `code:runtime/runner_locks.go:404`, `code:runtime/runner_acquire.go:104`, `code:foundation/spec/template.go:53, 70, 80`. The plan should run `rg '@concept: userdata' .` at execute time and remove every hit.

### executors/claude-agent/src/userdata-schema.ts → expected-attributes-schema.ts

- File renames.
- Schema content: same fields as today's userdata schema (`model`, `system_prompt`, `user_prompt_template`, `cli.*`) with two changes:
  - `user_prompt_template` renames to `user_prompt` (signals it's the resolved prompt, not a template). Same string type.
  - `default:` keys added for fields with natural defaults (e.g., `model.default: "claude-sonnet-4-5"`).
- The schema's `additionalProperties` flips from `false` to `true` to admit author-declared extension attributes for inter-node dataflow (e.g., a `warnings_block` attribute used purely for cycle communication that the executor doesn't read).
- Exported as `expectedAttributesSchema` and `expectedAttributesSchemaBytes`.
- Returned via `ObservabilityCapabilities.expected_attributes_schema`.
- Properties the executor *produces* at write-back rather than reads at dispatch (none in claude-agent today; called out for future executors) are marked `readOnly: true` per the JSON Schema standard keyword.

### executors/claude-agent/src/agent-run.ts

- `renderTemplate` function retires (delete the function, its tests, and the import path).
- `AgentRunOptions` simplifies:
  - Drop `userPromptTemplate: string`.
  - Add `userPrompt: string` (already resolved by rimsky).
  - Keep `systemPrompt: string` (also resolved, but no footer appended).
  - Drop the `templateVars` field (no template rendering).
- The dispatch entrypoint reads from the resolved `attributes` bag:
  - `attributes.user_prompt` → `userPrompt`
  - `attributes.system_prompt` → `systemPrompt`
  - `attributes.model` → `model`
  - `attributes.cli` → CLI config object (parsed by `parseCliConfig`)
- After reading `userPrompt`, generate the per-run callback token (existing `randomUUID()` call) and the resume context (existing `parseResumeContext`). Append the fixed metadata footer to `userPrompt` before invoking the CLI runner.

### executors/claude-agent/src/server.ts + http-bridge.ts

- Read prompt fields from `req.attributes` instead of `req.userdata`.
- Drop `parseCliConfig(userdata.cli)`; use `parseCliConfig(attributes.cli)` instead.
- The `toRecord(req.userdata)` line drops (no userdata field on the wire).

### docs/executors/claude-agent/userdata.md → docs/executors/claude-agent/expected-attributes.md

- Documentation file renames (the source path is `docs/executors/claude-agent/userdata.md`; the destination is in the same directory).
- Content updates to describe the new attribute-schema shape, the metadata footer behavior, and the deprecation of `{{userdata.X}}` placeholders.

### Other in-tree executors (verifier-shape-checks, http-node, stub, etc.)

Each executor that reads `req.userdata` today needs the same shape of change:

- Advertised schema renames from `userdata_schema` to `expected_attributes_schema` in the `ObservabilityCapabilities` response.
- Input-field reads switch from `req.userdata.*` to `req.attributes.*`.
- Any per-executor docs (`docs/executors/<exec>/*.md`) update field names.

Concrete instances to update (the plan should sweep for any others):
- `code:executors/verifier-shape-checks/server.go:111` (reads `userdata.checks`).
- `code:executors/verifier-shape-checks/validation.go:84` (calls `exec.GetUserdata()`).
- `code:executors/http-node/observability.go` (advertises userdata schema).
- Any `executors/stub/*` paths that participate in the wire contract.

No semantic change in their behaviors — they receive their inputs from the attribute bag now.

### control/cli + MCP routes

- Any CLI command, MCP tool, or HTTP route that displays or accepts `userdata_overrides` updates to `attribute_overrides`.

## Data flow

End-to-end dispatch under the new model:

1. **Template registration.** The graph layer computes the per-node effective schema (`executor.expected_attributes_schema ∪ L1 ∪ L2`). Validates via `checkAttributesSchema` ("source or default or executor-write-back"). Computes `template_hash` over the effective schema set.

2. **Instance creation.** `attribute_overrides` validates only routing keys. Value bytes are inert (the structural-inertness discipline applies uniformly to attribute values).

3. **Dispatch.** Runtime calls `substituteAttributesSchema`:
   - Resolves source-bound properties against the wait-set / claim / params / trigger / child via the substitution engine.
   - Sets static-default properties from the effective schema.
   - Calls `applyAttributeOverrides` for L3 + L4 on the resolved set.
   - Validates the final merged attribute set against the effective schema.

4. **ExecuteRequest built.** `attributes` field carries the merged set. No `userdata` field on the wire.

5. **Executor receives.** Reads attribute fields directly:
   - Reads `attributes.user_prompt`, `attributes.system_prompt`, `attributes.model`, `attributes.cli.*`.
   - Generates per-run callback token (executor-internal).
   - Reads `req.resume_context` for resume payload/reason (executor-internal channel, not an attribute).
   - Appends the fixed metadata footer to `user_prompt`.
   - Sends final prompt to Claude.

6. **Commit / write-back.** Executor returns updated attributes via `attributes_delta` on the callback. Persistence merges into `rimsky_node_attributes.data` for the run.

## Error handling

Failure modes and their routing:

- **Strict directive misses at runtime** → dispatch fails with `error_class: template_resolution_failed`, citing the directive verbatim and the path tokens (per `@blessed-invariant 20`, no value bytes). Routes through `on_executor_errored`.

- **Type mismatch between resolved value and schema type at dispatch** → dispatch fails via the JSON Schema validation gate at the end of `substituteAttributesSchema`. Routes through `on_executor_errored` with `error_class: template_validation_failed`.

- **Override violating schema type** → caught by the same post-merge JSON Schema validation pass. Same routing.

- **Static attribute missing default at registration** → registration fails with `template_validation_failed`. The template never reaches the runtime.

- **`?` and `| <literal>` on same directive at registration** → registration fails with a clear message ("directive has both `?` and fallback `| <literal>` — pick one").

- **Type conflict between executor's expected schema and template's per-node schema** → registration fails ("type for property `<name>` in node `<type>` conflicts with executor's expected type").

- **Property declared in template but rejected by executor's `additionalProperties: false`** → registration fails ("property `<name>` not declared in executor's expected_attributes_schema; executor schema is closed").

- **Executor-written property not populated at commit** → if `required:`, commit fails via the post-write-back JSON Schema validation gate. Otherwise property remains absent from the persisted row.

## Testing

### Unit tests

- `graph/node/template_validator_test.go`:
  - Source declarations with embedded text + multi-directive accepted.
  - `?` marker on a directive accepted.
  - `?` and `|` on the same directive rejected.
  - Multi-pipe chains still rejected (existing behavior preserved).
  - Array-form sources still rejected (existing behavior preserved).
  - New `checkAttributesSchema` pass:
    - Property with `source:` and no `default:` accepted.
    - Property with `default:` and no `source:` accepted.
    - Property with both `source:` and `default:` rejected.
    - Property with neither, present in executor's expected schema, accepted.
    - Property with neither, not in executor's expected schema, rejected.

- `graph/attribute/substitution_test.go`:
  - `?` marker resolution: missing directive returns null.
  - `?` marker with present value returns the value (marker is no-op when value is present).
  - Mixed embedded source with strict + `?` + `|` directives all resolving correctly.
  - Strict directive missing inside an embedded source raises `ErrMissingSource`.

- `runtime/runner_dispatch_test.go` (new file — `runtime/runner_dispatch.go` has no test file today):
  - `substituteAttributesSchema` emits static-default values when no `source:` is declared.
  - L3/L4 overrides applied via `applyAttributeOverrides`.
  - Final JSON Schema validation catches type mismatches.
  - Override values are not substituted (literal text matching directive shape preserved).
  - Embedded-source resolution (text + directives) produces correct concatenated output.
  - `readOnly: true` properties without `source:` or `default:` accepted; expected to be populated by executor write-back.

- `runtime/attribute_overrides_test.go` (replaces `userdata_overrides_test.go`):
  - L3 by_executor merge.
  - L4 by_node merge.
  - L4 wins over L3 on conflict.
  - Deep-merge semantics on nested objects.

- `executors/claude-agent/src/agent-run.test.ts`:
  - `renderTemplate` tests delete.
  - New: executor reads `attributes.user_prompt` verbatim and uses it as the prompt body.
  - New: metadata footer appended to user prompt in the documented format.
  - New: footer always emitted (empty fields render as empty strings).
  - New: system prompt does not receive a footer.

- `executors/claude-agent/src/server.test.ts` + `http-bridge.test.ts`:
  - Dispatch with `req.attributes` populated invokes the agent with the right values.
  - Dispatch with `req.userdata` present (should not happen post-collapse, but defensive) is ignored or errored — pick one; spec lean: ignored, since userdata isn't in the protocol anymore.

### Scenario tests

- `test/scenarios/`: a "Z-pattern producer-owned recovery" scenario.
  - Template declares `generate-config` + `verify-config` with a subscription cycle.
  - `verify-config` emits `warnings_block` (string attribute) on failure.
  - `generate-config` reads `source: "{{nodes.verify-config.attribute.warnings_block?}}"`.
  - On first dispatch, `warnings_block` is null (no upstream run); producer generates from scratch.
  - On re-dispatch after verifier failure, `warnings_block` contains the failure context; producer regenerates with guidance.
  - Cycle terminates per `max_retries_without_progress`.

- `test/scenarios/`: a static-attribute scenario.
  - Node declares `attributes.model.default: "claude-opus-4-7"`.
  - Verify the dispatched ExecuteRequest carries `attributes.model: "claude-opus-4-7"`.
  - Instance creates with `attribute_overrides.by_node.<node>.model: "claude-sonnet-4-5"`.
  - Verify the dispatched ExecuteRequest carries `attributes.model: "claude-sonnet-4-5"` (L4 wins over L1/L2).

- `test/scenarios/`: an embedded-source scenario.
  - Node declares `attributes.user_prompt.source: "Generate {{params.what}} for {{params.domain | \"unknown\"}}."`.
  - Verify the dispatched ExecuteRequest carries the fully-resolved string.

### Cross-executor conformance

- `cmd/rimsky-executor-conformance`: validates against `expected_attributes_schema` (renamed). Behavior under the new field name. The harness rejects executors that advertise the old `userdata_schema` field name (which no longer exists in the proto).

## Design changes

Concept and CHANGELOG mutations to be applied by execute-plan. Each entry is precise enough to apply mechanically.

- **Retire `.ok-planner/design/concepts/userdata.md`.**

  Move the file to `.ok-planner/design/concepts/_retired/userdata.md` (creating the `_retired/` subdirectory if it doesn't yet exist). Prepend a new section to the moved file's top:

  > ## Retirement
  >
  > 2026-05-20 — Userdata retires as a distinct concept. The role userdata played (per-node executor configuration, structurally inert, with template-level and instance-level overrides) is now covered by attributes with `default:` properties (static-default attributes); see `concept:attribute`. Override mechanism renamed: `userdata_overrides` → `attribute_overrides`. Wire field removed: `proto:executor.proto::ExecuteRequest.userdata` is gone. `@blessed-invariant 11` retires. See `.ok-planner/specs/2026-05-20-userdata-collapse-into-attributes-design.md`.

  Do not modify the original content below the new section; it remains as the historical record.

- **Retire `@blessed-invariant 11`.**

  Remove the invariant block from `code:graph/attribute/substitution.go` (currently at lines 20–24, the four-line comment block beginning with `@blessed-invariant 11 — Userdata is inert in Rimsky`). Other `@blessed-invariant` blocks in the file (20, 21) are unaffected.

  The full reference sweep (~39 hits across source) is enumerated in the `## Components` section under "Cross-cutting: `@blessed-invariant 11` reference sweep" — execute-plan applies that cross-cutting cleanup alongside this retirement.

  Remove `@blessed-invariant 11` references from concept docs as part of the `concept:inertness` mutation below.

- **Mutate `.ok-planner/design/concepts/attribute.md` in place.**

  - Replace the entire content of the `## What it is` section (currently one paragraph ending "... post-substitution + commit post-writeback)."). New text:

    > Attributes are the typed inputs, outputs, and configuration of a node, declared by a JSON Schema in the template's `attributes:` block. Each schema property is one of three shapes: source-bound (`source:` directive resolved at dispatch), static-default (`default:` value resolved at registration), or executor-written (populated at commit by the executor; marked `readOnly: true` in the executor's `expected_attributes_schema`). Persisted writeback lives in `table:rimsky_node_attributes.data`. Validation runs twice (dispatch post-substitution + commit post-writeback).

  - Update `## Boundaries` first sentence to remove the userdata exclusion. New text:

    > Owns: the schema, the substitution grammar, the three property shapes, the override merge across the four layers, the two validation gates, the writeback ledger. Does NOT own: claim payload (lives on `claim`), assets (assets are claims, not attributes — see `concept:asset`), semantic validation (the retired `quality-rule` concept; today the verifier-executor pattern covers that surface — see `executors/verifier-shape-checks/`). Adjacent: `node`, `named-event`, `inertness`, `asset`.

  - Replace the existing `## Invariants` per-field-arity invariant. The old text begins "Per-field `source:` arity is 1 — each attribute property declares exactly one substitution directive." Replace with:

    > Per-field `source:` admits literal text and one or more `{{...}}` directives. Each directive resolves independently against its source kind (`nodes`, `claim`, `params`, `trigger`, `child`). Per-directive strict-default with `?` opt-in to lenient (missing → null); mutually exclusive with `| <literal>` fallback. Multi-source array form (`source: [...]`) and multi-pipe chains (`{{X | Y | Z}}`) are not admitted. Many-to-many fan-in across upstreams lives in the cascade vocabulary (subscriptions over multiple senders, plus optional schema fields whose dispatch-time `ErrMissingSource` is silently omitted at `code:runtime/runner_dispatch.go::substituteAttributesSchema`). Enforced at registration by `code:graph/node/template_validator.go::checkAttributeSource` (rejects the declined forms). The arity asymmetry between subscriptions (many-to-many) and per-field substitution (1:1 per directive) is intentional: subscriptions sum signals across upstreams; substitution names a single value per field. Per-directive composition within a source string concatenates, it does not sum.

  - Append two new bullets to `## Invariants`, after the per-field-arity invariant:

    > Each property has at most one of `source:` or `default:`. Each property satisfies one of: has `source:`, has `default:`, or is marked `readOnly: true` in the executor's `expected_attributes_schema` (executor-write-back populates at commit). Properties failing all three checks are rejected at registration with `template_validation_failed`. Enforced at `code:graph/node/template_validator.go::checkAttributesSchema`.
    >
    > The template-author L2 declaration cannot set `readOnly: true` on a property the executor's schema does not also mark `readOnly: true`. Rejected at registration. The executor is authoritative on which of its attributes it produces vs consumes.

  - Replace the `## Non-goals` `Multi-directive fallback chains` bullet. Old text begins "The fallback operator `{{<directive> | <literal>}}` admits exactly one directive on the left and exactly one JSON literal on the right." Replace with:

    > **Multi-pipe fallback chains.** A single directive admits at most one `| <literal>` fallback. Multi-directive chains (`{{X | Y | Z}}`) and composite literals (`{}`, `[]` as fallbacks) are not admitted. Per-directive `?` marker and `| <literal>` fallback are mutually exclusive (incoherent: `?` says null on missing, `|` says literal on missing — pick one).

  - Append a new `## Static-default properties` section after the `## Open within this concept` section (before `## Notes`):

    > ## Static-default properties
    >
    > A schema property declared with `default: <value>` and no `source:` is a static-default property. Its value is set from the effective schema at registration; instance-level overrides (`attribute_overrides.by_executor.<exec>.<attr>` or `attribute_overrides.by_node.<node>.<attr>`) replace the default at dispatch.
    >
    > Static-default properties replace the role userdata played pre-2026-05-20: per-node executor configuration (model selection, CLI flags, fixed prompts) declared by template authors and overridable by operators at instance time. The substitution grammar does not apply to default values; an operator-supplied `"{{X}}"` in an override is a literal string.
    >
    > Static-default values are persisted per node-run alongside source-resolved and executor-written values in `table:rimsky_node_attributes.data`, providing dispatch-time forensic clarity. Template-default mutations do not retroactively rewrite history.

  - Append a new entry at the bottom of `## Notes`:

    > 2026-05-20 — Userdata collapse. `concept:userdata` retires; its role moves to `default:` properties on the unified attribute schema. Substitution grammar relaxes (embedded text + multi-directive) per `code:graph/node/template_validator.go::checkAttributeSource`. Per-directive strict-default with `?` for lenient. New `checkAttributesSchema` validator enforces the "source or default or executor-write-back" rule. `@blessed-invariant 11` retires. See `.ok-planner/specs/2026-05-20-userdata-collapse-into-attributes-design.md`.

- **Mutate `.ok-planner/design/concepts/inertness.md` in place.**

  - In the `## What it is` section opening paragraph (line 17), replace "six byte streams: userdata, claim scope, claim payload, blob content, named-event payloads, and message payloads (post-2026-05-15)" with "five byte streams: claim scope, claim payload, blob content, named-event payloads, and message payloads (post-2026-05-15)." Also remove the trailing "(Plus the `Error.payload` Struct from the post-2026-05-12 proto restructure.)" parenthetical if its inclusion was about userdata; leave it if it stands alone — verify in context.

  - In the structural-inertness bullet (line 22), replace "Applies to: userdata, attribute values, named-event payloads, `Error.payload`." with "Applies to: attribute values, named-event payloads, `Error.payload`." (Removes the leading "userdata," — the rest of the list is unchanged.)

  - In the `## Auth audit log: verbatim request_params` section (around line 58), update the prose reference to "rimsky's userdata-inert invariant" to read "rimsky's structural-inertness discipline" — the surrounding guarantee (no sensitive data in request bodies) still holds, just under the broader discipline rather than the retired invariant.

  - In the `## Notes` section's [2026-05-15] bullet (around line 63), update the parenthetical "(justified by userdata-inert + claim/payload-inert invariants — no secrets in any control-plane request body)" to "(justified by structural-inertness + claim/payload-inert invariants — no secrets in any control-plane request body)."

  - In the `## Invariants` section opening sentence (line 34), replace "Four `@blessed-invariant`s codify the discipline" with "Three `@blessed-invariant`s codify the discipline".

  - Remove the `**§11**` bullet from the `## Invariants` list entirely. The list reduces to §20 (claim opacity), §21 (blob + named-event), §24 (message payloads).

  - In the `## Boundaries` section's adjacency list, remove `concept:userdata`.

  - Append a new entry at the bottom of `## Notes`:

    > 2026-05-20 — Userdata collapse. `concept:userdata` retires; `@blessed-invariant 11` retires. Attribute-value inertness covered by the structural-inertness discipline. See `.ok-planner/specs/2026-05-20-userdata-collapse-into-attributes-design.md`.

- **Mutate `.ok-planner/design/concepts/instance.md` in place.**

  - Replace every `userdata_overrides` reference with `attribute_overrides`. Each reference also requires a content-shape note since the override now targets attribute paths (not free-form userdata fragments):

    - `## What it is` section: update the line describing the `POST /instances` body to say `attribute_overrides? (per-instance per-node attribute fragments)`.
    - `## Boundaries` section: update the "Owns" line to say "params, attribute_overrides" instead of "params, userdata_overrides." Remove `userdata` from the adjacency list.
    - `## Invariants` section: replace the `userdata_overrides` validation bullet with: "`attribute_overrides` validation inspects only routing keys (`by_executor`/`by_node` plus executor/node names); fragment values are never inspected (preserves structural-inertness for attribute values)."

  - The file has no existing `## Notes` section (the sections end at `## Open within this concept`). Add a new `## Notes` section after `## Open within this concept` containing the entry:

    > ## Notes
    >
    > 2026-05-20 — `userdata_overrides` → `attribute_overrides`. Same merge shape (`by_executor` + `by_node`), applied to attribute values rather than userdata bytes. Persisted as `col:rimsky_instances.attribute_overrides`. See `.ok-planner/specs/2026-05-20-userdata-collapse-into-attributes-design.md`.

- **Mutate `.ok-planner/design/concepts/validation.md` in place.**

  The file uses `## Definition` (not `## What it is` or `## Purpose`); its sections are `## Definition`, `## Boundaries`, `## Invariants`, `## Annotation sites`, `## Notes`.

  - In `## Definition` (line 11 onwards): the protobuf shape block (around line 17) carries `bytes node_userdata = 1; // opaque`. Replace this field with a description of the attribute-bag input that the validation pipeline receives — specifically, the merged effective attribute schema and resolved attribute values for the node under validation. Also update the sentence at line 35 ("Used at template-registration time to give services a say in whether a node's userdata + bindings make sense in their domain.") — change "userdata + bindings" to "attributes + bindings".

  - In `## Boundaries` (line 37 onwards): the sentence at line 39 references "registration-time pipeline integration (`validation_pipeline.go` after the static `userdata_schema` JSON-Schema check)." Update to "registration-time pipeline integration (`validation_pipeline.go` after the static `expected_attributes_schema` JSON-Schema check against the merged effective schema)."

  - In `## Invariants` (line 41 onwards): the pipeline-order bullet at line 43 references "static `userdata_schema` JSON-Schema check from the executor's `Capabilities`." Update to "static `expected_attributes_schema` JSON-Schema check from the executor's `ObservabilityCapabilities`, applied against the merged effective attribute schema."

  - Remove the `@blessed-invariant 11` bullet at line 45 entirely. The "Preserves `@blessed-invariant 11` (userdata inert in rimsky)" sentence has no analog post-collapse — attribute-value inertness is covered by `concept:inertness`'s structural-inertness discipline, which doesn't need a separate per-pipeline-step assertion.

  - In `## Notes` (line 55 onwards): the existing introduction at line 57 says the method name is `Validate` (not `ValidateUserdata`) because the request "carries more than userdata: claim bindings, attribute schemas, sensor config, etc." Update to remove the userdata framing — replace "more than userdata" with "more than the executor's expected-attributes schema" and remove the `ValidateUserdata` parenthetical (`ValidateUserdata` is no longer a meaningful counterfactual).

  - Append a new entry at the bottom of `## Notes`:

    > 2026-05-20 — Userdata collapse. Validation pipeline input changes from `node_userdata` bytes to the merged effective attribute set. Schema check now against `expected_attributes_schema` (the executor's contribution to the effective schema). `@blessed-invariant 11` reference removed; attribute-value inertness covered by `concept:inertness`. See `.ok-planner/specs/2026-05-20-userdata-collapse-into-attributes-design.md`.

- **Resolve `.ok-planner/design/tensions/userdata-schema-as-opacity-exception.md`.**

  Move the file from `.ok-planner/design/tensions/userdata-schema-as-opacity-exception.md` to `.ok-planner/design/tensions/_resolved/userdata-schema-as-opacity-exception.md`. Update the front-matter `status: open` to `status: resolved`. Append a new `## Resolution` section before the existing `## Evidence` section:

  > ## Resolution
  >
  > 2026-05-20 — Resolved by userdata collapse. `concept:userdata` retires; `@blessed-invariant 11` retires. The opacity-exception muddiness was specifically about userdata-schema validation being a sanctioned but unnamed exception to the opacity invariant. With userdata gone, the exception is gone. The schema-validation surface that remains (attribute schema validation against the executor's `expected_attributes_schema`) is part of `concept:attribute`'s validation gate discipline, not an exception. See `.ok-planner/specs/2026-05-20-userdata-collapse-into-attributes-design.md`.

- **Mutate `.ok-planner/design/concepts.md` (the TOC) in place.**

  - Remove the `userdata` row entirely.
  - Update the `attribute` row's one-line definition to reflect the absorbed role: "Typed inputs, outputs, and configuration of a node, declared by JSON Schema. Each property is source-bound (rimsky substitution), static-default (resolved at registration), or executor-written (populated at commit). Persisted per-run; overrides via `attribute_overrides`."

- **CHANGELOG.md**: append a new bullet under `## Unreleased`:

  > - **Userdata collapse into attributes.** `concept:userdata` retires; `@blessed-invariant 11` retires. The role userdata played (per-node executor configuration with template + instance overrides) moves to `default:` properties on the unified attribute schema. `proto:executor.proto::ExecuteRequest.userdata` field removed. `Capabilities.userdata_schema` renamed to `expected_attributes_schema`. `col:rimsky_instances.userdata_overrides` renamed to `attribute_overrides`. The attribute-source grammar relaxes to admit embedded text + multiple directives; per-directive strict-default with `?` opt-in to lenient. `code:executors/claude-agent/src/agent-run.ts::renderTemplate` retires; the executor reads source-bound prompt attributes verbatim and appends a fixed metadata footer (callback_token + resume context). Pre-v1 break-freely; no migration shim. See `.ok-planner/specs/2026-05-20-userdata-collapse-into-attributes-design.md`.

## Open questions for plan phase

These are spec-stable decisions deferred to the plan writer for implementation-question-only knobs.

1. **Effective-schema computation timing and persistence.** The L1 merge happens at registration. The plan should decide whether to compute the merged effective schema once at template registration and persist it on `rimsky_templates` (alongside `template_hash`), or recompute on every dispatch. Computing once and persisting matches today's template_hash discipline and is the natural choice; the plan should confirm and pick the persistence shape.

2. **Order of property resolution within `substituteAttributesSchema`.** Today the function iterates the schema's `properties` map in Go-map-iteration order (effectively random). Under the new model, properties are independent (no same-node cross-references), so order doesn't affect correctness. The plan can either preserve the existing iteration or sort for determinism (helpful for forensic comparison).

3. **Footer format details for claude-agent.** The spec mandates "fixed format" and suggests a `---`-delimited block at the end of the user prompt with `callback_token`, `resume_payload`, `resume_reason` keys. The plan should pin the exact shape: delimiter characters, key-value separator, ordering, encoding for binary resume_payload (utf-8 vs base64), etc. The choice is executor-internal and easy to change later.

4. **Conformance harness updates beyond the field rename.** `cmd/rimsky-executor-conformance` exercises the executor protocol. Beyond the mechanical `userdata_schema` → `expected_attributes_schema` rename, the harness may need new test cases covering executor-write-back contracts (since attribute commit is now the only path for executor-supplied data). The plan should enumerate.

5. **Documentation sweep.** Beyond the concept-doc mutations in `## Design changes`, the plan should sweep `docs/concepts/`, `docs/executors/claude-agent/`, `docs/protocols/`, `docs/agents/llms.txt`, `docs/humans/landing.md`, `docs/glossary.md`, and the README for any userdata references that need updating. The brainstorm did not enumerate these; the plan should.
