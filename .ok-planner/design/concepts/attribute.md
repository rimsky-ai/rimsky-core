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

Owns: the schema, the substitution grammar, the two validation gates, the writeback ledger. Does NOT own: userdata (separate opaque stream), claim payload (lives on `claim`), quality rules (semantic validation lives in `quality-rule`). Adjacent: `substitution`, `node`, `quality-rule`, `userdata` (deliberately separate), `named-event`.

## Invariants

- Validation gates twice: dispatch (post-substitution) and commit (executor writeback). Both mandatory (`@blessed-invariant 12`).
- The substitution grammar is a closed enumeration: six source kinds (`deps.<node>.<field>`, `claim.<alias>.address`, `claim.<alias>.payload.<field>`, `claim.<alias>.scope`, `params.<key>`, `nodes.<emitter>.event.<name>.<json_path>`). Nothing else is recognized.
- Errors omit value bytes (cite path tokens only) to preserve `@blessed-invariant 20`/`21`.

## Aliases and historical names

None live. `attributes:` is the template-key name and Go-package name.

## Open within this concept

- The "single sanctioned introspection site" claim is actually three sites (`walkPath`, `stringifyRaw`, `makeStoreHandle`) — see `tensions/substitution-introspection-site-count.md`.
- The grammar lists six kinds inline but CLAUDE.md / `docs/concepts/attributes.md` cite five in some passes — see `tensions/substitution-grammar-count-drift.md`.

