---
concept: tag
aliases:
  - template-tag
---

# Template tag

## What it is

A tag is a movable name that points at one template's content hash. Rimsky persists it as a mapping from the tag name to that hash. An operator moves a tag by repointing the mapping, and that changes no template's identity.

## Purpose

A tag lets an operator refer to a template by a name that outlives any one version of it. A template's identity is its content, so nothing can update a template in place; a tag is how an operator says which template is the current one for a given shape of work. The mapping is mutable and the hash it points at is not. Moving a tag never migrates a running instance: an instance stays bound to the hash it was created against, and only later instance creates resolve the tag to its new target.

## Boundaries

A tag owns the mapping from name to hash and nothing else. The spec behind the hash belongs to `concept:template`, and so does the template-deployed lifecycle event; tag names ride that event's payload, but the event itself belongs to the template. Instance routing is out, because an instance binds to a hash rather than to a tag. A tag is distinct from `concept:terminal-tag`, an executor-emitted per-verdict discriminator that shares only the word.

See also `concept:template`, `concept:lifecycle-subscriber`, `concept:rimsky`.

## Aliases

`template-tag`.
