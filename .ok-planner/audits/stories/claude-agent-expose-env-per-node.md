---
audit: claude-agent-expose-env-per-node
artifact: story:claude-agent-expose-env-per-node
determination: supported
commit: b767a27d
audited: 2026-08-02T09:33:41Z
---

# Per-node expose-env declaration, operator boundary, intersection, no plaintext persisted

Supported. `agentrun.go::firstDisallowedExposeEnv` rejects a node's `cli.expose_env` entry that is outside the `RIMSKY_CLAUDE_AGENT_EXPOSE_ENV_ALLOWLIST` operator allowlist, naming the variable, instance, and node, and `clirunner.go::collectExposedEnv` looks the allowed names up from the handler's own process environment and injects only those into the spawned CLI child, unit-tested at both layers (`TestRunAgentExposeEnvAllowlistViolation`, `TestSpawnExposesOnlyRequestedEnv`). The intersection and the no-persisted-plaintext guarantee are proven together end-to-end by a three-node scenario test where each node's declared variable is visible in its own CLI child (by presence and value digest) and absent from every other node's, and where the full persisted `latest_attributes` bag for all three nodes is scanned and shown to contain neither secret's plaintext.
