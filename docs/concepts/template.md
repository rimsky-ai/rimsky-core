---
concept: template
definition: |
  A content-addressed bundle of node definitions, attribute schemas, claim and lock declarations, and frame-resolution config. The id is `sha256-<64-hex>` over an RFC 8785 JCS-canonicalized spec. Re-registering the same spec is a cheap no-op. Templates persist through four lifecycle states: registered, deployed, undeployed, deregistered.
proto_symbol: (none)
config_field: (none)
api_surface: POST /templates
related: [tag, instance, node, frame]
deprecated_terms: [template_id]
---

# Template

## Definition

A content-addressed bundle of node definitions, attribute schemas, claim and lock declarations, and frame-resolution config. The id is `sha256-<64-hex>` over an RFC 8785 JCS-canonicalized spec. Re-registering the same spec is a cheap no-op. Templates persist through four lifecycle states: registered, deployed, undeployed, deregistered.

## Why it exists

Workflows need a static, named artifact that captures structure: which nodes exist, what they depend on, what attributes they accept, what claims they declare, what executor runs each. Rimsky calls that artifact a template.

Content-addressing the template by hash gives several useful properties:

- **Deterministic identity**: two semantically-identical specs produce the same hash; re-registering is idempotent.
- **No mutation surprises**: the spec a template hash points to never changes. If you want different behavior, you produce a new template (with a new hash).
- **Tag movement is explicit**: human-friendly names ("compose:project-alpha:items") are tags that point at hashes, separate from the content-address.

Instances bind to a specific template hash at creation. Tag movement does not migrate live instances — running work continues against the hash it was launched against. The `template_hash` is `sha256-<64-hex>`; the spec body is JCS-canonicalized JSON; re-registering the same spec is a cheap no-op.

## How you encounter it

- **Control API**: `POST /templates` to register; `POST /templates/{id}/deploy` to deploy; `GET /templates`, `GET /templates/{id}` for read; `DELETE /templates/{id}` to deregister.
- **CLI**: `rimsky template register`, `rimsky template deploy`, `rimsky template undeploy`, `rimsky template rm`.
- **Lifecycle**: registered → deployed → undeployed; deregistered is the absent state. Each transition fires a `LifecycleSubscriber.OnTemplate*` event to all subscribed services.

## Consumer-visible guarantees

- Re-registering an identical spec is idempotent on hash. The control API will accept the registration without error and without modifying state.
- A template's content hash is stable: once registered, the template's behavior does not change. To change behavior, you produce a new template (new hash) and either move a tag or create new instances.
- Instance creation against a template that is registered-but-not-deployed is rejected. Templates must be deployed before instances can be created against them.

## Common mistakes

<!-- vocabulary-lint-ignore: template_id -->
- Treating `template_id` as a stable identifier you can use across content changes. The current term is `template_hash`; it changes with every spec change. Use a tag for stable human-friendly references.
- Expecting tag movement to migrate running instances. It doesn't — instances bind to the hash they were created against.
- Pre-v1 reminder: hash bytes are not pinned across breaking changes. Until v1 ships, dev databases may need to be nuked when the canonical-form algorithm or proto vocabulary changes.

## See also

- [`tag.md`](tag.md)
- [`instance.md`](instance.md)
- [`node.md`](node.md)
- [`frame.md`](frame.md)
