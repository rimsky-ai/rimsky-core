---
tension: pre-v1-hash-instability
category: unspecified
status: open
affects:
  - template
---

# Pre-v1 template hash bytes are not pinned across breaking changes; the post-v1 commitment is unspecified

## What is muddy

The current state: JCS library version is pinned in `go.mod` and the canonical-hash function is the registry's identity function. A transitive minor bump that changed canonicalization output bytes would invalidate every existing template id (annotated at `modeling/template/canonical/jcs.go:13-15`).

But CLAUDE.md and `docs/concepts/template.md` add: "hash bytes are not pinned across breaking changes — dev-DB nuke." This is documented as a pre-v1 stance, but the post-v1 commitment is not specified. Will v1 freeze the JCS library version? The canonical-form algorithm? The proto vocabulary? All three?

## Why it matters

Anyone building tooling that re-derives template hashes (subscribers, third-party diff tools) needs to know what to bind to. Post-v1 readers will hit a wall: the rule says "pinned" but historical practice says "rebuild on breaking changes."

## Resolution candidates (do NOT pick)

- v1 commits to: JCS library version + canonical-form algorithm + spec-shape => hash bytes pinned indefinitely.
- v1 commits to a defined migration path for hash changes (e.g., dual-publish under both hashes).
- v1 introduces a hash-version prefix (`sha256-v1-<hex>`) for future flexibility.

## Evidence

- `_discover/2026-05-10-content-addressed-templates.md` Observations.
- `_discover/jcs-canonicalization-pinning.md` Observations.
- `modeling/template/canonical/jcs.go:13-15`.

