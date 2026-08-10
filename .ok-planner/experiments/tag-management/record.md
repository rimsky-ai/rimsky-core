---
experiment: tag-management
commit: PENDING
---

# Movable template-hash names through the CLI

## What it ran against

A `rimsky-all-in-one` container booted from this tree's image. Two templates
that differ only in one check bound, so they hash differently, are registered
and deployed; the probe then drives the `rimsky tag` verbs and creates
instances through the tag. `run.sh` boots and removes the container.

## What was observed

`tag create` bound the name `pipeline` to the first hash; `tag list` carried
the name and the hash it points at, and `tag get` resolved it. An instance
created with the tag as its template ref bound to the tagged hash. `tag mv`
re-pointed the name at the second hash: `tag get` then resolved to the new
hash and no longer to the old one, and a newly created instance bound to the
new hash. The instance created before the move still reported the hash it was
created from and carried no `terminated_at` — the move did not disturb it.
`tag mv` back to the first hash resolved again to the first hash. `tag rm`
removed the name from the tag list and the name no longer resolved as a
template ref, while the instances created under it kept running.
