---
audit: dry-run-request-flag
artifact: story:dry-run-request-flag
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:36:49Z
---

# Per-request dry-run preview on every write action

Supported: the control API's `?dry_run=true` query flag is wired through every write action, runs the same in-transaction validation as the live path, and returns a synthetic `{"dry_run": true, "<would_have_X>": {...}}` envelope without persisting. The registry `v1Actions` (`lib/control/controlapi/actions.go`) enumerates 23 `IsWrite: true` actions (instance:create/terminate/pause/resume/kill/debug-override, breakpoint:create/resume/delete, template:register/deploy/undeploy/deregister, tag:create/set/delete, node:reset, message:send, lineage:prune, asset:delete, auth:create/revoke/rotate); `TestDryRunCoverage_AllWriteActions` (`test/scenarios/auth/dry_run_coverage_test.go`) pulls that exact registry population at runtime, fails if any `IsWrite` action lacks a dry-run test descriptor or if a descriptor exists for a non-write action, and drives every one of the 23 with `?dry_run=true`, asserting a 200, a `dry_run:true` envelope, and the expected `would_have_*` key; most cases additionally assert the underlying row/count is unchanged. Spot-checking the implementation (e.g. instance:create in `lib/control/controlapi/instances.go`) shows the dry-run short-circuit (an `errDryRunOK` sentinel) sits inside the same DB transaction after the same template-state and schema/attribute-override validation the live path runs, so the preview genuinely shares validation with the real write. `TestDryRun_AuthCreateMintsNoKey` further confirms a dry-run preview does not leak a real plaintext credential. The claim is fully backed for the 23-action population actually enumerable in the registry today.
