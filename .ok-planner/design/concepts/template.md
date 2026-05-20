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

A template is the static artifact a consumer registers: node definitions, attribute schemas, claim/lock declarations, frame-resolution policy, handler declarations, quality rules. Stored in `rimsky_templates` keyed by `id = "sha256-<64-hex>"` over the JCS-canonicalized spec bytes. Lifecycle states: `registered | deployed | undeployed | deregistered`.

## Purpose

Content-addressing gives a template stable identity. Two semantically-identical specs (key order, whitespace) produce the same hash; differing specs do not. Idempotent re-registration is a database-layer property (`ON CONFLICT DO NOTHING`).

## Boundaries

Owns: the spec bytes, the canonical hash, the lifecycle states, the registration entry point. Does NOT own: deployment routing (see `tag`), per-deployment overrides (see `instance`, `userdata`), runtime state (see `node`). Adjacent: `tag`, `instance`, `lifecycle-subscriber`, and the JCS canonicalization step (a sub-detail of template hashing inside this concept; pinned via the `graph/template/canonical/jcs.go` library version).

## Invariants

- `rimsky_templates.id` is `sha256-` prefix + 64 hex chars over RFC 8785 JCS bytes.
- The JCS library version is pinned in `go.mod` — a transitive bump that changes canonicalization output invalidates every existing template id (`graph/template/canonical/jcs.go:13-15`).
- Instances bind to a specific `template_hash` at creation; tag movement does not migrate live instances.

## Aliases and historical names

The legacy `template_id` term still appears in some prose; `template_hash` is the current canonical name (`docs/concepts/template.md` uses `vocabulary-lint-ignore: template_id` for one historical reference).

## Open within this concept

- Pre-v1, hash bytes are not pinned across breaking canonicalization changes (`tensions/pre-v1-hash-instability.md`).
- The `compose:`-tag prefix reservation is client-side only — see `tensions/compose-prefix-client-side.md`.

## Notes

- 2026-05-19 — `TemplateSpec` gains optional `Defaults *TemplateDefaults` carrying template-author userdata baselines (`defaults.userdata.by_executor.<name>`). `TemplateNodeDef` gains optional `Tags []string` for operator-facing metadata (with materialization-time `{{params.<key>}}` substitution support). Both extensions are additive; hash semantics unchanged. Per spec 2026-05-19-multi-instance-template-ergonomics-design.

