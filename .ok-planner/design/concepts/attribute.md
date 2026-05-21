---
concept: attribute
status: as-is
aliases: []
references:
  - _discover/2026-05-10-attribute-substitution-grammar.md
  - _discover/quality-rules-and-attribute-validation.md
---

# Attribute

## What it is

Attributes are the typed inputs and outputs of a node, declared by a JSON Schema in the template's `attributes:` block. The schema's `source:` fields carry `{{...}}` substitution directives resolved at dispatch. Persisted writeback lives in `rimsky_node_attributes.data`. Validation runs twice (dispatch post-substitution + commit post-writeback).

## Purpose

Attributes give nodes a typed, validated contract for their inputs and outputs. The substitution grammar lets downstream nodes consume upstream outputs and live claim payloads without rimsky understanding the data; the schema gates catch shape problems on both sides.

## Boundaries

Owns: the schema, the substitution grammar, the two validation gates, the writeback ledger. Does NOT own: userdata (separate inert stream — see `concept:inertness`), claim payload (lives on `claim`), assets (assets are claims, not attributes — see `concept:asset`), semantic validation (the retired `quality-rule` concept; today the verifier-executor pattern covers that surface — see `executors/verifier-shape-checks/`). Adjacent: `node`, `userdata` (deliberately separate), `named-event`, `inertness`, `asset`.

Clarifying note (per 2026-05-15 data-platform-extensions): attributes are typed node I/O; assets are claims with `lifetime: durable` against a `DataProcessing`-capable producer. Templates author both side-by-side — attributes for transient run inputs/outputs, assets for durable datasets. Don't conflate.

Clarifying note on arity: per-field substitution is 1:1 by design — one `source:` directive names one value. Multi-upstream fan-in is the cascade vocabulary's job, expressed through `concept:node-subscription` (N upstreams per receiver) and optional schema fields (the dispatch path omits non-required fields on `ErrMissingSource`). The arity asymmetry is load-bearing — see the per-field-arity invariant.

Clarifying note on subgraph sealing: subgraphs are sealed. Internal nodes can read from siblings of the same invocation, the calling node's attributes, and the always-available source kinds (`params`, claims, trigger messages, `child.partition_key`) — but not from upstream nodes in the calling graph by free reference. The calling graph's namespace is not visible inside the subgraph. Authors thread calling-graph state through the calling node explicitly.

## Non-goals

Patterns considered carefully during platform design and **decided against**. These are positions, not deferrals — future agents reaching for these patterns should argue against this section's rationale rather than treating them as open backlog.

- **Cross-frame attribute caching.** A `{{nodes.X.attribute.Y}}` read at receiver R's dispatch resolves only against the X-run that contributed to R's dispatch via this frame's wait-set. Reads of X-runs from earlier frames return `ErrMissingSource`. `rimsky_node_attributes` rows are the persistent record of what each node-run produced — not a cache. State that must be available across frames belongs in `params`, claim payloads, or threaded subgraph inputs.
- **Function-form substitution grammar.** No `{{coalesce(X, Y)}}`, `{{newest(X, Y)}}`, `{{merge(X, Y)}}`, or other in-grammar functions. The grammar stays a closed enumeration of source-kind directives plus an optional literal fallback. Aggregation and transformation logic lives in receiver executors, not in the substitution layer.
- **Multi-directive fallback chains.** The fallback operator `{{<directive> | <literal>}}` admits exactly one directive on the left and exactly one JSON literal (`null`, boolean, number, or quoted string) on the right. Multi-directive chains (`{{X | Y | Z}}`) and composite literals (`{}`, `[]`) are not admitted.
- **Closure semantics for subgraphs.** Subgraph internal nodes cannot read attributes from upstream nodes in the calling graph by free reference (see Boundaries above). Calling-graph state threads through the calling node explicitly.
- **`force_fresh: true` (always-re-execute), `pull_only: true` (suppress auto-subscribe), `trigger_if_missing: true` (lazy upstream initialization).** None of these flags exist. The configuration surface is exactly `hard_dep: true` on attribute schema properties whose source is `{{nodes.<X>.attribute.<Y>}}`.

See `.ok-planner/history/specs/2026-05-20-attribute-pull-resolution-design.md` for the brainstorm rationale per item.

## Invariants

