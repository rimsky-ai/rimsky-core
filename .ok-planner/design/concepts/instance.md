---
concept: instance
status: as-is
aliases: []
---

# Instance

## What it is

An instance is one live deployment of a template, identified by a rimsky-generated UUID. Created via the instance-create control endpoint with `{template, instance_key?, params, attribute_overrides?}`. Bound to a specific template hash. Carries `params` (a free-form JSON blob substitutable as `{{params.<key>}}`) and optional `attribute_overrides?` (per-instance per-node attribute fragments).

## Purpose

Templates declare the graph shape; instances are the live runtimes. Instances are what frames belong to and what cascade resolves against.

## Boundaries

Owns: the per-deployment runtime state, params, attribute_overrides (including `by_match` matcher overlays and the per-entry match-counter column), `service_bindings` (the per-instance late-bound service catalog, set at creation), `created_by_api_key_id` (the creating api-key, see `concept:api-key`), the paused state column, the binding to a template hash. Does NOT own: the template spec (see `template`), live node rows (those have their own `instance_id` FK), claim conflict (those scope to the supervisor). Adjacent: `template`, `tag`, `frame`, `node`, `api-key`, `host-agent-proxy`.

## Invariants

- The template binding is a foreign key to the template hash, fixed at creation.
- `instance_key` (formerly `consumer_key`) is nullable; canonical identity is the UUID.
- `attribute_overrides` validation inspects only routing keys (`by_executor` / `by_node` plus executor/node names; for `by_match`, matcher key names + cross-checked values for `node_type` / `executor` / `graph`); overlay fragment values are never inspected (preserves structural-inertness for attribute values). Matcher attribute paths (`attrs.<path>`) are shape-validated (primitive equality) but not schema-cross-checked — unused matchers surface via a per-instance match-counter column on the override record.
- Candidate selection by the supervisor skips paused instances (the candidate query filters out paused rows).
- `service_bindings` is opaque JSON, set at instance creation and consumed by the `concept:host-agent-proxy` at dispatch time to resolve a late-bound service name to a dev-machine binary.
- `created_by_api_key_id` is the api-key whose authenticated request created the instance (nullable for instances created under `concept:anonymous-mode`); it is the routing key the host-agent-proxy uses to find the owner's connected `concept:host-agent`.
- An instance is terminal exactly when its terminal timestamp is set. The force-terminate control action is the production mechanism that sets it, abandoning any in-flight node-runs (transitioning them to failed) and closing the instance's main `concept:run-scope` in the same teardown, so a terminated instance never retains an open main run-scope. Terminal is not removal: the instance key is freed for reuse only by the subsequent row delete, which is permitted only once the instance is terminal.
- An instance is durable by default: it self-terminates only when created with `terminate_after_run = true`, and then only after its next frame ends (strict 'run at most once more' semantics). The default (`terminate_after_run = false`) never self-terminates.
- Termination is independent of `concept:sensor` / `concept:publisher-subscription` and of node presence — the termination decision reads nothing about subscriptions or nodes.

## Aliases and historical names

`instance_key` is the current name for the optional dedup hint; the old name `consumer_key` still appears in some early prose. See `tension:_resolved/consumer-key-vs-instance-key`.

## Resolved within this concept

- The legacy `consumer_key` spelling vs the canonical `instance_key` was resolved — see `tension:_resolved/consumer-key-vs-instance-key`.

## Notes

2026-05-21 — `userdata_overrides` → `attribute_overrides`. Same merge shape (`by_executor` + `by_node`), applied to attribute values rather than userdata bytes. Persisted on the instance row. See `spec:2026-05-20-userdata-collapse-into-attributes`.

2026-05-21 — Matcher overlay (`by_match`) added to `attribute_overrides` per `spec:2026-05-21-attribute-overrides-matcher-overlay`. A new per-instance match-counter column (a JSON array of integers, indexed by `by_match` entry position) is incremented synchronously by the supervisor at match time and is readable via the per-instance fetch endpoint.

2026-05-24 — Adds a per-instance paused flag column and the corresponding pause / resume / paused-on-create surface per `spec:2026-05-24-instance-debugger`. Soft-pause semantics: in-flight dispatches run to terminal; new claims are held until resume.

- 2026-05-25 — Codebase citations removed + cross-refs repaired for self-containment per spec:2026-05-25-concept-doc-self-containment.
- [2026-05-24] Adds `service_bindings` (opaque late-bound service catalog) and `created_by_api_key_id` (the creating api-key, nullable under `concept:anonymous-mode`) to the instance row, consumed by the `concept:host-agent-proxy` for late-bound dispatch resolution and agent routing. Per spec 2026-05-24-host-agent-and-proxy-design.
- 2026-05-28 — termination invariant added per spec:2026-05-28-quality-of-life-features; force-terminate is the first production path to mark an instance terminal, distinct from the row-delete reaper that frees the instance key.
- 2026-05-28 — force-terminate teardown refined per spec:2026-05-28-quality-of-life-features: the action closes the instance's main concept:run-scope in the same teardown that marks it terminal and abandons in-flight node-runs, so a terminated instance never retains an open main run-scope. This does not depend on the background terminator sweep, which only reaches terminated instances that still carry concept:lifecycle-subscriber bookkeeping.
- 2026-06-03 — Durable-by-default lifecycle + opt-in terminate_after_run (strict: terminate after the next frame ends, regardless of queued frames). Replaces the longstanding unconditional auto-terminate-on-drain and removes the publisher-subscription coupling. Richer termination modes (drain-to-quiet, count-based) are deliberately deferred behind the same flag or a renamed successor. Per spec:2026-06-03-instance-lifecycle-durable-by-default.
