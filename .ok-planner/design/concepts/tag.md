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

Owns: name → hash mapping. Does NOT own: the underlying spec (see `concept:template`), instance routing (instances bind to hashes, not tags), the template-deployed lifecycle event and its fan-out (tags ride that event's payload; the event itself belongs to `concept:template`). Adjacent: `concept:template`, `concept:lifecycle-subscriber`, `concept:rimsky`. Distinct from `concept:terminal-tag`, an unrelated executor-emitted per-verdict discriminator that shares only the word.

## Invariants

- Tag → hash mapping is mutable; the hash itself is immutable.
- Tag movement does NOT retroactively migrate live instances bound to a different hash.
- The `compose:<project>:<...>` tag prefix is reserved and **server-enforced**: tag-create rejects a `compose:`-prefixed name unless the request originates from the privileged compose path.
