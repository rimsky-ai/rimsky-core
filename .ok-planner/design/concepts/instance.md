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

An instance is one live deployment of a template, identified by a rimsky-generated UUID. Created via the instance-create control endpoint with `{template, instance_key?, params, attribute_overrides?}`. Bound to a specific template hash. Carries `params` (a free-form JSON blob substitutable as `{{params.<key>}}`) and optional `attribute_overrides?` (per-instance per-node attribute fragments).

## Purpose

Templates declare the graph shape; instances are the live runtimes. Instances are what frames belong to and what cascade resolves against.

## Boundaries

Owns: the per-deployment runtime state, params, attribute_overrides (including `by_match` matcher overlays and the per-entry match-counter column), the paused state column, the binding to a template hash. Does NOT own: the template spec (see `template`), live node rows (those have their own `instance_id` FK), claim conflict (those scope to the supervisor). Adjacent: `template`, `tag`, `frame`, `node`.

## Invariants

- The template binding is a foreign key to the template hash, fixed at creation.
- `instance_key` (formerly `consumer_key`) is nullable; canonical identity is the UUID.
- `attribute_overrides` validation inspects only routing keys (`by_executor` / `by_node` plus executor/node names; for `by_match`, matcher key names + cross-checked values for `node_type` / `executor` / `graph`); overlay fragment values are never inspected (preserves structural-inertness for attribute values). Matcher attribute paths (`attrs.<path>`) are shape-validated (primitive equality) but not schema-cross-checked — unused matchers surface via a per-instance match-counter column on the override record.
- Candidate selection by the supervisor skips paused instances (the candidate query filters out paused rows).

## Aliases and historical names

`instance_key` is the current name for the optional dedup hint; the old name `consumer_key` still appears in some early prose. See `tension:_resolved/consumer-key-vs-instance-key`.

## Resolved within this concept

- The legacy `consumer_key` spelling vs the canonical `instance_key` was resolved — see `tension:_resolved/consumer-key-vs-instance-key`.

## Notes

2026-05-21 — `userdata_overrides` → `attribute_overrides`. Same merge shape (`by_executor` + `by_node`), applied to attribute values rather than userdata bytes. Persisted on the instance row. See `spec:2026-05-20-userdata-collapse-into-attributes`.

2026-05-21 — Matcher overlay (`by_match`) added to `attribute_overrides` per `spec:2026-05-21-attribute-overrides-matcher-overlay`. A new per-instance match-counter column (a JSON array of integers, indexed by `by_match` entry position) is incremented synchronously by the supervisor at match time and is readable via the per-instance fetch endpoint.

2026-05-24 — Adds a per-instance paused flag column and the corresponding pause / resume / paused-on-create surface per `spec:2026-05-24-instance-debugger`. Soft-pause semantics: in-flight dispatches run to terminal; new claims are held until resume.

- 2026-05-25 — Codebase citations removed + cross-refs repaired for self-containment per spec:2026-05-25-concept-doc-self-containment.
