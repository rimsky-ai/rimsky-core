---
decision: release-semver-sha-dot-joined
status: as-is
---

# Pre-release SHA encoding

## Choice

Dot-joined into the SemVer pre-release segment, not `+` build metadata.

## Rationale

The SemVer build-metadata separator is rejected by the downstream tag and module grammars rimsky distributes through, so the SHA has to ride the pre-release segment instead.