- Validation gates twice: dispatch (post-substitution) and commit (executor writeback). Both mandatory (`@blessed-invariant 12`).
- The substitution grammar is a closed enumeration of source kinds: `nodes.<X>.attribute.<field-path>`, `nodes.<X>.event.<name>.<field-path>`, `claim.<alias>.{address|scope|payload.<field-path>}`, `params.<field-path>`, `trigger.message.payload.<field-path>`, `child.partition_key`. Each path-walking kind admits an optional-empty trailing path; with an empty trailing path the directive resolves to the kind's JSON root. Resolution is either whole-directive (the input is exactly one `{{...}}` directive modulo whitespace; returns the JSON value verbatim) or embedded (the input has literal text alongside directives; stringifies and concatenates). The grammar also admits a fallback operator: `{{<directive> | <literal>}}` returns the directive's value if present, else the literal (one of `null`, `true`, `false`, a JSON number, or a quoted string). Multi-directive chains (`{{X | Y | Z}}`) and composite literals (objects, arrays) are not admitted. The legacy `deps.<X>.<Y>` form is retired and rejected with a migration-pointer error.
- Errors omit value bytes (cite path tokens only) to preserve `@blessed-invariant 20`/`21`.
- Attribute storage is per-run, keyed by `node_run_id` (foreign key to `rimsky_node_runs` with `ON DELETE CASCADE`). A denormalized `node_id` column supports forensic / observability lookups via `GetLatestByNode`; the dispatch-time substitution path uses `GetByRun` against wait-set sender_run_ids that contributed to this dispatch in this frame.
- Per-field `source:` admits an opt-in `hard_dep: true` flag on `nodes.<X>.attribute.<Y>` reads. When set, the cascade walker proactively invalidates the upstream so its value is available in the current frame. Hard-dep cycles are rejected at template registration via `BuildHardDepEdges`.
- Substitution reads are scoped to the current frame. A `{{nodes.X.attribute.Y}}` directive resolves to the X-run that contributed to this dispatch via the frame's wait-set; reads of X-runs from earlier frames return `ErrMissingSource`. `rimsky_node_attributes` rows are the persistent record of what each node-run produced — not a cache. State that must be available across frames belongs in `params`, claim payloads, or threaded subgraph inputs.
- Per-field `source:` arity is 1 — each attribute property declares exactly one substitution directive. Many-to-many fan-in across upstreams lives in the cascade vocabulary (subscriptions over multiple senders, plus optional schema fields whose dispatch-time `ErrMissingSource` is silently omitted at `code:runtime/runner_dispatch.go::substituteAttributesSchema`). Enforced at registration by `code:graph/node/template_validator.go::checkAttributeSource` (rejects any `source:` that isn't exactly one `{{...}}` directive with no surrounding text). The arity asymmetry between subscriptions (many-to-many) and substitution (per-field 1:1) is intentional: subscriptions sum signals across upstreams; substitution names a single value per field.

## Aliases and historical names

None live. `attributes:` is the template-key name and Go-package name.

## Open within this concept

- The "single sanctioned introspection site" claim is actually three sites (`walkPath`, `stringifyRaw`, `code:runtime/runner_dispatch.go::makeClaimHandle`) — see `tensions/substitution-introspection-site-count.md`.
- The grammar lists six kinds inline but CLAUDE.md / `docs/concepts/attributes.md` cite five in some passes — see `tensions/substitution-grammar-count-drift.md`.

## Notes

- 2026-05-19 — Grammar text corrected (retired `deps.*`, added live `trigger.*` and `child.*`) and whole-directive value-lift documented per spec 2026-05-19-multi-instance-template-ergonomics-design. Adjacent `tensions/substitution-grammar-count-drift.md` is partly addressed by this update; the cross-doc-prose sweep (CLAUDE.md, `docs/concepts/attributes.md`) remains open.
- 2026-05-19 — Embedded-mode `Substitute` (the string-returning entry point) now JSON-encodes composite bare-form pulls (object / array) via `json.Marshal` so the resulting string carries a well-formed JSON shape rather than Go's default `%v` formatting. This applies wherever `Substitute` (not `SubstituteValue`) accepts a directive that resolves to a composite — notably `{{claim.<alias>.payload}}` (which acquired bare-form support per this spec) and any analogous bare-form `nodes.X.attribute` or `trigger.message.payload` directive embedded alongside literal text. Call sites unchanged: `runtime/runner_locks.go` (lock-name and selector substitution) and `runtime/runner_dispatch.go` (the attribute-schema path resolves via `SubstituteValue`, which lifts composites directly). Per pre-v1 "break freely"; matches `SubstituteValue`'s lift behaviour at the embedded path.
- 2026-05-20 — Multi-source attribute substitution proposal declined. Sketch archived to `.ok-planner/history/sketches/2026-05-19-multi-source-attribute-substitution.md`; the per-field-arity invariant and Boundaries clarification above were added by this spec. Rationale: a first-non-missing fallback semantic loses signal (subscriptions fire on each upstream transition, but substitution would collapse to one candidate); an array-as-value semantic collapses to today's 1:1 schema with optional fields plus auto-subscribe; the read-vs-cascade arity split is the load-bearing distinction. See `.ok-planner/history/specs/2026-05-20-multi-source-substitution-decline-design.md` for the full reasoning trail.
- 2026-05-20 — Per-run keying lift + minimalist substitution model. `rimsky_node_attributes` re-keyed from `node_id` to `node_run_id`, completing the 2026-05-15 run-tree extension's "all state-bearing columns" intent. Substitution context at dispatch reads only drained wait-set rows for this receiver in this frame (topic_kind='attribute', settled-success senders); no scope-walk, no cross-frame caching. Per-field `hard_dep: true` flag opt-in for "ensure upstream is invalidated in this frame," with cascade-walker proactive invalidation via `BuildHardDepEdges`. Fallback operator `{{<directive> | <literal>}}` added. New `## Non-goals` section above captures load-bearing decisions about what this concept deliberately does NOT support. The 2026-05-20 multi-source decline (per-field arity 1) remains intact — the fallback operator is "exactly one directive + one literal," not multi-source. See `.ok-planner/history/specs/2026-05-20-attribute-pull-resolution-design.md`.

