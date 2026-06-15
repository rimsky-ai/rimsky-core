---
decision: release-scan-docker-scout
status: as-is
---

# CVE scanning gate

## Choice

A vulnerability-scanning step over every locally-built image that fails the build on any unaddressed critical or high severity finding.

## Rationale

Block release on unaddressed critical/high CVEs.
