---
audit: image-entrypoint-role-selection
artifact: decision:image-entrypoint-role-selection
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:34:08Z
---

# Single-binary multi-role entrypoint

Supported. `cmd/rimsky-entrypoint/main.go`'s `selectRoles` returns all three roles for no args, the one named role for a single valid role argument, and an error naming the offending value (and the too-many-args count) for anything else, checked against `main.go`'s `os.Exit(2)` on error; `shouldMigrate` derives migrate ownership from the selected roles (all-roles path and lone `rimsky-control-api` own it; lone `rimsky-scheduler`/`rimsky-supervisor` do not) and `runMigrateIfOwned` runs `rimsky-migrate` synchronously to completion before either `runUnified` or `runSingleRole` starts. `main_test.go`'s `TestEntrypointRoleSelection`, `TestShouldMigrate` (including its explicit "three-container split migrates exactly once" case over all 3 roles), `TestNewLaunchPlan`, and `TestRunMigrateIfOwned_InvalidOverrideExitsNonZero` (which also asserts migrate did not run before the invalid-override error fired) cover the choice end to end.
