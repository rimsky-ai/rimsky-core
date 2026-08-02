---
audit: release-attestations
artifact: decision:release-attestations
determination: supported
commit: b767a27d
audited: 2026-08-02T09:40:22Z
---

# Every pushed image is built with provenance-max and SBOM attestations

Supported. The `push-images` Makefile target's shared `BUILDX_PUSH` invocation carries `--provenance=mode=max --sbom=true` and is used for all 15 published images (the 4 core images plus the 11 bundled-service images enumerated in the `IMAGES` list), so both attestations are attached on every push rather than through a separate signing pipeline. `test/plumbline/build_chain_test.go::TestReleasePushCarriesAttestations` asserts both flags are present in the Makefile, and `RELEASING.md` documents the same mechanism and its Hub-UI visibility.
