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

## Invariants

- Validation gates twice: dispatch (post-substitution) and commit (executor writeback). Both mandatory (`@blessed-invariant 12`).
- The substitution grammar is a closed enumeration of source kinds: `nodes.<X>.attribute.<field-path>`, `nodes.<X>.event.<name>.<field-path>`, `claim.<alias>.{address|scope|payload.<field-path>}`, `params.<field-path>`, `trigger.message.payload.<field-path>`, `child.partition_key`. Each path-walking kind admits an optional-empty trailing path; with an empty trailing path the directive resolves to the kind's JSON root. Resolution is either whole-directive (the input is exactly one `{{...}}` directive modulo whitespace; returns the JSON value verbatim) or embedded (the input has literal text alongside directives; stringifies and concatenates). The legacy `deps.<X>.<Y>` form is retired and rejected with a migration-pointer error.
- Errors omit value bytes (cite path tokens only) to preserve `@blessed-invariant 20`/`21`.

## Aliases and historical names

None live. `attributes:` is the template-key name and Go-package name.

## Open within this concept

- The "single sanctioned introspection site" claim is actually three sites (`walkPath`, `stringifyRaw`, `code:runtime/runner_dispatch.go::makeClaimHandle`) — see `tensions/substitution-introspection-site-count.md`.
- The grammar lists six kinds inline but CLAUDE.md / `docs/concepts/attributes.md` cite five in some passes — see `tensions/substitution-grammar-count-drift.md`.

## Notes

- 2026-05-19 — Grammar text corrected (retired `deps.*`, added live `trigger.*` and `child.*`) and whole-directive value-lift documented per spec 2026-05-19-multi-instance-template-ergonomics-design. Adjacent `tensions/substitution-grammar-count-drift.md` is partly addressed by this update; the cross-doc-prose sweep (CLAUDE.md, `docs/concepts/attributes.md`) remains open.
- 2026-05-19 — Embedded-mode `Substitute` (the string-returning entry point) now JSON-encodes composite bare-form pulls (object / array) via `json.Marshal` so the resulting string carries a well-formed JSON shape rather than Go's default `%v` formatting. This applies wherever `Substitute` (not `SubstituteValue`) accepts a directive that resolves to a composite — notably `{{claim.<alias>.payload}}` (which acquired bare-form support per this spec) and any analogous bare-form `nodes.X.attribute` or `trigger.message.payload` directive embedded alongside literal text. Call sites unchanged: `runtime/runner_locks.go` (lock-name and selector substitution) and `runtime/runner_dispatch.go` (the attribute-schema path resolves via `SubstituteValue`, which lifts composites directly). Per pre-v1 "break freely"; matches `SubstituteValue`'s lift behaviour at the embedded path.

