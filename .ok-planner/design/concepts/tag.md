---
concept: tag
status: as-is
aliases:
  - template-tag
references:
  - _discover/2026-05-10-content-addressed-templates.md
  - _discover/rimsky-cli-compose-prefix-reservation.md
---

# Template tag

## What it is

A tag is a movable string alias pointing at a `template_hash`. Persisted as a tag-name → template-hash mapping record. Tags can be moved by operators (or by the CLI's `compose` flow) without changing template identity.

## Purpose

Templates are immutable (content-addressed). Tags are how operators say "the current production version of this template-shape is X." Moving a tag does not migrate running instances; only future instance creates pick up the new target.

## Boundaries

Owns: name → hash mapping, lifecycle event fan-out (tags arrive on the template-deployed lifecycle event). Does NOT own: the underlying spec (see `concept:template`), instance routing (instances bind to hashes, not tags). Adjacent: `concept:template`, `concept:lifecycle-subscriber`, `concept:rimsky`.

## Invariants

- Tag → hash mapping is mutable; the hash itself is immutable.
- Tag movement does NOT retroactively migrate live instances bound to a different hash.

## Aliases and historical names

`template-tag` is the explicit name in some references; the schema column and operator vocabulary just use `tag`.

## Open within this concept

- `compose:<project>:<...>` prefix reservation is enforced client-side only by the CLI (see `concept:rimsky`); the server accepts any tag string — see `tension:compose-prefix-client-side`.

## Notes

2026-05-25 — Codebase citations removed + cross-refs repaired for self-containment per spec:2026-05-25-concept-doc-self-containment.

