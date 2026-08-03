---
issue: inproc-executor-client-goroutine-channel
kind: audit
category: decision-drift
artifacts:
  - decision:inproc-eventstream
status: promoted
opened: 2026-08-02T09:58:03Z
sprint: 2026-08-03-audit-gap-drain.md
---

# The in-process executor call is bridged through a goroutine and channel the decision's prose denies

When a bundled executor runs in-process (inside the single unified rimsky process instead of behind gRPC), the client that invokes it wraps the handler call in a goroutine, receives the result over a buffered channel, and selects against the caller's context, with panic recovery inside the goroutine (`code:lib/runtime/executor/inproc_client.go::Execute`). The governing decision's Choice says, verbatim, "no goroutine handoff, no channel, no receive loop" (`decision:inproc-eventstream`).

The contradiction is textual, not architectural. What the decision was rejecting — its Alternatives section says so — is a streaming receive loop the other two transports don't have. The code has no stream and no loop: it is a single unary call, bridged so that caller cancellation is honored even if the handler blocks, and so a handler panic is recovered instead of crashing the unified process that also hosts the scheduler, supervisor, and control API. Stripping the bridge to satisfy the prose would trade real safety for a sentence; the sentence is what's wrong.

The ruling decides how the decision's Choice gets rewritten to describe the mechanism that actually exists.

## Options

- Amend the decision's Choice to describe the real shape: a unary call bridged through a goroutine and buffered channel for cancellation and panic isolation — still no event stream, no receive loop. Cost: rewriting a Choice is an intent-level corpus mutation, so it rides a sprint.
- Revert the code to a bare synchronous call. Cost: an unrecovered handler panic takes down the whole single-process deployment, and cancellation stops working — a severe regression with no motivating benefit.

## Ruling

> Generated ruling (/verify-issues): amend the decision's Choice to describe the implemented mechanism — a unary in-process call bridged through a goroutine and buffered channel solely for context-cancellation and panic isolation, with no event stream and no receive loop — and keep the code as is. The decision's own rejection targets streaming, which the code does not do; the literal "no goroutine, no channel" clause mis-states the boundary the decision actually drew, and reverting the code to match it would strip panic isolation from the unified process. Corpus catches up; only a sprint may rewrite the Choice.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
