---
audit: release-dev-mechanical
artifact: decision:release-dev-mechanical
determination: unsupported
commit: 3918d24e
audited: 2026-08-02T09:58:10Z
issue: 2026-08-02-095815-dev-release-script-has-no-test-coverage
---

# Dev-release flow

Unsupported for lack of a test, not for lack of implementation: the dev-release script matches the claimed mechanical shape on inspection — no branch point for operator judgment, no notes file written, and a version derived deterministically from the latest stable tag. But no test anywhere in the project's suites exercises this script or the version string it derives, and it carries no citation to this decision anywhere in source, unlike three sibling release decisions this same codebase guards with dedicated regression tests.
