---
error: compose_prefix_violation
surfaced_to: cli-user
---

# Compose prefix violation

## What it means

A caller other than `rimsky compose up` attempted to register a tag or instance key under the reserved `compose:<project>:` prefix. The CLI rejects this client-side.

## When it happens

`rimsky tag create compose:project-alpha:foo`, `rimsky instance create --instance-key compose:project-alpha:items`, or any direct control-api call that sets one of these fields to a `compose:` prefixed value.

## What to do

If you intend the resource to be compose-managed, use `rimsky compose up` against a `rimsky-compose.yml` manifest. If you intend the resource to be hand-managed, pick a different tag identifier or instance key without the `compose:` prefix.

## See also

- [`../../concepts/tag.md`](../../concepts/tag.md)
- [`../../concepts/instance.md`](../../concepts/instance.md)
