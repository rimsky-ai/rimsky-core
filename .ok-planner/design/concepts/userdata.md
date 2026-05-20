---
concept: userdata
status: as-is
aliases: []
references:
  - _discover/2026-05-10-opacity-of-userdata-claim-blob.md
  - _discover/2026-05-10-userdata-overrides-by-instance.md
---

# Userdata

## What it is

Userdata is an inert per-node JSON blob the template author attaches and the executor consumes verbatim. Carried on `proto:executor.proto::ExecuteRequest.userdata`. Never substituted. Never inspected by rimsky. Validated executor-side against the executor's declared `userdata_schema` if any. Inertness discipline cross-linked at `concept:inertness`.

## Purpose

Templates need a channel for "stuff the executor needs to read but rimsky shouldn't understand" — CLI flags, model selection, ad-hoc tuning, synthetic-blocker fields, tracing markers. Userdata is that channel; rimsky's only job is to forward the bytes.

## Boundaries

Owns: the bytes, the per-instance override merge mechanism, the routing-key validation. Does NOT own: substitution (see `attribute`), executor-side schema enforcement (see `executor.userdata_schema`), claim payload (see `claim`). Adjacent: `executor`, `inertness`.

## Purpose (in practice)

Escape-hatch for executor-specific config that rimsky should not need to learn about. Three primary uses:

1. **Synthetic-blocker scenarios** — executor configures internal sleep / wait state via per-node tuning.
2. **Per-run trace artifacts** — caller threads correlation IDs, span contexts, or audit hooks the executor consumes.
3. **Ad-hoc tuning** — per-node knobs the executor recognizes that rimsky does not (e.g., retry budgets, output format flags).

Per-instance overrides via `col:rimsky_instances.userdata_overrides` extend this with operator-level customization at instance-creation time (see `route:POST /instances`).

## Invariants

- Userdata is inert (`@blessed-invariant 11`). No substitution pass. No inspection. No validation beyond the executor-side schema check.
- Rimsky never substitutes, validates, or otherwise interprets userdata. The per-instance overrides merge is the only structural traversal of userdata content (handled by `code:foundation/shared/jsonmerge.go::DeepMergeJSON`).
- `{{...}}` directives in userdata are literal text reaching the executor verbatim; the substitution grammar does not include a `{{userdata.*}}` source kind.
- Per-instance `userdata_overrides` validate only routing keys (`by_executor`, `by_node`, plus the executor/node names). Fragment values are never inspected.
- Template-level `defaults.userdata.by_executor.<name>` validate only the routing key (the `<name>` must match a node's executor). Fragment values are never inspected.

## Per-instance overrides

Templates declare a per-node userdata blob; some operations (tracing, synthetic-blocker scenarios, ad-hoc tuning, per-run artifacts) need to alter that blob for one instance without forking the template. Per-instance overrides handle that.

Shape: `{by_executor: {<executor-name>: {...}}, by_node: {<node-name>: {...}}}`. Stored on `rimsky_instances.userdata_overrides`. Deep-merged at dispatch time in the order

```
template.defaults.userdata.by_executor[<executor>]
  → node.userdata
  → instance.userdata_overrides.by_executor[<executor>]
  → instance.userdata_overrides.by_node[<node>]
```

More specific wins; operator-level overrides win over template-author defaults. Merge helper is `code:foundation/shared/jsonmerge.go::DeepMergeJSON`.

Validation discipline (preserves `@blessed-invariant 11`):
- Inspects only routing keys (`by_executor`, `by_node`, plus the executor/node names which must be declared in the template). Fragment values never inspected.
- Unknown top-level keys are rejected at create-time.
- Nodes whose `executor_name` is null (claim-only path) get only `by_node[name]` overrides.

(Previously documented as a standalone concept `userdata-overrides`; folded here under `2026-05-11-design-log-convergence`. Added under the platform-extensions design 2026-05-08.)

## Aliases and historical names

CLAUDE.md "Common mistakes" calls out the confusion with cloud-init userdata (cloud-init parses; rimsky doesn't).

## Open within this concept

- The executor's `userdata_schema` (read by rimsky to validate userdata bytes at template-registration and dispatch time) is a sanctioned but unnamed exception to `@blessed-invariant 11` inertness — see `tensions/userdata-schema-as-opacity-exception.md`.

## Notes

- 2026-05-19 — Template-level userdata defaults added per spec 2026-05-19-multi-instance-template-ergonomics-design. `@blessed-invariant 11` unchanged: only routing keys (`by_executor` plus executor names) are inspected; fragment values are never read.

