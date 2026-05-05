---
concept: instance
definition: |
  A running execution of a template, identified by a Rimsky-generated UUID. Instances bind to a specific template content hash at creation. An optional `instance_key` is a caller-supplied dedup key. Tag movement does not migrate live instances.
proto_symbol: (none)
config_field: (none)
api_surface: POST /instances
related: [template, tag, node, frame]
deprecated_terms: [consumer_key]
---

# Instance

## Definition

A running execution of a template, identified by a Rimsky-generated UUID. Instances bind to a specific template content hash at creation. An optional `instance_key` is a caller-supplied dedup key. Tag movement does not migrate live instances.

## Why it exists

A template is the static artifact; an instance is the live thing that runs against it. Multiple instances of the same template can run concurrently — each has its own population of nodes, frames, and attributes. The instance is the unit of "this is the running execution of this workflow."

The optional `instance_key` is an idempotency key. When a caller supplies one, repeated `POST /instances` with the same key returns the existing instance instead of creating a duplicate. Useful when an outer system wants exactly-once instance creation and may retry the create call.

The `params` block is instance-level config: substitutable into selectors and attribute schemas via `{{params.<key>}}`. Params are set at instance creation and immutable for the instance's lifetime.

## How you encounter it

- **Control API**: `POST /instances` to create (`{template, instance_key?, params}`); `GET /instances` to list; `GET /instances/{idOrKey}` to read; `DELETE /instances/{idOrKey}` to terminate; `GET /instances/{idOrKey}/nodes` for the instance's node states.
- **CLI**: `rimsky-cli instance create`, `rimsky-cli instance get`, `rimsky-cli instance delete`.
- **Lifecycle events**: `LifecycleSubscriber.OnInstanceCreated` fires at creation; `OnInstanceTerminated` fires at deletion. Both are RPCed synchronously by the control-api process.

## Consumer-visible guarantees

- Instance identity is stable: the UUID returned at creation is the only identifier; the optional `instance_key` is a dedup tool, not an identifier.
- Once an instance is created against a template hash, that binding is fixed. Moving the tag the template was registered under to a different hash does not migrate the instance.
- Instance creation against a non-deployed template is rejected.

## Common mistakes

- **Rimsky's instance ≠ AWS EC2 instance ≠ class instance ≠ template instance (C++).** A Rimsky instance is a runtime execution of a workflow template; nothing to do with virtual machines, OO instantiation, or compile-time C++ template specialization.
- Treating `instance_key` as the canonical instance ID. The UUID returned at creation is canonical; `instance_key` is an optional dedup hint.
<!-- vocabulary-lint-ignore: consumer_key -->
- Calling the optional dedup key `consumer_key`. The current name is `instance_key`; the rename came from recognizing the key is an instance-level concern, not a consumer-level one.

## See also

- [`template.md`](template.md)
- [`tag.md`](tag.md)
- [`node.md`](node.md)
- [`frame.md`](frame.md)
