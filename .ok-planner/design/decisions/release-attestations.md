---
decision: release-attestations
status: as-is
---

# Supply-chain attestations on push

## Choice

`docker buildx build --push --provenance=mode=max --sbom=true`.

## Rationale

SBOM + provenance on Hub.

## Notes

2026-06-08 — Decision recorded via spec 2026-06-08-design-corpus-bootstrap.
