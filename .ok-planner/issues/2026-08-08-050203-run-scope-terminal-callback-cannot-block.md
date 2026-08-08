---
issue: lifecycle-subscribers-can-block-without-authorization
kind: human
category: enforcement-gap
artifacts:
  - concept:lifecycle-subscriber
  - concept:validation
  - story:lifecycle-subscriber-author
  - story:validation-author
status: verified
opened: 2026-08-08T05:02:03Z
---

# Six lifecycle callbacks can veto operations, and nothing authorizes that

A lifecycle subscriber is an external service rimsky notifies as templates and
instances change state. The corpus describes it in one direction only: the
protocol **delivers** transitions to peers that opt in, so those peers can
**react** — apply substrate setup at deploy, warm a cache at instance creation,
tear down at undeploy. Neither `concept:lifecycle-subscriber` nor
`story:lifecycle-subscriber-author` says a subscriber may refuse anything.

Six of the seven callbacks refuse things anyway.

## How it happens

The fan-out for template register, deploy, undeploy, deregister, and instance
events runs **inside** the database transaction that performs the transition, and
the call site returns the subscriber's error up (`lib/control/controlapi/templates.go:406-411`
and the same shape at deregister, deploy, and undeploy). Returning the error
rolls the transaction back, so the template is not registered and the caller is
told it failed. A notification became a veto as a side effect of where the call
was placed.

The seventh callback — run-scope terminal — fires after its scope has already
closed, so there is nothing left to roll back: the error is logged and teardown
proceeds (`lib/runtime/lifecycle_fanout.go:113-117`). That one behaves the way a
delivery-and-reaction protocol should. The other six are the anomaly.

The nearest thing the corpus says about a subscriber affecting rimsky is a
latency clause: events fire synchronously, so "a slow subscriber holds up the
firing process's path." That is about delay, not about abortion.

## Why it matters beyond tidiness

rimsky already has a protocol whose job is refusing things. `concept:validation`
exists for exactly this, and `story:validation-author` promises findings surfaced
"as errors (blocking) or warnings (informational)" at registration time. So a
peer today has two ways to block a template registration: the documented one, and
an undocumented side effect of subscribing to notifications.

Two mechanisms for one capability, one of them unwritten, is the condition under
which an implementer picks the wrong one — and the wrong one puts an external
service in the path of every control-plane transition, where a peer that is down
or slow blocks operations rimsky could have completed.

## Ruling

> A subscriber may block, but never veto. Two separate things, and the current
> code has them backwards.
>
> **No veto.** A subscriber never decides whether an operation is allowed. Its
> error does not refuse a transition and does not roll one back. Refusing a
> template is `concept:validation`'s job, and that protocol already exists for it.
> The six call sites that propagate a subscriber error into a transaction rollback
> are the defect.
>
> **But blocking is right, and needed.** If a subscriber is provisioning what
> makes an instance work, then delivery must be at-least-once and dependent
> execution must wait until it lands. The transition succeeds; the work that
> relies on the provisioning does not start until the peer has acknowledged. So
> the at-least-once commitment stays exactly as written, and the ledger keeps
> tracking to success.
>
> The missing mechanism is that nothing reads the ledger to decide whether work
> may proceed — it answers "already delivered?" and has never been asked "ready?".
> Building that gate is a separate piece of work, and what each event blocks
> differs per event: teardown events block nothing, since whatever would wait is
> being removed.

## Where this goes

This issue stays open in the intake for the sprint that takes the gating on — it
is not promoted into the examples-removal sprint.

`sketch:2026-08-08-subscriber-readiness-gating` carries the worked-through design:
the veto/block distinction, what already exists, the per-event gating table, and
the open questions — where blocked work waits, what a permanently failing
subscriber does to a deployment, and whether an operator needs an override.
