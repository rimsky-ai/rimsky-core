---
concept: attribute
status: as-is
aliases: []
---

# Attribute

## What it is

Attributes are the typed inputs, outputs, and configuration of a node, declared by a JSON Schema in the template's `attributes:` block. Each schema property is one of three shapes: source-bound (`source:` directive resolved at dispatch), static-default (`default:` value resolved at registration), or executor-written (populated at commit by the executor; marked `readOnly: true` in the executor's expected-attributes schema). Persisted writeback lives in the per-run attribute ledger's data column. Validation runs twice (dispatch post-substitution + commit post-writeback).

## Purpose

Attributes give nodes a typed, validated contract for their inputs and outputs. The substitution grammar lets downstream nodes consume upstream outputs and live claim payloads without rimsky understanding the data; the schema gates catch shape problems on both sides.

## Boundaries

Owns: the schema, the substitution grammar, the three property shapes, the override merge across the four layers, the two validation gates, the writeback ledger. The template-key name and Go-package name are both `attributes`. Does NOT own: claim payload (lives on `claim`), assets (assets are claims, not attributes — see `concept:asset`), semantic validation (there is no separate semantic-validation concept; today the verifier-executor pattern, a co-holding verifier node that runs shape checks, covers that surface). Adjacent: `node`, `named-event`, `inertness`, `asset`.

Clarifying note: attributes are typed node I/O; assets are claims with `lifetime: durable` against a data-processing-capable producer. Templates author both side-by-side — attributes for transient run inputs/outputs, assets for durable datasets. Don't conflate.

Clarifying note on arity: per-field substitution is 1:1 by design — one `source:` directive names one value. Multi-upstream fan-in is the cascade vocabulary's job, expressed through `concept:node-subscription` (N upstreams per receiver) and optional schema fields (the dispatch path omits non-required fields on a missing source). The arity asymmetry is load-bearing — see the per-field-arity invariant.

Clarifying note on subgraph sealing: subgraphs are sealed. Internal nodes can read from siblings of the same invocation, the calling node's attributes, and the always-available source kinds (`params`, claims, trigger messages, `child.partition_key`) — but not from upstream nodes in the calling graph by free reference. The calling graph's namespace is not visible inside the subgraph. Authors thread calling-graph state through the calling node explicitly.

## Non-goals

Patterns considered carefully during platform design and **decided against**. These are positions, not deferrals — future agents reaching for these patterns should argue against this section's rationale rather than treating them as open backlog.

- **Cross-frame attribute caching.** A `{{nodes.X.attribute.Y}}` read at receiver R's dispatch resolves only against the X-run that contributed to R's dispatch via this frame's wait-set. Reads of X-runs from earlier frames return a missing-source error. The per-run attribute rows are the persistent record of what each node-run produced — not a cache. State that must be available across frames belongs in `params`, claim payloads, or threaded subgraph inputs.
- **Function-form substitution grammar.** No `{{coalesce(X, Y)}}`, `{{newest(X, Y)}}`, `{{merge(X, Y)}}`, or other in-grammar functions. The grammar stays a closed enumeration of source-kind directives plus an optional literal fallback. Aggregation and transformation logic lives in receiver executors, not in the substitution layer.
- **Multi-pipe fallback chains.** A single directive admits at most one `| <literal>` fallback. Multi-directive chains (`{{X | Y | Z}}`) and composite literals (`{}`, `[]` as fallbacks) are not admitted. Per-directive `?` marker and `| <literal>` fallback are mutually exclusive (incoherent: `?` says null on missing, `|` says literal on missing — pick one).
- **Closure semantics for subgraphs.** Subgraph internal nodes cannot read attributes from upstream nodes in the calling graph by free reference (see Boundaries above). Calling-graph state threads through the calling node explicitly.
- **`force_fresh: true` (always-re-execute), `pull_only: true` (suppress auto-subscribe), `trigger_if_missing: true` (lazy upstream initialization).** None of these flags exist. The configuration surface is exactly `hard_dep: true` on attribute schema properties whose source is `{{nodes.<X>.attribute.<Y>}}`.

