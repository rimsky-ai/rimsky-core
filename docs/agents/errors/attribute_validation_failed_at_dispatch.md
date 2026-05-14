---
error: attribute_validation_failed_at_dispatch
surfaced_to: cli-user
---

# Attribute validation failed at dispatch

## What it means

After resolving `{{...}}` substitution directives, the resulting input attributes did not validate against the node's `attributes:` schema. The dispatch was rejected; the node did not run.

## When it happens

Most commonly when an upstream node's committed attributes don't match the consuming node's expected types — a substitution returns a value whose JSON shape doesn't fit the schema. Also: a `{{params.<key>}}` reference resolved to an unexpected type, or a missing-but-required field.

## What to do

Check the executor trace and the named-source values (`{{nodes.<source>.attribute.<field>}}` resolutions, `{{params.<key>}}` resolutions) against the node's `attributes:` schema. The error message will name the failing JSON-Schema path. Either correct the upstream value, adjust the schema, or fix the substitution-source declaration.

## See also

- [`../../concepts/attributes.md`](../../concepts/attributes.md)
- [`../../concepts/node.md`](../../concepts/node.md)
