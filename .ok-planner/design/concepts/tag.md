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

A tag is a movable string alias pointing at a `template_hash`. Stored in `rimsky_template_tags` (TEXT tag-name → template_hash). Tags can be moved by operators (or by `rimsky-cli compose`) without changing template identity.

## Purpose

Templates are immutable (content-addressed). Tags are how operators say "the current production version of this template-shape is X." Moving a tag does not migrate running instances; only future instance creates pick up the new target.

## Boundaries

Owns: name → hash mapping, lifecycle event fan-out (tags arrive on `OnTemplateDeployed`). Does NOT own: the underlying spec (see `template`), instance routing (instances bind to hashes, not tags). Adjacent: `template`, `lifecycle-subscriber`, `rimsky-cli`.

## Invariants

- Tag → hash mapping is mutable; the hash itself is immutable.
- Tag movement does NOT retroactively migrate live instances bound to a different hash.

## Aliases and historical names

`template-tag` is the explicit name in some references; the schema column and operator vocabulary just use `tag`.

## Open within this concept

- `compose:<project>:<...>` prefix reservation is enforced client-side only by `rimsky-cli`; the server accepts any tag string — see `tensions/compose-prefix-client-side.md`.