## Invariants

- Validation gates twice: dispatch (post-substitution) and commit (executor writeback). Both mandatory (`@blessed-invariant 12`).
- The substitution grammar is a closed enumeration of source kinds: `nodes.<X>.attribute.<field-path>`, `nodes.<X>.event.<name>.<field-path>`, `claim.<alias>.{address|scope|payload.<field-path>}`, `params.<field-path>`, `trigger.message.payload.<field-path>`, `child.partition_key`. Each path-walking kind admits an optional-empty trailing path; with an empty trailing path the directive resolves to the kind's JSON root. Resolution is either whole-directive (the input is exactly one `{{...}}` directive modulo whitespace; returns the JSON value verbatim) or embedded (the input has literal text alongside directives; stringifies and concatenates). The grammar also admits a fallback operator: `{{<directive> | <literal>}}` returns the directive's value if present, else the literal (one of `null`, `true`, `false`, a JSON number, or a quoted string). Multi-directive chains (`{{X | Y | Z}}`) and composite literals (objects, arrays) are not admitted. The `deps.<X>.<Y>` form is not part of the grammar and is rejected with a migration-pointer error.
- Errors omit value bytes (cite path tokens only) to preserve `@blessed-invariant 20`/`21`.
- Attribute storage is per-run, keyed by the node-run identity (a cascade-deleting foreign key to the node-run row). A denormalized node-id column supports forensic / observability lookups by latest-per-node; the dispatch-time substitution path looks up by run against the wait-set sender runs that contributed to this dispatch in this frame.
- Per-field `source:` admits an opt-in `hard_dep: true` flag on `nodes.<X>.attribute.<Y>` reads. When set, the cascade walker proactively invalidates the upstream so its value is available in the current frame. Hard-dep cycles are rejected at template registration by the hard-dep edge builder.
- Substitution reads are scoped to the current frame. A `{{nodes.X.attribute.Y}}` directive resolves to the X-run that contributed to this dispatch via the frame's wait-set; reads of X-runs from earlier frames return a missing-source error. The per-run attribute rows are the persistent record of what each node-run produced — not a cache. State that must be available across frames belongs in `params`, claim payloads, or threaded subgraph inputs.
- Per-field `source:` admits literal text and one or more `{{...}}` directives. Each directive resolves independently against its source kind (`nodes`, `claim`, `params`, `trigger`, `child`). Per-directive strict-default with `?` opt-in to lenient. Lenient missing rendering depends on resolution mode: in whole-directive mode (the source is exactly one `{{...}}` directive, modulo whitespace) the directive lifts to JSON `null`; in embedded mode (literal text alongside directives) the directive contributes the empty string so the surrounding template still concatenates cleanly. The `?` marker is mutually exclusive with `| <literal>` fallback. Multi-source array form (`source: [...]`) and multi-pipe chains (`{{X | Y | Z}}`) are not admitted. Many-to-many fan-in across upstreams lives in the cascade vocabulary (subscriptions over multiple senders, plus optional schema fields whose dispatch-time missing-source is silently omitted by the schema-substitution path). Enforced at registration by the template validator's attribute-source check (rejects the declined forms). The arity asymmetry between subscriptions (many-to-many) and per-field substitution (1:1 per directive) is intentional: subscriptions sum signals across upstreams; substitution names a single value per field. Per-directive composition within a source string concatenates, it does not sum.

- Each property has at most one of `source:` or `default:`. Each property satisfies one of: has `source:`, has `default:`, is marked `readOnly: true` in the executor's `expected_attributes_schema` (executor-write-back populates at commit), or it is a property the executor does not enumerate, under an executor schema that does not constrain it — either the executor declares no `properties` block at all (a fully permissive schema), or it declares `additionalProperties` that is not `false`. In both cases the executor has delegated naming authority for unenumerated properties. Properties failing all of these checks are rejected at registration with `template_validation_failed`, or at dispatch with `attributes_schema_invalid` if the executor's schema wasn't visible at registration time. The registration-time check soft-fails the `readOnly`-fallback leg when the discovery cache hasn't populated yet (no hook wired, executor not yet handshaked, or executor advertises no schema) — under those conditions, runtime dispatch reapplies the rule once the schema is visible. Enforced by the template validator's attributes-schema check at registration and its effective-schema check at dispatch (the latter invoked from the dispatch-time attribute-resolution path).

