---
concept: template
status: as-is
aliases:
  - canonical-spec
---

# Template

## What it is

A template is the static artifact a consumer registers: node definitions, attribute schemas, claim/lock declarations, subscription and cascade-coupling declarations, message-type schemas, publisher declarations, a params schema, late-bind service names, and sub-graph declarations. Persisted as a template record keyed by a content-hash identifier computed over the canonicalized spec bytes. Templates pass through a small lifecycle from initial registration through deployment, undeployment, and final deregistration.

## Purpose

Content-addressing gives a template stable identity. Two semantically-identical specs (key order, whitespace) produce the same hash; differing specs do not. Idempotent re-registration is a registration-entry-point property: the registration handler resolves the incoming spec's hash first and, when a matching template already exists, returns success without re-inserting rather than surfacing a conflict.

## Boundaries

Owns: the spec bytes, the canonical hash, the lifecycle states, the registration entry point. Does NOT own: deployment routing (see `concept:tag`), per-deployment overrides (see `concept:instance`), runtime state (see `concept:node`). Adjacent: `concept:tag`, `concept:instance`, `concept:lifecycle-subscriber`, and the canonicalization step (a sub-detail of template hashing inside this concept; pinned to a fixed canonicalization-library version).

## Invariants

- The template id is a stable digest-prefix plus the hex-encoded digest, computed over the canonicalized spec bytes.
- The canonicalization-library version pin is a standing commitment, guarded by a mechanical check that fails when the pinned version moves. A bump that changes canonicalization output would silently split template identity — every persisted template keeps its old id while every newly computed hash differs — so the pin moves only as a deliberate act, together with a decision on identity migration (see `decision:jcs-cyberphone`).
- Instances bind to a specific template-hash identity at creation; tag movement does not migrate live instances.
- A template-level late-bind list names services whose registration-time existence and schema validation are bypassed (their actual schema comes from the spawned binary's capabilities handshake at dispatch). The list is part of the canonical spec bytes, so it participates in the canonicalized template hash — changing the list reregisters the template under a new hash, preserving the content-addressing invariant. Names absent from the list are subject to strict registration-time checks. See `concept:host-agent-proxy`.
- Reference and schema validation at registration is **unconditional**: every executor, store, and named-lock reference a template declares by name must exist and validate, and an executor's expected-attributes schema must be visible and satisfied, across the executor / store / lock / schema legs alike. There is no operator setting that relaxes this — a reference that cannot be validated at registration is a hard registration failure, with no register-before-provision path. Executor and store names validate against the shared `concept:service-address-book`, the same source of truth dispatch-time resolution reads.
