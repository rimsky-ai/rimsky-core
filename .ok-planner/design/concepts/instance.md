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
- `instance_key` is nullable; canonical identity is the UUID.
- `attribute_overrides` validation inspects only routing keys (`by_executor` / `by_node` plus executor/node names; for `by_match`, matcher key names + cross-checked values for `node_type` / `executor` / `graph`); overlay fragment values are never inspected (preserves structural-inertness for attribute values). Matcher attribute paths (`attrs.<path>`) are shape-validated (primitive equality) but not schema-cross-checked — unused matchers surface via a per-instance match-counter column on the override record.
- Candidate selection by the supervisor skips paused instances (the candidate query filters out paused rows).
- `service_bindings` is opaque JSON, set at instance creation and consumed by the `concept:host-agent-proxy` at dispatch time to resolve a late-bound service name to a dev-machine binary.
- `created_by_api_key_id` is the api-key whose authenticated request created the instance (nullable for instances created under `concept:anonymous-mode`); it is the routing key the host-agent-proxy uses to find the owner's connected `concept:host-agent`.
- An instance is terminal exactly when its terminal timestamp is set. The force-terminate control action is the production mechanism that sets it, abandoning any in-flight node-runs (transitioning them to failed) and closing the instance's main `concept:run-scope` in the same teardown, so a terminated instance never retains an open main run-scope. Terminal is not removal: the instance key is freed for reuse only by the subsequent row delete, which is permitted only once the instance is terminal.
- An instance is durable by default: it self-terminates only when created with `terminate_after_run = true`, and then only after its next frame ends (strict 'run at most once more' semantics). The default (`terminate_after_run = false`) never self-terminates.
- Termination is independent of `concept:sensor` / `concept:publisher-subscription` and of node presence — the termination decision reads nothing about subscriptions or nodes.
- Instantiation is the mandatory static-config validation gate: `POST /instances` validates each node's statically-knowable attribute config (value constraints included) against every referenced service's schema and rejects create on any static misconfiguration. All referenced services exist at instantiation (the bound-on-demand host-agent proxy is itself a present service), so whatever a relaxed registration mode skipped is enforced here. Substitution-sourced values, knowable only once a node acquires its inputs, stay validated at dispatch (`@blessed-invariant 12`, validate-twice — that pass becomes defense-in-depth for the static part).
