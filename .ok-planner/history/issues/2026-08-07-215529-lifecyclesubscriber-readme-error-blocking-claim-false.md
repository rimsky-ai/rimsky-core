---
issue: lifecyclesubscriber-readme-error-blocking-claim-false
kind: human
category: doc-drift
artifacts:
  - concept:lifecycle-subscriber
  - story:lifecycle-subscriber-author
  - decision:doc-accuracy-gates
status: retired
sprint: 2026-08-08-ruled-intake-drain.md
opened: 2026-08-07T21:55:29Z
github: https://github.com/rimsky-ai/rimsky-core/issues/106
---

# The lifecycle-subscriber README promises a veto one of its hooks doesn't honor

A lifecycle subscriber is an external service rimsky notifies as instances and
runs change state. Its example README states a blanket rule: return a non-nil
error from any callback and the mutation is blocked.

It isn't true for the run-scope terminal callback — and that is the hook an
implementer is most likely to reach for as a veto. The run scope is closed before
fan-out fires, and the subscriber's error is logged and dropped, never surfaced
and never blocking. So a subscriber written to refuse teardown refuses nothing,
and nothing tells it so.

Six further claims in the same README were re-verified and are false:

- It says the example's main file includes the HTTP bridge and points at comments
  there for detail. The file registers gRPC only, mounts no bridge, and carries no
  comments; the bridge lives in the shared server kit.
- It says the instance-terminated callback fires when an instance is deleted. It
  also fires from a background sweep with no delete involved.
- It says only claim-producer and executor entries can declare the protocol.
  Publisher entries can too.
- It describes the per-peer error details as per-store. They're keyed by peer
  name, across producer, executor and publisher peers alike.
- It says the unit test pins a representative subset of the callbacks. It
  exercises all seven.
- Its field list for the instance-created callback omits the routing-identity
  field.

The acceptance-leg count is also off by one — seventh of seven, where there are
eight.

## Ruling

> Retired: the examples module is being removed in full, so the README this issue
> reports on ceases to exist and the drift it documents is dissolved rather than
> corrected. The documentation project maintains the cookbook that replaces it.
>
> Findings underneath these issues that concern rimsky rather than the examples
> were pulled out as their own issues before retirement — an unenforced publisher
> kind, an unproven ordering guarantee on terminal events, and a lifecycle
> callback that cannot refuse — so nothing about the platform is lost with the
> module.
