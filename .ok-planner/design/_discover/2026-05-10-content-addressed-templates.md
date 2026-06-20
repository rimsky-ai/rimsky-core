---
topic: content-addressed-templates
kind: schema
---

# Templates are content-addressed via JCS-canonicalized SHA-256; tags are movable aliases

## Description

A template is the static artifact a consumer registers with rimsky: node definitions, attribute schemas, claim/lock declarations, frame-resolution config (`docs/concepts/template.md`). Templates need a stable identity such that re-registering the same spec is idempotent and content changes produce a new identity. Rimsky chose content-addressing.

`rimsky_templates.id` is `TEXT PRIMARY KEY` in the form `"sha256-<64-hex>"` (`foundation/persistence/postgres/migrations/003-template-registry-and-lifecycle.sql:49`). The hash is computed as `SHA-256` over the RFC 8785 JSON Canonicalization Scheme (JCS) bytes of the spec (`modeling/template/canonical/jcs.go:46-56`). JCS produces byte-identical output across implementations regardless of map ordering, whitespace, or non-essential string escape variation — so two semantically-identical specs produce the same hash.

Tags are separate. `rimsky_template_tags` (`migrations/003:57-63`) is a `TEXT` tag-name → `template_hash` mapping; tags are mutable and movable while hashes are immutable. Operators move a tag (`compose:project-alpha:items` → `sha256-...new...`) when they want a different version of a template to be the "current" deploy target without re-registering or changing identity. Lifecycle subscribers (`OnTemplateDeployed`) receive both the hash and the tag.

`docs/concepts/template.md` explicitly says: "Re-registering an identical spec is idempotent at the database layer." The control-api `POST /templates` path implements this by computing the hash on the incoming spec and using `ON CONFLICT (id) DO NOTHING` on the INSERT.

Instances bind to a specific `template_hash` at creation (`rimsky_instances.template_hash`, migration 003). Tag movement does not migrate live instances — running work continues against the hash it was launched against. CLAUDE.md "Templates are content-addressed" calls this out and adds a pre-v1 caveat: "hash bytes are not pinned across breaking changes — dev-DB nuke."

The JCS library version (`github.com/cyberphone/json-canonicalization`) is pinned in `go.mod`. The annotation at `jcs.go:13-15` is explicit: "the canonical-hash function is the registry's identity function. Any change that alters output bytes for previously-registered specs is a breaking change. The JCS library version is pinned in go.mod." A transitive minor bump that changed canonicalization output bytes would invalidate every existing template id; this is therefore a `invariant`-style discipline annotation in the modeling layer.

The four template lifecycle states (`registered`, `deployed`, `undeployed`, deregistered) are tracked in `rimsky_templates.state` plus per-state transition rows; each transition fires a `LifecycleSubscriber.OnTemplate*` event (`docs/concepts/template.md` "Lifecycle" section).

## Code surface

- `modeling/template/canonical/jcs.go` — `CanonicalSpecHash` (lines 46-56) and pinning annotation (lines 13-15).
- `foundation/persistence/postgres/migrations/003-template-registry-and-lifecycle.sql:5-65` — schema + `pre-v1` drop+recreate prologue.
- `modeling/controlapi/templates.go` — control-api handlers; idempotency on INSERT.
- `protocols/proto/v1/lifecycle.proto:35` — `OnTemplateRegistered.spec` carries the canonical bytes.
- `foundation/persistence/templates.go` — Go-side CRUD.

## Prose surface

- `docs/concepts/template.md` — concept-doc treatment; content-addressing rationale.
- `docs/concepts/tag.md` — tag semantics (movable aliases).
- `docs/concepts/instance.md` — instances bind to template_hash, not tag.
- `CLAUDE.md` "Non-obvious gotchas" — "Templates are content-addressed."
- `CLAUDE.md` "Compose owns project-prefixed names" — tag-prefix discipline.

## Adjacent topics

- `2026-05-10-pre-v1-break-freely-migrations` — pre-v1 hash bytes are not pinned across breaking changes.
- `2026-05-10-lifecycle-subscriber-opt-in` — `OnTemplateRegistered/Deployed/...` carry the hash.
- `2026-05-10-rimsky-cli-thin-client` — `compose:` prefix reserved for CLI manifest workflow.
- `rimsky-cli-compose-prefix-reservation` — operator-facing tag-naming discipline.

## Observations

- The control-api uses bare paths (no `/v1/` prefix per `2026-05-10-rimsky-cli-thin-client`); this means a template registered against rimsky version X is implicitly "v1-shaped" without any wire-format version pin. The pre-v1 hash-instability acknowledges that breaking proto/spec changes will rebuild the registry.
- The legacy `template_id` term still appears in some prose (`docs/concepts/template.md` uses `<!-- vocabulary-lint-ignore: template_id -->` to suppress a lint), confirming a rename from the legacy name. CLAUDE.md cites `template_hash` consistently.
- `rimsky_template_tags` carries both `compose:`-prefixed tags (reserved for the CLI manifest workflow) and free-form operator-issued tags. The reservation is client-side (CLI rejects manual registration with the prefix); the server side accepts any tag.
- `modeling/template/canonical/jcs.go` lives in the canonical-spec subpackage by itself — separate from `foundation/persistence/templates.go` (the storage side) so a third-party tool can re-derive the hash without pulling in persistence. This mirrors the protocols/foundation split.
