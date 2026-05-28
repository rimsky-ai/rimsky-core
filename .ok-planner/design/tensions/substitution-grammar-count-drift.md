---
tension: substitution-grammar-count-drift
category: inconsistent
status: open
affects:
  - attribute
---

# Substitution grammar count drifts: 5 in some prose, 6 in code (event kind added separately)

## What is muddy

The grammar implemented at `graph/attribute/substitution.go` lists five source kinds inline. The sixth (`nodes.<emitter>.event.<name>.<json_path>`) was added under the platform-extensions design and is declared in the `ResolveContext.EventLookup` field comment in the same file separately.

CLAUDE.md and `docs/concepts/attributes.md` cite five kinds in some passes; the named-event kind is sometimes mentioned, sometimes omitted. A reader counting from any one surface gets a stale grammar.

## Why it matters

Template authors writing `{{nodes.emitter.event.name.path}}` need to know whether it's supported. The doc/code drift makes "what can I substitute" non-canonical.

## Resolution candidates (do NOT pick)

- Enumerate all six substitution source kinds in one canonical place — the attribute concept's definition — so the grammar count cannot drift across surfaces (see `concept:attribute`).
- Fold the event-lookup source kind into the single canonical grammar enumeration rather than declaring it separately, so the implementation lists all six kinds together (see `concept:named-event`).
- Add a guard that pins the substitution-grammar count, so an added or removed source kind cannot silently desynchronize the enumeration from the documented grammar.

## Evidence

- `_discover/2026-05-10-attribute-substitution-grammar.md` Observations bullet 1.
- `graph/attribute/substitution.go` (five kinds inline) vs the `ResolveContext.EventLookup` callback comment (sixth) in the same file.

## Notes

- 2026-05-19 — Partly addressed by `spec:2026-05-19-multi-instance-template-ergonomics-design`: `concept:attribute`'s Invariants section now reflects the current grammar (retired `deps.*`, added live `trigger.*`/`child.*`). The cross-doc sweep against other surfaces remains open.

