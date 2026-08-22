---
decision: termination
---

# Run-to-terminal verbs poll quiescence, then terminate their instances

## Choice

The run-to-terminal verbs own termination. Each verb polls the instances it created until every one is quiescent per `concept:instance` — no running frame and no pending message — then terminates each through the control API's terminate action and exits. The self-hosting verbs and the remote one-shot run share this gate. No verb waits on a platform-set terminal stamp.

## Rationale

An instance is durable and never self-terminates; only a terminate action sets its terminal stamp. The verb that drove the work is the actor that knows the work is done, so it decides when its instances end. A platform-side "done" would need a definition the platform does not have, and it would race with late messages by design. Park needs no verb-level handling: the supervisor's time-wake at the park's resume-at carries a parked run forward, and a parked run holds its frame open, so the instance is not quiescent until the run settles.

## Alternatives

- A platform instance-terminal promotion, where the platform stamps an instance terminal when it quiesces and every verb waits on that stamp — rejected: it needs a platform-owned definition of done and races with late messages; it becomes the right primitive only if a story for self-ending instances arrives.
- Exit once instances are created and their roots dispatched — rejected: the verbs' promise is a terminal outcome an operator can script against, not submission.
- Verb-level park handling (treating a park as done, or expiring it from the verb) — rejected: it duplicates the supervisor's park policy in a second place.
