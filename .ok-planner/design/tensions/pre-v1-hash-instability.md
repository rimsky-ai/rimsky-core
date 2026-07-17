---
tension: pre-v1-hash-instability
category: unspecified
status: open
affects:
  - template
---

# Pre-v1 template hash bytes are not pinned across breaking changes; the post-v1 commitment is unspecified

## What is muddy

The current state: JCS library version is pinned in `go.mod` and the canonical-hash function is the registry's identity function. A transitive minor bump that changed canonicalization output bytes would invalidate every existing template id (annotated at the canonicalization package now living under the graph module's template layer).

The repo-wide pre-v1 stance ("no backwards-compat guarantee on the wire protocol, config shape, or persisted identifiers until v1 ships — a breaking change may require nuking a dev database") now lives in the project's general pre-v1 rules rather than a template-specific CLAUDE.md note, and there is no in-tree `docs/concepts/template.md` (public docs are out-of-tree). The durable concept doc states only that the canonicalization-library version is pinned today; it does not state a post-v1 commitment. Will v1 freeze the JCS library version? The canonical-form algorithm? The proto vocabulary? All three?

## Why it matters

Anyone building tooling that re-derives template hashes (subscribers, third-party diff tools) needs to know what to bind to. Post-v1 readers will hit a wall: the rule says "pinned" but historical practice says "rebuild on breaking changes."

## Resolution candidates (do NOT pick)

- State in `concept:template` the post-v1 commitment for hash byte stability: whether the canonical-hash function is pinned for the indefinite life of v1, whether the template id carries a version prefix that lets the canonical form evolve, or whether breaking hash changes ride a defined migration path.

## Evidence

- `concept:template` documents only the pre-v1 pinning stance (canonicalization-library version pinned today, so a transitive bump changes every template id); it states no post-v1 commitment on hash-byte stability.

