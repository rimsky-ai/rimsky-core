---
issue: conflict-scratch-mid-dispatch-channel-story-vs-decision
kind: audit
category: conflicting
artifacts:
  - story:opaque-executor-scratch
  - decision:scratch-protocol
  - concept:node-run
status: answered
opened: 2026-07-25T03:18:31Z
---

# story promises a mid-dispatch scratch callback that scratch-protocol says was retired unused

## Problem

story Acceptance/Proof: scratch written 'either mid-dispatch via the executor-protocol scratch callback or by attaching scratch bytes to a settling Outcome'; decision: 'There is no mid-dispatch scratch write channel' — terminal outcome is the only path, the checkpoint callback was considered and retired. Both cannot hold.

## Candidates

- Amend the story to the outcome-only channel
- Reintroduce the mid-dispatch channel and amend the decision

## Discussion

`decision:scratch-protocol` squarely decides this, and every other bearing artifact plus the code agrees with it. Its Choice is explicit and deliberate, not a passing remark: "There is no mid-dispatch scratch write channel: a terminal outcome (Success, Error, or Park) is the only way an executor hands scratch back to rimsky. A dedicated mid-dispatch checkpoint callback was considered and retired unused — a genuine long-running-checkpoint need is a fresh spec with a real consumer, not a channel kept live on speculation."

`concept:node-run` independently states the same shape: "The executor sets scratch by attaching scratch bytes to a settling terminal outcome (success, error, or park) — there is no mid-dispatch scratch write channel (see `decision:scratch-protocol`)." `concept:executor`'s Scratch section: "the executor writes scratch by attaching it to the settling outcome — there is no mid-dispatch scratch channel."

Code confirms the same thing by absence: the supervisor's callback router (`code:lib/runtime/callback.go#149-152`) registers exactly three routes — `/v1/callback/{async_ack_id}` (async terminal), `/v1/runs/{run_id}/keepalive`, and `/v1/runs/{run_id}/attributes` — and no scratch-specific route exists anywhere in `lib/runtime`. `Scratch` fields appear only on the three settling-outcome request/response DTOs in `callback.go` (lines 216, 225, 232), never on a standalone mid-dispatch payload. (This is in contrast to attributes, which — per the sibling issue `issue:conflict-attributes-writeback-channel-decisions`, verified separately in this batch — genuinely does have a live mid-dispatch writeback route; scratch does not, and the story's "mirroring the attributes incremental-writeback pattern" phrasing appears to conflate the two.)

`story:opaque-executor-scratch`'s Acceptance is the lone outlier, asserting scratch can be written "either mid-dispatch via the executor-protocol scratch callback... or by attaching scratch bytes to a settling Outcome" — a channel that decision:scratch-protocol says was "considered and retired unused," that no other artifact or code path corroborates, and that no decision anywhere argues should exist. This is a rotted story, not a live tension between two design positions: nothing in the corpus or the code supports amending the decision instead of the story.

A future sprint should amend `story:opaque-executor-scratch`'s Acceptance and Proof sections to describe the outcome-only channel (the first filed candidate), dropping the mid-dispatch-callback language. No code change is implied.

Closing this issue as answered by `decision:scratch-protocol` (cross-confirmed by `concept:node-run`, `concept:executor`, and the code).
