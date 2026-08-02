---
audit: release-semver-sha-dot-joined
artifact: decision:release-semver-sha-dot-joined
determination: unsupported
commit: b767a27d
audited: 2026-08-02T09:58:10Z
issue: 2026-08-02-095815-dev-release-script-has-no-test-coverage
---

# Pre-release SHA encoding

Unsupported for the same reason as its sibling dev-release decision: the commit-hash encoding is implemented exactly as claimed — dot-joined into the pre-release segment rather than appended as build metadata — but nothing in the project's suites exercises the script that derives it, and no citation to this decision exists anywhere in source.
