---
topic: quality-rules-and-attribute-validation
kind: discipline
---

# Two layers of node-output validation: attribute JSON Schema (mandatory dual gate) plus quality rules (declarative pluggable evaluators)

## Description

A node's output is a typed value (the writeback on `Complete`). Rimsky validates this at two layers:

**Layer 1: Attribute JSON Schema validation** is structural. The template's `attributes:` block declares a JSON Schema; the supervisor validates twice (`@blessed-invariant 12`): at dispatch post-substitution, and at commit post-writeback. This catches shape problems — missing required fields, type mismatches, additionalProperties violations. Implementation lives in `modeling/attribute/validate.go` (see `2026-05-10-attribute-substitution-grammar`).

**Layer 2: Quality rules** is declarative-pluggable semantic validation. A template node can declare `quality_rules:` — a list of `Spec{Type, Config, Severity}` entries (`modeling/qualityrule/spec.go:16-20`; field on `TemplateNodeDef.QualityRules` at `modeling/node/template.go:64`). Each rule has a `type` string (builtin name or `custom`), a free-form `config` map, and a `severity` (default `error`). The evaluation is dispatched per rule via the `Evaluator` interface (`modeling/qualityrule/spec.go:43-45`):

```go
type Evaluator interface {
    Evaluate(ctx context.Context, input EvalInput) (passed bool, details string, err error)
}
```

