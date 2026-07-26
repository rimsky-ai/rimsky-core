---
issue: conflict-claim-lifetime-termination-release
kind: audit
category: conflicting
artifacts:
  - concept:claim-lifetime
  - concept:claim-handle
status: verified
opened: 2026-07-25T21:11:30Z
---

# One concept says instance termination releases durable claims; another — and the code — say only deletion does

Claims with a committed-durable lifetime are meant to outlive the run that created them. The claim-lifetime concept says instance termination releases every committed-durable claim handle of the instance; the claim-handle concept says termination abandons only still-active in-flight claims and leaves committed-durable rows alone — release requires the asset-delete endpoint or instance deletion.

The code implements the second: force-terminate explicitly skips any handle not in the active state (`code:lib/runtime/instance_kill.go`), and the committed-durable release routine is called only from the instance-delete handler, which itself requires the instance to already be terminal. So a terminated instance's durable claims survive — by design, since termination is recoverable-adjacent and deletion is the destructive act.

## Options

- Correct `concept:claim-lifetime`'s bullet: instance deletion (not termination) releases committed-durable handles. Cost: a one-clause sprint delta.
- Change the code to release at termination — destroys the termination/deletion distinction the asset model depends on.

## Ruling

> Generated ruling (/verify-issues): amend `concept:claim-lifetime` to say instance
> deletion releases committed-durable claim handles, matching `concept:claim-handle`,
> `concept:asset`, and the code's terminate-vs-delete split.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
