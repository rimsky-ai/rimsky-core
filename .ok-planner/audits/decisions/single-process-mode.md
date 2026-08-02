---
audit: single-process-mode
artifact: decision:single-process-mode
determination: supported
commit: b767a27d
audited: 2026-08-02T09:33:37Z
---

# All-in-one entrypoint runs scheduler, supervisor, control-api in one process

Supported. `cmd/rimsky-entrypoint/main.go`'s no-arg path (`runUnified`) sets `RIMSKY_PROCESS_ROLE=unified`, collects bundled executor/claim-producer registrations via `bundledwire.CollectBundled` and `os.Exit(1)`s before calling `launch.StartUnifiedStack` if that fails (boot aborts before any role starts), then calls `launch.StartUnifiedStack`, which in turn starts all three role library entry points (`RunScheduler`, `RunSupervisor`, `RunControlAPI`) in-process and drains them in reverse order on one shared shutdown path. The single-role path (`runSingleRole`) spawns a child process per named role and explicitly strips `RIMSKY_PROCESS_ROLE` from the child's env (`envWithoutProcessRole`), keeping per-role-process behavior distinct. `lib/control/launch/unified_test.go`'s `TestStartUnifiedStack_OneDriverAcrossRunners` and `TestStartUnifiedStack_BlobBackendOpenedOnceAndSharedAcrossRunners` confirm all three runners receive the same in-process driver and blob-backend instance, and `cmd/rimsky-entrypoint/main_test.go` covers role selection, migration ownership, and the bundled-registration abort path.
