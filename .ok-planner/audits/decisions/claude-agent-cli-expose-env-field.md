---
audit: claude-agent-cli-expose-env-field
artifact: decision:claude-agent-cli-expose-env-field
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:33:41Z
---

# expose_env is a per-node config field, intersected with the operator allowlist, no container-wide knob

Supported. `CliConfig.ExposeEnv` is parsed exclusively from the node's `cli.expose_env` array (`requestparse.go::ParseCliConfig`); `agentrun.go::firstDisallowedExposeEnv` fails the dispatch with an error naming the disallowed variable, the instance id, and the node id when a declared name is outside the operator allowlist (unit-tested by `TestRunAgentExposeEnvAllowlistViolation`, which asserts all three identifiers appear in the error). Checked every env-exposure entry point in the executor's spawn/resume paths (`clirunner.go::Spawn`, `Resume`, plus the internal clean-exit retry path in `agentrun.go`) — all three route through the same `req.ExposeEnvNames` field sourced only from `cliConfig.ExposeEnv`; there is no second, container-wide exposure path anywhere in the package.
