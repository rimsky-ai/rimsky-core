---
topic: userdata-overrides-by-instance
kind: schema
---

# Per-instance `userdata_overrides`: rimsky inspects only routing keys, never fragment values

## Description

A template declares per-node userdata as a fixed opaque blob. Operators sometimes need to override that blob for a specific instance — for tracing, ad-hoc tuning, synthetic-blocker scenarios, per-run trace artifacts — without forking the template. Rimsky offers per-instance overrides at create-time, with shape-blind merging at dispatch.

`POST /instances` accepts a `userdata_overrides` body:

```json
{
  "by_executor": {"<executor-name>": { ...userdata-fragment... }},
  "by_node":     {"<node-name>":     { ...userdata-fragment... }}
}
```

Both top-level keys optional; unknown top-level keys rejected at create-time. Stored on `rimsky_instances.userdata_overrides` (migration `005-instance-userdata-overrides.sql`).

At dispatch time, `applyUserdataOverrides` (`foundation/integration/userdata_overrides.go:36`) deep-merges into per-node userdata in order: **template → `by_executor[<node's executor>]` → `by_node[<node's name>]`** (most specific wins). The merge helper `modeling/shared.DeepMergeJSON` is shared because both control-api and dispatch use it.

The validator at `modeling/controlapi/userdata_overrides.go:44-100` inspects **only** the routing keys — top-level `by_executor`/`by_node`, plus the executor names and node names — never the fragment values. The comment is explicit:

> The fragment values themselves are NOT inspected — they're userdata per @blessed-invariant 11. This validator only inspects keys and container shapes.

The validator rejects:

- Unknown top-level keys (anything other than `by_executor` and `by_node`).
- Executor names that aren't used by the locked template (a typo would be a silent no-op at dispatch otherwise).
- Node names that aren't declared in the template (same reason).

It does NOT validate:

- Fragment shape against any schema.
- Fragment values for any property.
- Fragment depth or size.

The merge at `foundation/integration/userdata_overrides.go:32-50` is "shape-blind": rimsky inspects only the routing-keys, never the userdata fragments themselves. The deep-merge walks JSON objects and arrays without knowing what's inside — the merge contract is that a more-specific override fragment replaces (objects merge by key, scalars/arrays replace by position).

`@blessed-invariant 11` (userdata is opaque) is preserved. The full per-node JSON Schema validation alternative is structurally rejected: it would violate invariant 11. The "shape-validate but not key-validate" alternative is rejected because typo'd executor names would silently no-op.

CLAUDE.md "Non-obvious gotchas" notes: "Per-instance userdata overrides exist. POST /instances accepts a userdata_overrides blob shaped { by_executor: {...}, by_node: {...} } that rimsky deep-merges into per-node userdata at dispatch time, ordered template → by_executor → by_node (most-specific wins). Validation at create-time inspects only routing-key names (executor / node) — never the fragment values, preserving invariant 11."

Use cases live executor-side: synthetic-blocker scenarios (inject a special value the executor recognizes), per-run trace artifacts, ad-hoc tuning. A future feature wanting to validate or transform override fragment values is structurally invariant-violating.

## Code surface

- `foundation/persistence/postgres/migrations/005-instance-userdata-overrides.sql` — schema.
- `foundation/persistence/instances.go:18-50` — `rimsky_instances.userdata_overrides` JSONB column annotation.
- `foundation/integration/userdata_overrides.go` — entire file; merge.
- `modeling/controlapi/userdata_overrides.go` — entire file; validator.
- `modeling/shared/jsonmerge.go` — `DeepMergeJSON` helper.

## Prose surface

- `CLAUDE.md` "Non-obvious gotchas" — full description.
- `docs/concepts/userdata.md` — userdata opacity rule.
- `docs/concepts/instance.md` — instance-creation API.

## Adjacent topics

- `2026-05-10-opacity-of-userdata-claim-blob` — invariant 11 governs this.
- `2026-05-10-attribute-substitution-grammar` — overrides do NOT pass through substitution (userdata is opaque).
- `2026-05-10-content-addressed-templates` — overrides are per-instance, not per-template.

## Observations

- The validator's "reject executor/node names not in template" check requires the template to be locked at instance-create time; an instance against an undeployed template can't be created anyway, and the lock is the same. CLAUDE.md "Non-obvious gotchas" notes this is to catch silent no-op typos.
- `DeepMergeJSON` is a hand-rolled merge in `modeling/shared/` rather than a third-party library (per `2026-05-10-stdlib-slog-and-minimal-deps`). The merge semantics (objects merge by key; arrays replace; scalars replace) are simple enough that a third-party library wasn't justified.
- The merge ordering (template → by_executor → by_node) is "most specific wins"; a node that runs against executor X gets `by_executor[X]` applied before its `by_node[name]`. A node whose `executor_name` is null (claim-only path) gets only the `by_node[name]` overrides.
- The use-cases live "executor-side" per the consequences. The executor can recognize "synthetic-blocker fields" or "tracing flags" in the userdata that rimsky doesn't and won't ever interpret.
