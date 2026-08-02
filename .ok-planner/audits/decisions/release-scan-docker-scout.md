---
audit: release-scan-docker-scout
artifact: decision:release-scan-docker-scout
determination: supported
commit: b767a27d
audited: 2026-08-02T09:40:22Z
---

# Docker Scout gates every locally-built image on unaddressed critical/high CVEs

Supported. The Makefile's `scan` target loops over `$(IMAGES)` (all 15 published images) running `docker scout cves --only-severity critical,high` per image, subtracts any CVEs allowlisted per-image in `.scout-accepted-cves.txt`, and `exit 1`s on the first image with unaddressed findings — blocking the `release` chain before `push-images` runs. No separate scanner is installed; the gate uses the `docker scout` CLI plugin already required by the chain. `test/plumbline/build_chain_test.go::TestReleaseScanGatesOnCriticalAndHigh` asserts both the `docker scout cves` invocation and the `--only-severity critical,high` gate are present in the Makefile.
