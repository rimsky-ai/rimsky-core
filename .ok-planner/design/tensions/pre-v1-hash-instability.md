---
tension: pre-v1-hash-instability
category: unspecified
status: open
affects:
  - template
---

# Pre-v1 template hash bytes are not pinned across breaking changes; the post-v1 commitment is unspecified

## What is muddy

The current state: JCS library version is pinned in `go.mod` and the canonical-hash function is the registry's identity function. A transitive minor bump that changed canonicalization output bytes would invalidate every existing template id (annotated at `graph/template/canonical/jcs.go`).

But CLAUDE.md and `docs/concepts/template.md` add: "hash bytes are not pinned across breaking changes — dev-DB nuke." This is documented as a pre-v1 stance, but the post-v1 commitment is not specified. Will v1 freeze the JCS library version? The canonical-form algorithm? The proto vocabulary? All three?

## Why it matters

Anyone building tooling that re-derives template hashes (subscribers, third-party diff tools) needs to know what to bind to. Post-v1 readers will hit a wall: the rule says "pinned" but historical practice says "rebuild on breaking changes."

## Resolution candidates (do NOT pick)

- State in `concept:template` the post-v1 commitment for hash byte stability: whether the canonical-hash function is pinned for the indefinite life of v1, whether the template id carries a version prefix that lets the canonical form evolve, or whether breaking hash changes ride a defined migration path.

## Evidence

- `_discover/2026-05-10-content-addressed-templates.md` Observations.
- `_discover/jcs-canonicalization-pinning.md` Observations.
- `graph/template/canonical/jcs.go`.

