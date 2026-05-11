---
concept: instance
status: as-is
aliases: []
references:
  - _discover/2026-05-10-content-addressed-templates.md
  - _discover/2026-05-10-userdata-overrides-by-instance.md
  - _discover/2026-05-10-frame-resolution-model.md
---

# Instance

## What it is

An instance is one live deployment of a template, identified by a rimsky-generated UUID. Created via `POST /instances {template, instance_key?, params, userdata_overrides?}`. Bound to a specific `template_hash`. Carries `params` (a free-form JSON blob substitutable as `{{params.<key>}}`) and optional `userdata_overrides` (per-instance per-node userdata fragments).

## Purpose

Templates declare the graph shape; instances are the live runtimes. Instances are what frames belong to and what cascade resolves against.

## Boundaries

Owns: the per-deployment runtime state, params, userdata_overrides, the binding to a template hash. Does NOT own: the template spec (see `template`), live node rows (those have their own `instance_id` FK), claim conflict (those scope to the supervisor). Adjacent: `template`, `tag`, `frame`, `node`, `userdata-overrides`.

## Invariants

- FK column is `template_hash TEXT`, bound at creation.
- `instance_key` (formerly `consumer_key`) is nullable; canonical identity is the UUID.
- `userdata_overrides` validation inspects only routing keys (`by_executor`/`by_node` plus executor/node names), never fragment values (preserves `@blessed-invariant 11`).

## Aliases and historical names

`instance_key` is the current name for the optional dedup hint; the old name `consumer_key` still appears in some early prose. See `tensions/consumer-key-vs-instance-key.md`.

## Open within this concept

- Legacy spelling `consumer_key` survives in code paths and prose — see `tensions/consumer-key-vs-instance-key.md`.

