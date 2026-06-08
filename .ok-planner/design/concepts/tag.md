---
concept: tag
status: as-is
aliases:
  - template-tag
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
- The `compose:<project>:<...>` tag prefix is reserved and **server-enforced**: tag-create rejects a `compose:`-prefixed name unless the request originates from the privileged compose path. Enforcement is at the source of truth, not a CLI courtesy.

## Aliases and historical names

`template-tag` is the explicit name in some references; the schema column and operator vocabulary just use `tag`.

## Notes

2026-05-25 — Codebase citations removed + cross-refs repaired for self-containment per spec:2026-05-25-concept-doc-self-containment.

2026-06-07 — compose-prefix reservation moved from client-side convention to server-enforced invariant per spec:2026-06-06-comprehensive-gap-closure.

