---
issue: held-frames-action-description-wrong
kind: audit
category: doc-drift
artifacts: []
status: repaired
opened: 2026-08-06T06:49:09Z
---

# Does the `diagnostics:read` action-registry description accurately describe what `held-frames` returns?

No, and the design corpus already dictated the correct wording:
`concept:frame` states explicitly that "The held-frames diagnostics endpoint
on the control API surfaces frames held by a `parked` node-run; held-claim
holds are not reported there" and "the held-frames diagnostic reports only
the `parked` subset" of the held-frame population. This matches the code:
`handleAdminHeldFrames` (`lib/control/controlapi/admin_diagnostics.go`) and
its test `TestAdminHeldFrames_GroupsByFrame` are built solely from the
parked-diagnostic query — confirmed still true on the current tree.

The rule this repair follows: align a stale sentence in code to the
commitment the design corpus (`concept:frame`) and the code already agree
on — no commitment changed, only the action-registry's `Description` string
brought into line with reality.

Repaired in `lib/control/controlapi/actions.go`: changed the `diagnostics:read`
action's `Description` from "List frames held by holding-subgraph claims and
undelivered producer-verb outbox entries." to "List frames held by a parked
node-run (held-claim holds are not reported here) and undelivered
producer-verb outbox entries."

Verified: `go build ./lib/control/...`, `go test ./lib/control/controlapi/...
-run TestAdminHeldFrames` pass; `golangci-lint run ./lib/control/controlapi/...`
and the plumbline lint both clean.
