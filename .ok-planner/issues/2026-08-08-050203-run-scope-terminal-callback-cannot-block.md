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

A lifecycle subscriber is an external service that rimsky notifies as templates
and instances change state. The corpus describes one direction only. The
protocol **delivers** transitions to peers that opt in, and those peers
**react**: they apply substrate setup at deploy, warm a cache at instance
creation, and tear down at undeploy. Neither `concept:lifecycle-subscriber` nor
`story:lifecycle-subscriber-author` says a subscriber may refuse anything.

Six of the seven callbacks refuse things anyway.

## How it happens

The fan-out for template register, deploy, undeploy, deregister, and instance
events runs **inside** the database transaction that performs the transition.
The call site returns the subscriber's error up
(`lib/control/controlapi/templates.go:406-411`, and the same shape at
deregister, deploy, and undeploy). That error rolls the transaction back. rimsky
then leaves the template unregistered and tells the caller the operation failed.
Because the call sits inside the transaction, a notification acts as a veto.

The seventh callback, run-scope terminal, fires after its scope closes, so
nothing remains to roll back. rimsky logs the error and teardown proceeds
(`lib/runtime/lifecycle_fanout.go:113-117`). That callback behaves the way a
delivery-and-reaction protocol should. The other six do not.

The corpus says one thing about a subscriber affecting rimsky: a latency
clause. Events fire synchronously, so "a slow subscriber holds up the
firing process's path." That clause covers delay, not refusal.

## Why it matters beyond tidiness

rimsky has a protocol whose job is refusing things. `concept:validation` covers
that job, and `story:validation-author` promises findings surfaced "as errors
(blocking) or warnings (informational)" at registration time. So a peer has two
ways to block a template registration: the documented one, and an undocumented
side effect of subscribing to notifications.

An implementer can pick the wrong mechanism, because one capability has two
mechanisms and one of them is unwritten. The wrong one puts an external service
in the path of every control-plane transition. A peer that is down or slow then
blocks operations rimsky could have completed.

## Ruling

> A subscriber may block, but never veto. These are two separate things, and the
> code has them backwards.
>
> **No veto.** A subscriber never decides whether an operation is allowed. Its
> error neither refuses a transition nor rolls one back. Refusing a template is
> `concept:validation`'s job, and that protocol exists for it. The six call sites
> that turn a subscriber error into a transaction rollback are the defect.
>
> **But blocking is right, and needed.** When a subscriber provisions what makes
> an instance work, delivery must be at-least-once and dependent execution must
> wait for it. The transition succeeds. The work that relies on the provisioning
> starts only after the peer acknowledges. So the at-least-once commitment stays
> as written, and the ledger keeps tracking to success.
>
> One mechanism is missing: nothing reads the ledger to decide whether work may
> proceed. The ledger answers "already delivered?" and no caller asks it
> "ready?". Building that gate is separate work. What each event blocks differs
> per event: teardown events block nothing, because whatever would wait is being
> removed.

## Where this goes

This issue stays open in the intake for the sprint that takes on the gating. The
examples-removal sprint does not promote it.

`sketch:2026-08-08-subscriber-readiness-gating` carries the worked-through
design: the veto/block distinction, what exists, the per-event gating table, and
the open questions. Those questions are where blocked work waits, what a
permanently failing subscriber does to a deployment, and whether an operator
needs an override.
