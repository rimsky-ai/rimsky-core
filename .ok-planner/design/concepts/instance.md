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

An instance is one live deployment of a template, identified by a rimsky-generated UUID. Created via `POST /instances {template, instance_key?, params, attribute_overrides?}`. Bound to a specific `template_hash`. Carries `params` (a free-form JSON blob substitutable as `{{params.<key>}}`) and optional `attribute_overrides?` (per-instance per-node attribute fragments).

## Purpose

Templates declare the graph shape; instances are the live runtimes. Instances are what frames belong to and what cascade resolves against.

## Boundaries

Owns: the per-deployment runtime state, params, attribute_overrides (including `by_match` matcher overlays and the per-entry match-counter column), the binding to a template hash. Does NOT own: the template spec (see `template`), live node rows (those have their own `instance_id` FK), claim conflict (those scope to the supervisor). Adjacent: `template`, `tag`, `frame`, `node`.

## Invariants

- FK column is `template_hash TEXT`, bound at creation.
- `instance_key` (formerly `consumer_key`) is nullable; canonical identity is the UUID.
- `attribute_overrides` validation inspects only routing keys (`by_executor` / `by_node` plus executor/node names; for `by_match`, matcher key names + cross-checked values for `node_type` / `executor` / `graph`); overlay fragment values are never inspected (preserves structural-inertness for attribute values). Matcher attribute paths (`attrs.<path>`) are shape-validated (primitive equality) but not schema-cross-checked — unused matchers surface via `col:rimsky_instances.attribute_overrides_match_counts`.

## Aliases and historical names

`instance_key` is the current name for the optional dedup hint; the old name `consumer_key` still appears in some early prose. See `tensions/consumer-key-vs-instance-key.md`.

## Open within this concept

- Legacy spelling `consumer_key` survives in code paths and prose — see `tensions/consumer-key-vs-instance-key.md`.

## Notes

2026-05-21 — `userdata_overrides` → `attribute_overrides`. Same merge shape (`by_executor` + `by_node`), applied to attribute values rather than userdata bytes. Persisted as `col:rimsky_instances.attribute_overrides`. See `.ok-planner/specs/2026-05-20-userdata-collapse-into-attributes-design.md`.

2026-05-21 — Matcher overlay (`by_match`) added to `attribute_overrides` per `.ok-planner/specs/2026-05-21-attribute-overrides-matcher-overlay-design.md`. New column `col:rimsky_instances.attribute_overrides_match_counts` (JSONB array of int64, indexed by `by_match` entry position). Incremented synchronously by the supervisor at match time; readable via `GET /instances/{id}`.
