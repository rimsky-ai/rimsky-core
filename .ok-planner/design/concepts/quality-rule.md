---
concept: quality-rule
status: as-is
aliases: []
references:
  - _discover/quality-rules-and-attribute-validation.md
---

# Quality rule

## What it is

A template-node-level declarative validation against a node's writeback. Each rule is `Spec{Type, Config, Severity}` (`modeling/qualityrule/spec.go:16-20`); the field on the node lives at `modeling/node/template.go:64` as `QualityRules []qualityrule.Spec`. Evaluators implement the `Evaluator` interface (`modeling/qualityrule/spec.go:43-45`):

```go
Evaluate(ctx context.Context, input EvalInput) (passed bool, details string, err error)
```

`EvalInput` carries `NewData` (proposed writeback), `PreviousData` (nullable), and `Cfg` (the rule's `Spec.Config`). Three builtins are pre-registered at `init()` in `modeling/qualityrule/eval/rules.go:149-155`:

- `row_count_ratio` — `len(new) >= cfg["min_ratio"] * len(previous)`; early-returns true when `PreviousData == nil`.
- `no_nulls` — every row in `[]map[string]any` has non-null values for each named field.
- `nullable_fields_present` — every row has each named field key present (may be null).

Consumers register additional evaluators under arbitrary names via `eval.Register(name, ev)` (the registry is a `sync.RWMutex`-guarded process-global map). The literal type name `custom` is reserved by the registry — a lookup returns "no custom handler registered" rather than the generic "unknown rule type" error.

Spec types live in `modeling/qualityrule/` (Apache); runtime evaluators live in `modeling/qualityrule/eval/` (AGPL per `licensing.yml:25,57`).

## Purpose

JSON Schema gates shape; quality rules gate content. "The new row count must be at least 50% of the previous run," "no null values in column X," "writeback parses as valid GeoJSON" — semantic checks that don't fit a schema.

## Boundaries

Owns: the per-rule declarative spec, the `Evaluator` interface, the severity partition, the commit-time evaluation site, the in-process registry. Does NOT own: schema shape validation (see `attribute`), executor-side checks (those are the executor's), the partition's downstream policy routing (see `error-policy`). Adjacent: `attribute`, `node`, `module-layout`, `error-policy`.

## Invariants

- Evaluation fires adjacent to (not in place of) the commit-time JSON Schema gate (`@blessed-invariant 12`). The supervisor calls `runQualityRules` at `foundation/integration/runner_terminal.go:120-126`, immediately after the schema gate passes.
- `EvaluateAll` partitions failures by `Severity` (`modeling/qualityrule/eval/rules.go:39-67`): **only the literal string `"warning"` diverts to the warnings slice; every other value — empty, `error`, or a typo — lands in the blocking errors slice**.
- Per-rule evaluator failure (returned `err`) short-circuits with a wrapped error; an unknown type name short-circuits identically. The supervisor wraps such short-circuit errors into a synthetic `Failure{RuleType: "evaluation_error", Severity: SeverityError}` so the commit-time emit always sees a structured `Failure` slice.
- On any non-warning failure, the supervisor emits one `quality_rule_failed` event per failure (`emitQualityRuleFailures` at `foundation/integration/runner_terminal.go:245-268`, batched in a single tx) and routes through `applyTerminalAppError` with `error_class="quality_rule_failed"`.
- Spec types are public-surface Apache; runtime evaluators are AGPL.

## Aliases and historical names

None live.

## Open within this concept

- The licensing split inside `modeling/qualityrule/` (spec Apache, eval AGPL) is unusual within a single concept — adjacent to `module-layout` (Licensing boundary subsection).
- Severity-as-string-equality partition: only `"warning"` diverts; typos silently block — see `tensions/quality-rule-severity-string-footgun.md`.
- Custom-handler registration ordering relative to template load — see `tensions/quality-rule-custom-handler-ordering.md`.

