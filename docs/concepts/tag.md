---
concept: tag
definition: |
  A movable alias from a string identifier to a template content hash. Tags resolve to template hashes during operations like `instance create`. Hash-shape strings (the `sha256-<64-hex>` form) are rejected as tag identifiers so the `tag-or-hash` resolution stays unambiguous.
proto_symbol: (none)
config_field: (none)
api_surface: POST /tags
related: [template, instance]
deprecated_terms: []
---

# Tag

## Definition

A movable alias from a string identifier to a template content hash. Tags resolve to template hashes during operations like `instance create`. Hash-shape strings (the `sha256-<64-hex>` form) are rejected as tag identifiers so the `tag-or-hash` resolution stays unambiguous.

## Why it exists

Content-addressed templates have a usability problem: the canonical identifier is a 64-character hex hash. Operators and CI systems want stable, human-readable references like `analytics-production` or `compose:project-alpha:items`. Tags solve this. A tag is a movable pointer; you can repoint it to a different hash without touching the running instances that were created against the old hash.

The hash-shape rejection rule is important. If a tag could literally look like a hash, control-api endpoints that accept "tag or hash" would have ambiguous resolution. Forbidding tag identifiers in the `sha256-<64-hex>` shape keeps resolution unambiguous.

## How you encounter it

- **Control API**: `POST /tags` to create; `GET /tags` to list; `PUT /tags/{tag}` to move; `DELETE /tags/{tag}` to delete.
- **CLI**: `rimsky-cli tag create`, `rimsky-cli tag mv`, `rimsky-cli tag list`, `rimsky-cli tag rm`.
- **Resolution sites**: anywhere that accepts a "template" parameter (e.g. `POST /instances`); the parameter is either a tag identifier or a hash, distinguished by shape.

## Consumer-visible guarantees

- Tag movement is atomic: a `PUT /tags/{tag}` either fully succeeds (the tag now points at the new hash) or fully fails (the tag still points at the old hash). There is no in-between state.
- Moving a tag does not migrate live instances: existing instances retain their bound `template_hash`.
- Tags reserved by `rimsky-cli compose` (the `compose:<project>:<...>` namespace) are rejected client-side when manually created. Compose owns project-prefixed tags.

## Common mistakes

- **Rimsky's tag ≠ git tag, HTML tag, container image tag.** A Rimsky tag is a movable alias to a template content hash. The closest analogy is a git branch (movable pointer) or a container image tag (movable label) — but Rimsky tags reject hash-shape values, and the move is atomic per a control-api `PUT`.
- Expecting tag move to roll over running instances. Tag move repoints the alias for *future* references; existing instances are not affected.
- Manually creating a tag with the `compose:<project>:<...>` prefix. The CLI rejects this client-side; that namespace is reserved for `rimsky-cli compose up`.

## See also

- [`template.md`](template.md)
- [`instance.md`](instance.md)
