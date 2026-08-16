---
audit: release-attestations
artifact: decision:release-attestations
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:47:25Z
checked: 15
unaccounted: 0
---

# Whether every pushed image carries max-mode provenance and SBOM attestations from the build step itself

Supported. One shared push command carries both flags — provenance in max mode and SBOM enabled — and all 15 push invocations in the release target, 4 core and 11 bundled services, are built from that one variable, so no image is pushed without them. The attestations come from the build tool on the push itself, as the rationale claims: the target creates a dedicated container-driver builder for consistent attestation support and pushes through it, and no separate signing or SBOM stage exists anywhere in the chain. A fitness test fails if either flag is dropped.
