---
issue: instance-delete-drops-undelivered-lifecycle-events
kind: audit
category: conflicting
artifacts:
  - decision:lifecycle-subscriber-at-least-once-delivery
  - concept:lifecycle-subscriber
status: promoted
sprint: 2026-08-23-row-bytes-outbox-and-log-kinds.md
opened: 2026-08-21T22:29:49Z
---

# Instance delete destroys undelivered lifecycle events

Deleting an instance silently drops any lifecycle delivery its subscribers have not yet received, against the ruled promise that rimsky delivers each lifecycle event at least once (`decision:lifecycle-subscriber-at-least-once-delivery`, which rejects at-most-once by name).

The mechanism: `instance_terminated` is the one lifecycle event never staged into the durable outbox. The poll loop derives it from the idempotency ledger — an instance with `terminated_at` set and ledger rows still standing is owed deliveries. The delete route purges those ledger rows, the instance's staged outbox rows, and its run-scope rows in one transaction, then deletes the instance. Terminate and delete are separate calls, and the poll interval (2 seconds by default) is all that stands between them; the one-shot run verb calls them back to back, so on that shipped path the subscriber never hears `on_instance_terminated`, and any run-scope terminal whose synchronous delivery failed is dropped with it.

Excepting a deleted instance from the promise is foreclosed: the ruled decision rejects exactly that shape ("deliver at most once and drop a failed delivery"). The remaining question is how delete and the promise coexist.

## Options

- **Stage `instance_terminated` into the outbox at termination.** All six lifecycle events then share the durable-outbox path; delete purges the ledger but leaves outbox rows, which the drain already delivers without looking the instance up. Cost: termination writes one more row per subscriber, and the outbox outlives the instance it names.
- **Refuse the delete while deliveries are owed.** Delete returns a conflict until the ledger is drained. Simpler, but delete's contract changes: a one-shot run that terminates and deletes can now block indefinitely on a dead subscriber.

The ruling decides which of the two shapes keeps the at-least-once promise across delete.

## Ruling

> Recommended ruling (/verify-issues): stage `instance_terminated`
> into the lifecycle outbox inside the termination transaction, like
> every other lifecycle event, and let delete purge only the ledger;
> the outbox rows survive and the drain delivers them for an instance
> that no longer exists.
>
> Rationale: it removes the one event that bypasses the outbox rather
> than adding a second delete contract, and a delete that can block
> indefinitely on a dead subscriber (the refusal option) trades an
> operator's ability to remove an instance for a delivery the outbox
> can carry anyway. Flip case: if the owner wants delete to be a hard
> end of all obligations — nothing delivered after removal — the
> refusal option with an operator override is the honest shape, and
> the at-least-once decision needs an amendment saying so.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
