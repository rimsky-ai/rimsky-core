---
decision: release-scan-docker-scout
status: as-is
---

# CVE scanning gate

## Choice

`docker scout cves --only-severity critical,high --exit-code` against all local images.

## Rationale

Block release on unaddressed critical/high CVEs.
