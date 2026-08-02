---
audit: launch-integration
artifact: decision:launch-integration
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:34:08Z
---

# Compose verb mirrors the entrypoint's role orchestration

Supported. Both the all-in-one entrypoint (`cmd/rimsky-entrypoint/main.go`'s `runUnified`) and the compose verb (`cmd/rimsky/cli/compose/launcher.go`'s `StartRoleStack`, via `startRoleStackFn = launch.StartUnifiedStack`) call the same `lib/control/launch.StartUnifiedStack`, which starts scheduler, supervisor, then control-api in that fixed order, appends each one's `StopFunc` to `UnifiedStack.stops`, and whose `Drain` walks `stops` back to front (`for i := len(s.stops) - 1; i >= 0; i--`); both callers then `select` on a signal channel versus `stack.FailCh()`. `RIMSKY_PROCESS_ROLE=unified` is set by both paths — the entrypoint's no-command branch and `compose/run.go`/`template_run.go` — which is what `lib/foundation/persistence/blob_config.go`'s memory-backend gate checks. `lib/control/launch/unified_test.go` (start order, `DrainReversesStartOrder`, failure-forwarding, cancel-drains-started-roles) and `cmd/rimsky/cli/compose/launcher_test.go` (`TestStartRoleStack_BootsAndDrains`, `TestMigrationsRunBeforeRunners`) exercise the shared launcher from both call sites.
