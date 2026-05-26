---
concept: template
status: as-is
aliases:
  - canonical-spec
references:
  - _discover/2026-05-10-content-addressed-templates.md
  - _discover/jcs-canonicalization-pinning.md
  - _discover/2026-05-10-lifecycle-subscriber-opt-in.md
---

# Template

## What it is

A template is the static artifact a consumer registers: node definitions, attribute schemas, claim/lock declarations, frame-resolution policy, handler declarations, quality rules. Persisted as a template record keyed by `id = "sha256-<64-hex>"` computed over the JCS-canonicalized spec bytes. Lifecycle states: `registered | deployed | undeployed | deregistered`.

## Purpose

Content-addressing gives a template stable identity. Two semantically-identical specs (key order, whitespace) produce the same hash; differing specs do not. Idempotent re-registration is a persistence-layer property: re-registering an identical spec is a no-op rather than a conflict.

## Boundaries

Owns: the spec bytes, the canonical hash, the lifecycle states, the registration entry point. Does NOT own: deployment routing (see `concept:tag`), per-deployment overrides (see `concept:instance`), runtime state (see `concept:node`). Adjacent: `concept:tag`, `concept:instance`, `concept:lifecycle-subscriber`, and the JCS canonicalization step (a sub-detail of template hashing inside this concept; pinned to a fixed canonicalization-library version).

## Invariants

- The template id is a `sha256-` prefix + 64 hex chars over RFC 8785 JCS bytes.
- The JCS canonicalization-library version is pinned — a transitive bump that changes canonicalization output invalidates every existing template id.
- Instances bind to a specific `template_hash` at creation; tag movement does not migrate live instances.
- A top-level `late_bind_services` list names services whose registration-time existence and schema validation are bypassed (their actual schema comes from the spawned binary's Capabilities handshake at dispatch). The list is stored inside the canonical spec bytes, so it participates in the JCS-canonicalized template hash — changing the list reregisters the template under a new hash, preserving the content-addressing invariant. Names absent from the list retain today's strict registration-time checks. See `concept:host-agent-proxy`.

## Aliases and historical names

The legacy `template_id` term still appears in some prose; `template_hash` is the current canonical name.

## Open within this concept

- Pre-v1, hash bytes are not pinned across breaking canonicalization changes (`tension:pre-v1-hash-instability`).
- The `compose:`-tag prefix reservation is client-side only — see `tension:compose-prefix-client-side`.

## Notes

- 2026-05-19 — The template spec gains an optional defaults block carrying template-author userdata baselines (per-executor defaults). The per-node template definition gains an optional tags list for operator-facing metadata (with materialization-time `{{params.<key>}}` substitution support). Both extensions are additive; hash semantics unchanged. Per spec:2026-05-19-multi-instance-template-ergonomics.
- 2026-05-25 — Codebase citations removed + cross-refs repaired for self-containment per spec:2026-05-25-concept-doc-self-containment.
- [2026-05-24] Adds the top-level `late_bind_services` field (stored in the canonical spec bytes, so it participates in the content-addressing hash). Names in the list bypass registration-time existence + schema validation; their schemas come from the spawned binary's Capabilities at dispatch. Per spec 2026-05-24-host-agent-and-proxy-design.

