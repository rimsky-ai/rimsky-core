---
issue: conflict-scratch-mid-dispatch-channel
kind: audit
category: conflicting
artifacts:
  - decision:scratch-protocol
  - story:opaque-executor-scratch
status: verified
opened: 2026-07-25T21:11:30Z
---

# A story exercises a mid-dispatch scratch channel that was deliberately never built

Scratch is the opaque byte payload an executor can persist across park/resume cycles. The scratch-protocol decision is explicit: there is no mid-dispatch scratch write channel — scratch rides only a settling outcome — and a dedicated checkpoint callback was considered and retired unused. The supervisor's callback listener confirms it: exactly three per-run routes exist (async callback, keepalive, attribute writeback), none for scratch (`code:lib/runtime/callback.go`).

The opaque-executor-scratch story nonetheless offers "either mid-dispatch via the executor-protocol scratch callback... or by attaching scratch bytes to a settling Outcome." The first half describes a channel that does not exist; the story's own worked example only exercises the second.

## Options

- Strike the mid-dispatch clause from `story:opaque-executor-scratch`; the settling-outcome path is the whole mechanism and the story's Acceptance/Proof already stand on it. Cost: a one-clause sprint delta.
- Build the mid-dispatch channel — re-opening a decision that explicitly retired it, with nothing new motivating it.

## Ruling

> Generated ruling (/verify-issues): amend `story:opaque-executor-scratch` to drop
> the mid-dispatch scratch-callback clause, leaving the settling-outcome path —
> the only channel the code and `decision:scratch-protocol` recognize.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
