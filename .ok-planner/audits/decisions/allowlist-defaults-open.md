---
audit: allowlist-defaults-open
artifact: decision:allowlist-defaults-open
determination: unsupported
commit: 3918d24e
audited: 2026-08-02T09:58:10Z
issue: 2026-08-02-095805-claude-agent-allowlist-env-parsing-untested
---

# Bundled-service allowlists default open when operator config is absent

Unsupported for lack of a test, not for lack of code. The only implementation of this allowlist pattern in the codebase correctly opens on an absent environment variable and closes on a present-but-empty one, matching the decision. But no test anywhere in the repository exercises the environment-parsing function itself: every unit test constructs the allowlist type directly, bypassing environment parsing, and the one end-to-end scenario that leaves the relevant variables unset declares no reference for the default-open behavior to accept or reject. The specific open/closed claim is unverified by the project's own suites.
