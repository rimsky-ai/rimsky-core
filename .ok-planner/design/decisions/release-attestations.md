---
decision: release-attestations
status: as-is
---

# Supply-chain attestations on push

## Choice

`docker buildx build --push --provenance=mode=max --sbom=true`.

## Rationale

SBOM + provenance on Hub.
