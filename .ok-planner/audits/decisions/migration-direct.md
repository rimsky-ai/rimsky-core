---
audit: migration-direct
artifact: decision:migration-direct
determination: supported
commit: b767a27d
audited: 2026-08-02T09:39:53Z
---

# A one-shot self-hosting run migrates in-process, no separate migrate-binary subprocess

Supported. `cmd/rimsky/cli/compose/launcher.go`'s `MigratePersistence` (tagged `@decision: migration-direct`) opens the freshly-created SQLite database and calls `driver.Migrate` directly in the calling goroutine; `StartRoleStack` — the function `run.go`'s one-shot self-host path invokes — calls `MigratePersistence` before starting the unified role stack, with no subprocess fork anywhere in the call chain (checked: no `os/exec` reference to a migrate binary in `cmd/rimsky/cli/compose`). Two tests exercise the ordering and directness: `TestMigratePersistence_CompletesBeforeStartRoleStack` and `TestMigrationsRunBeforeRunners`.
