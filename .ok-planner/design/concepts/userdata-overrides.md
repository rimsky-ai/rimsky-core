---
concept: userdata-overrides
status: as-is
aliases: []
references:
  - _discover/2026-05-10-userdata-overrides-by-instance.md
  - _discover/2026-05-10-opacity-of-userdata-claim-blob.md
---

# Userdata overrides

## What it is

Per-instance overrides for template-declared userdata. Shape: `{by_executor: {<executor-name>: {...}}, by_node: {<node-name>: {...}}}`. Stored on `rimsky_instances.userdata_overrides`. Deep-merged at dispatch in order: `template → by_executor[<executor>] → by_node[<node>]` (most-specific wins). Merge helper is `modeling/shared.DeepMergeJSON`.

## Purpose

Templates declare per-node userdata as a fixed blob. Some operations (tracing, synthetic-blocker scenarios, ad-hoc tuning, per-run artifacts) need to alter the blob for one instance without forking the template.

## Boundaries

Owns: the merge order, the create-time routing-key validation, the JSONB storage. Does NOT own: userdata content (still opaque), executor-side schema (still the executor's concern). Adjacent: `userdata`, `instance`, `executor`, `opacity`.

## Invariants

- Validation inspects only routing keys (`by_executor`, `by_node`, plus executor/node names that must be declared in the template). Fragment values never inspected — preserves `@blessed-invariant 11`.
- Merge ordering is `template → by_executor → by_node` (more specific wins).
- Unknown top-level keys are rejected at create-time.
- Nodes whose `executor_name` is null (claim-only path) get only `by_node[name]` overrides.

## Aliases and historical names

None live; the feature was added under the platform-extensions design (2026-05-08).

## Open within this concept

(no live tensions distinct from `userdata` and `opacity`)

