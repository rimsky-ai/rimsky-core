---
topic: jcs-canonicalization-pinning
kind: discipline
---

# JCS (RFC 8785) canonicalizes template specs to byte-stable input for SHA-256 hashing; library version is load-bearing

## Description

Template content-addressing (`2026-05-10-content-addressed-templates`) requires byte-stable input for the SHA-256 hash. Two semantically-identical specs (same shape, irrelevant whitespace or key-order differences) must produce the same hash; differing specs must not. This is what JSON Canonicalization Scheme (JCS, RFC 8785) provides.

`modeling/template/canonical/jcs.go` implements `CanonicalSpecHash` (lines 46-56), which:

1. Marshals the spec to canonical JSON bytes via `github.com/cyberphone/json-canonicalization`.
2. Computes SHA-256 over the bytes.
3. Returns `"sha256-<64-hex>"` as the template id.

The annotation at `jcs.go:13-15`:

> The canonical-hash function is the registry's identity function. Any change that alters output bytes for previously-registered specs is a breaking change. The JCS library version is pinned in go.mod.

This is `@blessed-invariant`-shaped though it's not numbered. The pin in `go.mod` is the actual lock; a transitive minor bump that changed canonicalization output bytes (different number-formatting, different string-escape choice, different key sort) would invalidate every existing template hash. The hash IS the identity, so identity change = registry data loss.

JCS guarantees byte-identical output across implementations regardless of map ordering, whitespace, and non-essential string escape variations. The comment at `jcs.go:8-12` justifies the JCS choice (vs a hand-rolled canonicalizer): "JCS guarantees byte-identical output across implementations regardless of map ordering / whitespace / non-essential string escape variations."

The spec body is passed through the canonicalization pipeline at template-registration time (`modeling/controlapi/templates.go`); rimsky stores the canonical bytes verbatim in the registry, and the LifecycleSubscriber `OnTemplateRegistered.spec` fires with those same canonical bytes. Subscribers can re-derive the hash deterministically.

`docs/concepts/template.md` notes the pre-v1 caveat: "hash bytes are not pinned across breaking changes. Until v1 ships, dev databases may need to be nuked when the canonical-form algorithm or proto vocabulary changes." This is `2026-05-10-pre-v1-break-freely-migrations` applied to the hash; once v1 ships, the canonical-form algorithm becomes frozen.

The split between `modeling/template/canonical/` (the hashing) and `foundation/persistence/templates.go` (the storage) is deliberate. A third-party tool that wants to re-derive the hash without pulling in persistence can import only `modeling/template/canonical/`. The Apache license on this subpackage (per `licensing-boundary-map`) supports this — external tools can compute template hashes without AGPL.

## Code surface

- `modeling/template/canonical/jcs.go` — entire file (`CanonicalSpecHash`).
- `modeling/template/canonical/jcs_test.go` — round-trip tests.
- `foundation/persistence/postgres/migrations/003-template-registry-and-lifecycle.sql:49-65` — `rimsky_templates.id` schema.
- `modeling/controlapi/templates.go` — call site at registration.
- `protocols/proto/v1/lifecycle.proto:35` — `OnTemplateRegistered.spec` field.
- `go.mod` — JCS library version pin.

## Prose surface

- `docs/concepts/template.md` — content-addressing description.
- `CLAUDE.md` "Non-obvious gotchas" — "Templates are content-addressed... Pre-v1: hash bytes are not pinned across breaking changes."
- `.ok-planner/specs/2026-05-04-modeling-layer-contract.md` — template registry contract.

## Adjacent topics

- `2026-05-10-content-addressed-templates` — uses this for the hash.
- `2026-05-10-pre-v1-break-freely-migrations` — hash instability pre-v1.
- `2026-05-10-lifecycle-subscriber-opt-in` — `OnTemplateRegistered.spec` carries canonical bytes.
- `licensing-boundary-map` — `modeling/template/canonical/` is Apache.

## Observations

- JCS is RFC 8785; the library `github.com/cyberphone/json-canonicalization` is one Go impl. Other Go impls exist; the choice of this particular library is unstated beyond "we pinned it." A future migration to a different JCS impl would have to produce byte-identical output to be safe.
- The canonical bytes are NOT compressed; large templates produce large canonical bytes. The SHA-256 is over the uncompressed form. This is irrelevant for hashing but matters for the `OnTemplateRegistered.spec` payload size (which is the canonical bytes).
- The pin in `go.mod` is `@blessed-invariant`-class but is not numbered. CLAUDE.md "Blessed invariants" list doesn't include it; the annotation lives in `jcs.go:13-15` as a per-file `@blessed-invariant` without a number.
- A pre-v1 breaking change to the canonicalization (e.g. switching to a different RFC, normalizing escape sequences differently) would require: bump the library; record the breaking change in CHANGELOG; nuke the dev DB. Post-v1, this becomes a much harder migration.
