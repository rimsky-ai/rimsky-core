---
issue: conflict-cancel-siblings-toc-vs-intrinsic
kind: audit
category: conflicting
artifacts:
  - concept:cancel-siblings
  - concept:fan-out
status: verified
opened: 2026-07-25T21:11:30Z
---

# The concepts index describes cancel-siblings as an opt-in flag; the concept says it is intrinsic

When a fan-out run's aggregation policy is `strict`, rimsky proactively cancels the still-running sibling partitions once one fails. The concepts catalog's one-line index entry describes this as "a boolean field... that turns on proactive sibling cancellation," but the concept body itself says the opposite — it is "not a separate, configurable behavior — it is intrinsic to choosing strict" — and the fan-out concept agrees ("strict and first both force-cancel unconditionally"). No such boolean exists anywhere in the template spec grammar.

This is a stale index sentence, nothing more; the concept body and the code agree.

## Options

- Correct the `concepts.md` index line to the intrinsic semantics. Cost: a one-line sprint delta.
- Amend the concept body toward a flag — impossible; no flag exists to describe.

## Ruling

> Generated ruling (/verify-issues): correct the cancel-siblings entry in the
> concepts index to describe intrinsic strict-policy behavior, matching the concept
> body and the code. There is no field in the spec grammar for the current index
> sentence to be true of.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
