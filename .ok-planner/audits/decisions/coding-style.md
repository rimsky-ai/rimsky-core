---
audit: coding-style
artifact: decision:coding-style
determination: unsupported
commit: b767a27d
audited: 2026-08-02T09:58:10Z
issue: 2026-08-02-095812-plumbline-lint-not-enforced-in-ci
---

# Coding style

Unsupported for the CI-enforcement clause; the edit-time enforcement — the materialized plugin, the citation-tag configuration, and the pre-commit hook — is real and currently reports the tree clean. But neither of the repository's two continuous-integration workflows nor any build-orchestration target ever invokes the lint binary. A test built for exactly this purpose exists and runs in the suite, but it resolves the lint binary's path only from two environment variables that are never set in continuous integration, so it unconditionally skips there rather than executing the check — enforcement in CI is inert, not active.