`EvalInput` carries `NewData` (the proposed writeback), `PreviousData` (nil when no prior version), and `Cfg` (the rule's `Spec.Config`). Implementations should be pure functions free of side effects.

The runtime evaluators + registry live at `modeling/qualityrule/eval/rules.go` (single file; AGPL-licensed per `licensing.yml:25,57` while the spec types at `modeling/qualityrule/spec.go` are Apache-licensed). The registry is a `sync.RWMutex`-guarded `map[string]Evaluator` with `Register(name, ev)` / `Get(name)` (`rules.go:15-34`). Three builtin evaluators are pre-registered at package `init()` (`rules.go:149-155`):

- **`row_count_ratio`** — `len(new) >= cfg["min_ratio"] * len(previous)`, skipped when no `PreviousData` (`rules.go:73-90`).
- **`no_nulls`** — every row in the writeback's `[]map[string]any` must have non-null values for each named field (`rules.go:94-119`).
- **`nullable_fields_present`** — every row must have each named field key present (may hold null) (`rules.go:123-147`).

The literal type name `custom` is reserved: looking it up returns "no custom handler registered" rather than "unknown rule type" (`rules.go:44-49`). Consumers register their own evaluators under arbitrary names via `eval.Register(name, ev)`.

`EvaluateAll(ctx, specs, input)` (`rules.go:39-67`) is the single entry point. It iterates specs in order, partitions results into two slices keyed on `Severity`: anything that's not exactly the string `"warning"` lands in the `errors` slice (`rules.go:56-63`). Per-rule evaluator failure (a returned `err`) short-circuits with a wrapped error rather than continuing; an unknown type name short-circuits identically.

A `Failure` (`spec.go:24-29`) carries `RuleType`, `Config`, `Severity`, and `Details`. The supervisor wraps an evaluator's internal error into a synthetic `Failure{RuleType: "evaluation_error", Severity: SeverityError, Details: err.Error()}` (`foundation/integration/runner_terminal.go:382-388`) so the commit-time append always sees a structured `Failure` slice.

**Integration with attribute writeback.** `foundation/integration/runner_terminal.go::applyTerminalComplete` fires both gates back-to-back (lines 102-126):

1. After delta-merging `t.AttributesDel` into the resolved attributes (line 103), JSON Schema validation runs (`attributes.Validate(..., PhaseCommit)`) and on failure emits an `attributes_schema_failed` event and stops (lines 104-118). This is the second commit-side leg of `@blessed-invariant 12`.
2. If the schema gate passes, `runQualityRules(acq.NodeDef.QualityRules, merged)` (lines 120-126; helper at `runner_terminal.go:376-390`) calls `qreval.EvaluateAll(ctx, rules, EvalInput{NewData: attrs})`. Note: the supervisor only populates `NewData`; `PreviousData` is left nil at this call site, so any builtin that compares to prior state (e.g. `row_count_ratio`) silently passes on every commit. The prose surface (`docs/concepts/deterministic-transformations.md`) describes `PreviousData` semantics; the call site doesn't wire them.
3. On quality-rule failure, the supervisor emits one `quality_rule_failed` event per failure entry (`emitQualityRuleFailures` at `runner_terminal.go:245-268`; one event per `Failure`, batched in a single tx) and routes through `applyTerminalAppError` with `error_class="quality_rule_failed"`. The downstream policy chain (`runner_terminal_errors.go:40`) treats this like any other application error — `error_types` mapping decides retry vs give_up vs invalidate.

Quality rules complement the JSON Schema gate. The schema checks "is the shape valid"; quality rules check "is the content acceptable" — e.g. "the new value isn't more than 2x the previous," "the writeback's row count is greater than 100," "the produced JSON parses as valid GeoJSON."

The two layers fire in order:
1. Dispatch-time JSON Schema validation (substituted input).
2. Executor runs.
3. Commit-time JSON Schema validation (writeback).
4. Commit-time quality-rule evaluation (writeback only; previous not wired at runner call site).
5. Persist (if all pass) or `attributes_schema_failed` / `quality_rule_failed` (if not).

This layering is documented across `docs/concepts/attributes.md` (schema part) and `modeling/qualityrule/spec.go` (rules part). The interaction with `@blessed-invariant 12` is precise: the dual-gate is mandatory, and quality-rule evaluation runs adjacent to (not in place of) the commit-time gate.

## Code surface

- `modeling/qualityrule/spec.go` — `Spec`, `Failure`, `EvalInput`, `Evaluator` interface (entire file, 46 lines).
- `modeling/qualityrule/eval/rules.go` — registry + three builtins + `EvaluateAll` (entire file, 197 lines, AGPL).
- `modeling/qualityrule/eval/rules_test.go` — co-located unit tests for the builtins.
- `modeling/node/template.go:64` — `QualityRules []qualityrule.Spec` field on `TemplateNodeDef`.
- `modeling/attribute/validate.go` — JSON Schema gate (`@blessed-invariant 12`).
- `foundation/integration/runner_terminal.go:120-126` — commit-time call to `runQualityRules`.
- `foundation/integration/runner_terminal.go:245-268` — `emitQualityRuleFailures` (one event per failure, single tx).
- `foundation/integration/runner_terminal.go:376-390` — `runQualityRules` helper wrapping `qreval.EvaluateAll`.
- `foundation/integration/commit_test.go` — commit-side test for the dual gate.
- `licensing.yml:25,57` — Apache (spec) / AGPL (eval) split.

## Prose surface

- `docs/concepts/attributes.md` — the schema-side gate (the prose primarily covers schema).
- `docs/concepts/deterministic-transformations.md` — quality-rule-driven transformations.
- `licensing.yml` — Apache/AGPL split for qualityrule.

## Adjacent topics

- `2026-05-10-attribute-substitution-grammar` — JSON Schema gate.
- `2026-05-10-opacity-of-userdata-claim-blob` — attribute opacity outside the substitution leaf.

## Observations

- The Apache/AGPL split (`spec.go` Apache; `eval/` AGPL per `licensing.yml`) means a third-party template author can consume the spec types without inheriting AGPL, but the runtime evaluators are AGPL. This is a careful licensing choice: the API is open, the implementation is copyleft.
- The `Evaluator` interface accepts `context.Context` but most evaluators are pure functions. The context is reserved for future evaluators that might call external services (the contract says "pure" but doesn't enforce it).
- The `severity` field defaults to `error` (per `spec.go:19`); the only branch that diverts is exact-string `"warning"` (`rules.go:58`). Anything else — `""`, `error`, or a typo like `Warn` — lands in the blocking `errors` slice. A future strongly-typed `shared.Severity` constant set would make this less footgun-shaped.
- The two layers are independent: a node can declare only quality rules (no attribute schema beyond an empty object), only an attribute schema, both, or neither. Practically, both are typically declared together.
- **Tension candidate (writeback-vs-previous):** the supervisor's call site at `runner_terminal.go:380-381` passes only `NewData`, leaving `PreviousData = nil`. The builtin `row_count_ratio` early-returns `true` when `PreviousData == nil` (`rules.go:78-80`), so the rule's headline use case — "new row count must be at least 50% of previous" — silently no-ops in production. Either the call site needs to read the prior attribute object before the merge, or the docs need to acknowledge that `PreviousData` is reserved for future use.
- **Tension candidate (custom-handler lifecycle):** `eval.Register` is a process-global mutable map (`rules.go:15-26`). A consumer that loads template specs at process start but registers custom handlers later in `main()` will see a window where `EvaluateAll` rejects valid templates with "no custom handler registered." There is no contract on registration ordering — it relies on consumer discipline.
- **Tension candidate (severity is per-rule, not per-instance):** a failing rule's severity is taken from the `Spec.Severity` (`rules.go:57`), not from the rule definition. A consumer can't deploy a single rule template that's `warning` in staging and `error` in prod without two template specs.
