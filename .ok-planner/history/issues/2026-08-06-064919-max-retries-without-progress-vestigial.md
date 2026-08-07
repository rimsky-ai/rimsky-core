---
issue: max-retries-without-progress-vestigial
kind: audit
category: corpus-hygiene
artifacts: []
status: promoted
opened: 2026-08-06T06:49:19Z
sprint: 2026-08-06-ruled-intake-drain.md
---

# `max_retries_without_progress` is written on every park and read by nothing

The queue schema carries a per-node tuning column,
`max_retries_without_progress`, that the park path writes on every
parked dispatch (`lib/runtime/runner_terminal_park.go:21-46` through
the persistence layer's tuning update) and that no query anywhere
reads back. It was evidently half of an unfinished mechanism: a
sibling counter column, `consecutive_retries_no_progress`, *is* read
into the parked-row struct, but the comparison that would enforce
"give up after N retries with no progress" — the thing the written
column would parameterize — does not exist in the tree. An operator
who sets the value expecting a retry cap gets nothing, silently.

The live corpus commits to no such mechanism — the error-policy
concept doesn't mention it. The only trace of the intended design is a
pre-corpus discovery scaffold (a deployment default, a per-node
override, a forced error when the counter exceeds the cap) that is not
part of the durable design surface. So neither direction is forced:
the column can go as dead schema, or the mechanism can be finished as
a real feature with a corpus commitment behind it.

## Options

- Drop the column and its write path. Cost: finishing the mechanism
  later means re-adding schema — cheap pre-v1, where migrations may
  drop and recreate freely.
- Implement the missing enforcement, reviving the scaffold's intent.
  Cost: a genuine feature — cap semantics, a terminal error class,
  and a new corpus invariant — with no story currently asking for it.

The ruling decides whether the half-built retry cap dies or ships.

## Ruling

> Recommended ruling (/verify-issues): drop it — remove the column
> and the dead write path; if a no-progress retry cap is ever wanted,
> it returns as a designed feature with its own corpus commitment.
>
> Rationale: the corpus commits to nothing here, the project's
> dead-surface discipline forbids write-only knobs, and pre-v1 the
> schema removal is free; the implement option builds a feature
> nothing has asked for to justify a column. The flip case: an
> operator need for bounding no-progress retry loops — the adjacent
> unvalidated-backoff issue shows this policy surface is getting
> attention — would flip this to finishing the mechanism instead.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
