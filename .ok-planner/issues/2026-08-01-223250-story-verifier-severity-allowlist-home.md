---
issue: story-verifier-severity-allowlist-home
kind: sprint
category: stories-prescriptive
artifacts:
  - story:verifier-severity-partition
status: promoted
sprint: 2026-08-01-ruled-intake-drain.md
opened: 2026-08-01T22:32:50Z
---

# Does the verifier's severity grammar deserve a corpus home?

The bundled shape-checks verifier (the shipped executor that validates data against declared checks) partitions its checks by severity: errors block, warnings don't. Its story promises in prose how the severity field is validated — empty defaults to error; only error and warning are accepted; anything else is rejected with a structured error before any check runs. The format rules force the story down to its sentence, and the question is whether that validation grammar needs a durable home first.

Re-verification confirms the code does exactly this (`code:lib/services/executors/verifier-shape-checks/parseSeverity`, called before any check executes). No verifier concept exists and no decision mentions severity — the corpus is silent. The grammar has the shape of ordinary input validation: a two-value config field with a default and fail-fast rejection. There is a nameable alternative (an open vocabulary, or defaulting to warning), but nothing downstream branches on the vocabulary being closed — unlike, say, error-class routing, no other component's correctness leans on it.

## Options

- Record the severity grammar as a decision — durable, but it enshrines a config-field validation default as an architectural choice, a precedent that would justify a decision for every validated enum field.
- Rule it below corpus altitude and reduce the story — the grammar stays owned by the executor's code, schema, and tests.

The ruling decides whether a validated two-value config field is corpus content. Note the sibling `issue:intent-verifier-shape-checks-executor-uncited` — the same executor currently has no corpus presence at all; if a story is added there, these should be drafted together.

## Ruling

> Recommended ruling (/verify-issues): rule the severity grammar below corpus altitude and reduce the story to its sentence — the partition behavior (errors block, warnings don't) is the promise; the field validation is implementation the executor's schema and tests own.
>
> Rationale: the decision option fails the corpus's own bar — a validated enum with a sensible default is a default, not a choice, and nothing downstream depends on the vocabulary's closedness — whereas the sibling claude-agent taxonomy recommendation goes the other way precisely because error-policy routing does depend on that set being closed. Flip case: if severity ever becomes a shared vocabulary across multiple verifiers or a routing input, it graduates to a real grammar decision.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
