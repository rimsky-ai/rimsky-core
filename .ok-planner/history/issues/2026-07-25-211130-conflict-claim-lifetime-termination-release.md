---
issue: conflict-claim-lifetime-termination-release
kind: audit
category: conflicting
artifacts:
  - concept:claim-lifetime
  - concept:claim-handle
status: repaired
opened: 2026-07-25T21:11:30Z
---

# Does instance termination release committed-durable claim handles, or only instance deletion?

Only instance deletion — confirmed in code: `lib/control/controlapi/instances.go::handleDeleteInstance` requires `inst.TerminatedAt != nil` (a 409 conflict otherwise) before it calls `runtime.ReleaseCommittedDurableClaims`; that release routine has no other caller. `lib/runtime/instance_kill.go` (force-terminate) force-fails in-flight runs and abandons only still-active claims — it never touches committed/durable rows. `concept:claim-handle` already documented this correctly ("Instance termination alone abandons the instance's still-active in-flight claims but does not release committed-durable rows"); only `concept:claim-lifetime`'s durable bullet was wrong, claiming "instance termination... releases every committed-durable claim handle of the instance."

The rules determine the fix and it changes no commitment: `concept:claim-handle` and the code already agree on the terminate-vs-delete split; only `concept:claim-lifetime`'s sentence needed to match. Repaired per the mechanical-vs-judgment rule's named example — aligning a stale sentence to the commitment the code and the counterpart artifact already agree on.

Changed `.ok-planner/design/concepts/claim-lifetime.md`: the durable-lifetime bullet now reads "Instance termination alone abandons the instance's still-active in-flight claims but does not release committed-durable rows; release requires explicit operator action (the asset-delete endpoint) or instance deletion (permitted only once the instance is terminal), which releases every committed-durable claim handle of the instance, held or not," replacing the prior "instance termination... releases" claim.

Verified via code reading only (`lib/control/controlapi/instances.go`, `lib/runtime/instance_termination.go`, `lib/runtime/instance_kill.go`); docs-only change, no build/test impact.
