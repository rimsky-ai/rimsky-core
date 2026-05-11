---
tension: substitution-grammar-count-drift
category: inconsistent
status: open
affects:
  - attribute
---

# Substitution grammar count drifts: 5 in some prose, 6 in code (event kind added separately)

## What is muddy

The grammar implemented at `modeling/attribute/substitution.go:7-13` lists five source kinds inline. The sixth (`nodes.<emitter>.event.<name>.<json_path>`) was added under the platform-extensions design and is declared in the `ResolveContext.EventLookup` field comment at line 72-90 separately.

CLAUDE.md and `docs/concepts/attributes.md` cite five kinds in some passes; the named-event kind is sometimes mentioned, sometimes omitted. A reader counting from any one surface gets a stale grammar.

## Why it matters

Template authors writing `{{nodes.emitter.event.name.path}}` need to know whether it's supported. The doc/code drift makes "what can I substitute" non-canonical.

## Resolution candidates (do NOT pick)

- Tabulate all six kinds in one canonical place (`docs/concepts/attributes.md` substitution section).
- Move the sixth kind into the inline enumeration at `substitution.go:7-13`.
- Add a substitution-grammar-listing test that pins the count.

## Evidence

- `_discover/2026-05-10-attribute-substitution-grammar.md` Observations bullet 1.
- `modeling/attribute/substitution.go:7-13` (five kinds inline) vs lines 72-90 (sixth in callback comment).

