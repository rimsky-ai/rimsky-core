---
audit: env-var-convention-across-modes
artifact: decision:env-var-convention-across-modes
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:35:29Z
---

# Bundled handlers read operator config from the same env-var names in both deployment modes

Supported. Bundled service handlers (checked: the claude-agent executor's `RIMSKY_CLAUDE_AGENT_MCP_ALLOWLIST`/`RIMSKY_CLAUDE_AGENT_EXPOSE_ENV_ALLOWLIST`, the sensors' `RIMSKY_SENSOR_*_HOST`/`_PORT`, the openlineage subscriber's `RIMSKY_OPENLINEAGE_BACKEND_URL`) read process env with no mode-specific branching, and the `rimsky.yml` schema parsed by `LoadRimskyConfigYAML` (`lib/control/config/claim_producers.go`) carries no per-service config block — only `claim_producers`/`executors`/`publishers`/`validators`/`data_processors` endpoint/protocol entries and the persistence/blob/locks/retention surfaces, none of which is bundled-handler operator config. `TestSnapshotAndSetEnvPreservesOperatorServiceEnv` in `cmd/rimsky/cli/compose/template_run_test.go` directly proves the ephemeral-run env wrapper (`snapshotAndSetEnv`) passes the claude-agent allowlist vars through unchanged while still setting the unified-mode markers.
