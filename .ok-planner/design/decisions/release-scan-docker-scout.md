---
decision: release-scan-docker-scout
status: as-is
---

# CVE scanning gate

## Choice

Docker Scout scans every locally-built image in the release chain, failing the release on any unaddressed critical or high severity finding.

## Rationale

Block release on unaddressed critical/high CVEs; Scout ships with the Docker tooling the chain already requires, so the gate adds no separately-installed scanner.

## Alternatives

- A standalone scanner (Trivy, Grype) — rejected: another tool to install and version-pin for coverage the in-box scanner already provides.
- No scan gate — rejected: releases could ship images with known critical or high CVEs.
