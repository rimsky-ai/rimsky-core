---
audit: claude-agent-env-passthrough-allowlist
artifact: decision:claude-agent-env-passthrough-allowlist
determination: supported
commit: b767a27d
audited: 2026-08-02T09:33:41Z
---

# Executor-container env reaches the CLI child only via the declared allowlist intersection, never wholesale

Supported. `opts.go::LoadOptsFromEnv` reads the operator allowlist from the handler's own process env (same var name in both containerized and all-in-one deployment, since it is read from `os.LookupEnv` unconditionally); `agentrun.go` intersects it against each node's `cli.expose_env`, and `clirunner.go::collectExposedEnv` looks the allowed names up from `os.LookupEnv` and merges them with the fixed `RIMSKY_CALLBACK_URL`/`RIMSKY_CALLBACK_TOKEN` plumbing and the CLI auth env before spawn — never a whole-`process.env` spread (checked all env-construction call sites in `clirunner.go::Spawn` and `Resume`: both merge exactly `collectExposedEnv`, `authEnv.Env`, and `req.Env`, nothing else). The violation path is unit-tested (`TestRunAgentExposeEnvAllowlistViolation`), the injection path is unit-tested (`TestRunAgentExposeEnvPassedToSpawn`, `TestSpawnExposesOnlyRequestedEnv`), and the security invariant — rimsky never sees the plaintext value — is proven end-to-end by a scenario test that scans the full persisted attribute bag for two different exposed secrets' plaintext and finds neither. The zero-value (`Allowlist{}`) is open by construction and is exercised directly by `TestRunAgentStdioAllowedWhenAllowlistOpen`.
