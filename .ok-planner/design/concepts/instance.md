---
concept: instance
status: as-is
aliases: []
---

# Instance

## What it is

An instance is one live deployment of a template, identified by a rimsky-generated UUID. Created via the instance-create control endpoint, carrying a template binding plus initial params and optional attribute overrides. Bound to a specific template hash. Carries a free-form params blob (substitutable into node configurations) and optional per-instance per-node attribute fragments.

## Purpose

Templates declare the graph shape; instances are the live runtimes. Instances are what frames belong to and what cascade resolves against. Instance creation creates the per-instance row and the per-instance node rows and triggers the instance-created lifecycle callback; no frame is enqueued and no work begins until a sender posts a message. The empty-message trigger (`story:empty-message-wakes-roots`) is the universal convenience for waking every structural root without crafting a typed envelope.

## Boundaries

Owns: the per-deployment runtime state, params, attribute overrides (including match-based overlays and per-entry match counters), the per-instance late-bound service-binding catalog (set at creation), the creator's api-key linkage (see `concept:api-key`), paused state, the binding to a template hash. Does NOT own: the template spec (see `template`), live node rows (those carry their own instance reference), claim conflict (that scopes to the supervisor). Adjacent: `template`, `tag`, `frame`, `node`, `api-key`, `host-agent-proxy`.

## Invariants

- The template binding is a foreign key to the template hash, fixed at creation.
- `instance_key` is nullable; canonical identity is the UUID.
- Attribute-overrides validation inspects only routing keys (the per-executor / per-node selectors and, for match-based overlays, the matcher key names plus cross-checked discriminators); overlay fragment values are never inspected (preserves structural-inertness for attribute values). Matcher attribute paths are shape-validated (primitive equality) but not schema-cross-checked — unused matchers surface via a per-instance match counter on the override record.
- Candidate selection by the supervisor skips paused instances (the candidate query filters out paused rows).
- The late-bound service-binding catalog is opaque, set at instance creation and consumed by the `concept:host-agent-proxy` at dispatch time to resolve a late-bound service name to a dev-machine binary.
- The creator's api-key linkage records the api-key whose authenticated request created the instance (absent for instances created under `concept:anonymous-mode`); it is the routing key the host-agent-proxy uses to find the owner's connected `concept:host-agent`.
- An instance is terminal exactly when its terminal timestamp is set. The force-terminate control action is the production mechanism that sets it, abandoning any in-flight node-runs across the currently-running frame (if any) and transitioning them to failed, then closing that frame and the `concept:run-scope` tree it owns; any queued-but-not-yet-running frames are dropped without execution. Terminal is not removal: the instance key is freed for reuse only by the subsequent row delete, which is permitted only once the instance is terminal.
- An instance is durable by default: it self-terminates only when created with the terminate-after-run flag set, and then only after its next frame ends (strict "run at most once more" semantics). The default never self-terminates.
- Termination is independent of `concept:sensor` / `concept:publisher-subscription` and of node presence — the termination decision reads nothing about subscriptions or nodes.
- Instantiation is the mandatory static-config validation gate: the instance-create endpoint validates each node's statically-knowable attribute config (value constraints included) against every referenced service's schema and rejects create on any static misconfiguration. All referenced services exist at instantiation (the bound-on-demand host-agent proxy is itself a present service), so whatever a relaxed registration mode skipped is enforced here. Substitution-sourced values, knowable only once a node acquires its inputs, stay validated at dispatch (invariant 12, validate-twice — that pass serves as defense-in-depth for the static part).
