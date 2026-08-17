---
issue: instance-terminated-fires-from-delete-request
kind: audit
category: conflicting
artifacts:
  - concept:lifecycle-subscriber
status: verified
opened: 2026-08-16T09:05:09Z
---

# The instance-terminated lifecycle event fires from the delete request, which the concept says it never does

Lifecycle subscribers are external services that hear instance events. The concept tells them the poll loop alone delivers the instance-terminated event, and the terminate request never does, so a slow subscriber never delays a caller. The concept lists four delivery sites and names that exception. The delete-instance route, legal only once the instance is terminated, fires the event synchronously in-request. Instance-terminated is the one event whose delivery deletes the idempotency row rather than marking it, so the delete request and the poll loop race to deliver to whichever peers the other has not reached. A caller that kills an instance and deletes it immediately usually pays the synchronous cost the concept says this event is exempt from. The ruling decides whether the delete route keeps its fan-out.

## Options

- Document the delete route as a fifth site, and document the ledger handoff with the poll loop; cost: the latency exception stays untrue for delete-triggered delivery.
- Remove the fan-out from the delete route so the poll loop is the sole deliverer; cost: delete no longer guarantees peers have heard before it returns.
- Split into two events, terminal reached and rows deleted; cost: adds a wire contract every subscriber must carry.

The ruling decides how many ways an instance-terminated event can reach a peer.

## Ruling

> Recommended ruling (/verify-issues): Remove the synchronous fan-out from the delete route and let the poll loop be the only deliverer, as the concept states. Delete returns once the rows are gone, and peers hear within the poll interval.
>
> Rationale: the concept's exception exists to keep the caller out of the subscriber's latency, and the ledger race exists only because two deliverers do. One deliverer leaves one path. Flip case: a delete-then-recreate-same-key flow may depend on peers having heard the termination before the recreate. That flow needs the terminated signal to precede the delete's return, and the third option, a distinct rows-deleted event, is the clean way to give it that.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
