---
decision: release-semver-sha-dot-joined
status: as-is
---

# Pre-release SHA encoding

## Choice

Dot-joined into the SemVer pre-release segment, not `+` build metadata.

## Rationale

`+` is invalid in Docker tags + npm + go-get.

## Notes

2026-06-08 — Decision recorded via spec 2026-06-08-design-corpus-bootstrap.
