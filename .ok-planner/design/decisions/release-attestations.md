---
decision: release-attestations
status: as-is
---

# Supply-chain attestations on push

## Choice

Build-tool attestations with both provenance (max mode) and SBOM enabled on every pushed image.

## Rationale

Consumers can verify what an image contains and how it was built, and the attestations are emitted by the same build step that pushes — no separate signing pipeline to operate.

## Alternatives

- No attestations — rejected: consumers have no verifiable account of an image's contents or build origin.
- A separate post-build signing/SBOM toolchain (e.g. cosign + syft) — rejected: extra pipeline stages and key handling for what the build tool emits natively.