- The template-author L2 declaration cannot set `readOnly: true` on a property the executor's schema does not also mark `readOnly: true`. Rejected at registration. The executor is authoritative on which of its attributes it produces vs consumes. The same open-schema carve-out applies: an unenumerated property under an executor schema that does not constrain it (no `properties` block, or `additionalProperties` not `false`) may be author-marked `readOnly: true`, since the executor has delegated naming authority for unenumerated properties. Enumerated properties remain fully checked.

- A fifth override layer (L5) extends the four-layer merge: `instance.attribute_overrides.by_match` is an ordered list of `{matcher, overlay}` entries. The matcher predicate is equality-only over a fixed key set (`node_type`, `executor`, `graph`, `child_key`, `attrs.<path>`); evaluated against the dispatch context at runtime; missing keys are wildcards; AND across present keys. Each matching entry's overlay folds on top via a recursive JSON deep-merge in declaration order — later entries win. Empty matcher (`{}`) matches every dispatch. The matcher reads from the post-L4 merged bag (overrides applied through L4 are visible to the matcher). Ordinal-shaped matcher keys (`dispatch_index`, `nth_child`, `partition_index`, `seq`) and expression-shaped values are rejected at registration. Enforced by the control-api override validator at registration and the runtime override applier at dispatch.

## Static-default properties

A schema property declared with `default: <value>` and no `source:` is a static-default property. Its value is set from the effective schema at registration; instance-level overrides (`attribute_overrides.by_executor.<exec>.<attr>` or `attribute_overrides.by_node.<node>.<attr>`) replace the default at dispatch.

Static-default properties carry per-node executor configuration (model selection, CLI flags, fixed prompts) declared by template authors and overridable by operators at instance time. The substitution grammar does not apply to default values; an operator-supplied `"{{X}}"` in an override is a literal string.

Static-default values are persisted per node-run alongside source-resolved and executor-written values in the per-run attribute ledger's data column, providing dispatch-time forensic clarity. Template-default mutations do not retroactively rewrite history.

## Matcher overlay (by_match)

A third routing dimension on `attribute_overrides`, alongside the static `by_executor` (L3) and `by_node` (L4) maps. `by_match` is an ordered list of `{matcher, overlay}` entries where the matcher is a content-keyed predicate over dispatch-time identity — solving the problem that static routes can't differentiate among children of a fan-out node that share node type and executor.

The matcher grammar is intentionally small: equality only, over a fixed key set. `child_key` is the recommended anchor for fan-out routing (the producer-emitted per-sub-scope identifier from `concept:fan-out`, stable across dispatch reorderings); `attrs.<path>` covers non-fan-out differentiation. Ordinal-style addressing (any "third call" / "index N" semantics) is rejected at registration: matchers address partitions by identity, never by execution order.

The `child_key` matcher key sources its value from the dispatched run's RunScope's `partition_key` (per `concept:run-scope`); the equality semantics and ordinal-rejection vocabulary are fixed. Non-fan-out dispatches see an empty-string `child_key` (the parent / sub-graph RunScope's `partition_key` is empty).

Override values are static — no substitution applied. The matcher reads from the post-L3+L4 bag, meaning earlier-layer overrides are visible to the matcher's `attrs.<path>` comparisons.

Per-entry match counters persist on the instance row's match-count map. The supervisor increments after the merge returns, in a short dedicated transaction. Operators and tests read the counter from the instance-fetch endpoint and assert on which entries fired. Entries that never match show 0 at instance terminal — the "silent miss becomes loud miss" discipline that makes matcher-overlay testing safe against producer key-scheme changes.
