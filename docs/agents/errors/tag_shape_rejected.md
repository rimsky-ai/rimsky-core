---
error: tag_shape_rejected
surfaced_to: cli-user
---

# Tag shape rejected

## What it means

A tag-create or tag-move request supplied a tag identifier that matches the `sha256-<64-hex>` content-hash shape. Rimsky rejects this so the `tag-or-hash` resolution stays unambiguous.

## When it happens

`POST /tags` or `PUT /tags/{tag}` (or the equivalent `rimsky tag` subcommand) with an identifier that looks like a content hash.

## What to do

Pick a different tag identifier — typically a kebab-case human-readable string. Reserve hash-shape strings for actual content hashes returned at template registration.

## See also

- [`../../concepts/tag.md`](../../concepts/tag.md)
- [`../../concepts/template.md`](../../concepts/template.md)
