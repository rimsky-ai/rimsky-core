---
audit: launch-config-injection
artifact: decision:launch-config-injection
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:34:08Z
---

# Synthetic config files injected through standard discovery

Supported. `cmd/rimsky/cli/compose/synthetic_config.go`'s `WriteSyntheticRimskyYAML` and `WriteSyntheticSupervisorYAML`/`WriteSyntheticSupervisorYAMLWithCallbackPort` write `rimsky.yml` and `supervisor.yml` into the run directory, alongside the SQLite state file and blob root that same rimsky.yml declares (`state.db`, `blobs/`, both under `runDir`). `run.go` then sets `RIMSKY_CONFIG` and `RIMSKY_SUPERVISOR_CONFIG` to those exact paths in the launched process's environment — the identical env vars `lib/control/launch/open_driver.go` (`os.Getenv("RIMSKY_CONFIG")`) and `lib/control/launch/supervisor.go` (`os.Getenv("RIMSKY_SUPERVISOR_CONFIG")`) read to locate config, i.e. the standard discovery surface, not a bespoke injection seam. `cmd/rimsky/cli/compose/synthetic_config_test.go` covers the synthetic-file content (paths, merged executors, claim producers, sibling publishers/named-locks, callback-port splicing), and `launcher_test.go`'s `TestMigrationsRunBeforeRunners` proves the loaded synthetic config drives a real migrate against the run's own `state.db`.
