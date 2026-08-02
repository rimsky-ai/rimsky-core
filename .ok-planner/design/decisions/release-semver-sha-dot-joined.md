---
decision: release-semver-sha-dot-joined
---

# Pre-release SHA encoding

## Choice

The commit SHA in a pre-release version is dot-joined into the SemVer pre-release segment, not appended as build metadata.

## Rationale

The SemVer build-metadata separator is rejected by the downstream tag and module grammars rimsky distributes through, so the SHA has to ride the pre-release segment instead.

## Alternatives

- SHA as SemVer build metadata (the `+` form SemVer designed for exactly this) — rejected: the separator is invalid in Docker tag grammar, silently stripped by the npm tooling, and rejected by the Go module tooling.
- Omitting the SHA from the version — rejected: pre-release builds lose traceability to their commit.
