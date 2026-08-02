---
audit: rimsky-deployment-bootstrap
artifact: story:rimsky-deployment-bootstrap
determination: supported
commit: b767a27d
audited: 2026-08-02T09:34:08Z
---

# Entrypoint role selection + migrate discipline

Supported. `cmd/rimsky-entrypoint/main.go` implements `newLaunchPlan`/`selectRoles`/`shouldMigrate`: no command selects all three roles and runs them in one process (`persistence.TopologyUnified`); a single named role (`rimsky-scheduler`/`rimsky-supervisor`/`rimsky-control-api`) spawns only that role in split topology; migrate ownership is derived so exactly one invocation in a three-container split runs it (`rimsky-control-api` only) while the all-in-one path always owns it, and `RIMSKY_ENTRYPOINT_MIGRATE=1`/`0` force/skip regardless, with any other value rejected before any binary spawns. `main_test.go` exercises all of this directly: `TestEntrypointRoleSelection` checks the mapping for all three roles plus the no-args and unknown-role cases; `TestShouldMigrate` checks default rules, both override values, invalid-override rejection, and asserts the three-container split migrates exactly once by iterating all three roles; `TestRunMigrateIfOwned_SignalInterrupts` and `TestRunMigrateIfOwned_InvalidOverrideExitsNonZero` prove migrate runs synchronously to completion (or is interrupted cleanly by a signal) before any role starts. `dockerfiles/Dockerfile.rimsky` wires `rimsky-entrypoint` as the image's sole `ENTRYPOINT`, so this is the code path every deployment topology actually runs through.
