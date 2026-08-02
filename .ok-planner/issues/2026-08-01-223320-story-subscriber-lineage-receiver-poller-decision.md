---
issue: story-subscriber-lineage-receiver-poller-decision
kind: sprint
category: stories-prescriptive
artifacts:
  - story:subscriber-lineage-receiver
  - concept:lineage
status: verified
opened: 2026-08-01T22:33:20Z
---

# The lineage receiver's poller-not-push choice is named in its story sentence

The bundled lineage subscriber (the shipped service that forwards run-lineage records to an external receiver) is implemented as a Postgres poller over the lineage projection — a ticker that reads new rows on an interval — rather than as a push-style lifecycle subscriber registered with rimsky. Its story names that implementation in the story sentence itself, which crosses the line the authoring rules draw: sentences state outcomes; mechanism choices belong to decisions.

Re-verification confirms the implementation (`code:lib/services/subscribers/openlineage/subscriber.go` runs on a ticker; no lifecycle-subscriber implementation exists in the package). The lineage-record concept explicitly disowns the emission side ("lives with the external-receiver subscriber" — `concept:lineage-record`), and no decision records the poller choice. The corpus has a direct precedent for recording exactly this shape of choice: the object-store sensor's deposit detection is recorded as a decision — periodic listing against a durable watermark, with push alternatives named and rejected (`decision:deposit-detection-watermark`).

## Options

- Record the poller-over-push choice as a decision and restate the story sentence as outcome only — one new decision, matching the watermark precedent; the cost is authoring real rationale (why polling beat a push subscriber: no registration lifecycle, restart-safe, at-least-once from a durable projection).
- Keep the mechanism in the sentence and rule it part of the promise — leaves the catalog with one story whose sentence prescribes implementation, an exception the format rules would have to carry.

The ruling decides whether the poller choice becomes a recorded decision or a sanctioned exception in story form.

## Ruling

> Recommended ruling (/verify-issues): record the poller-over-push choice as a decision — periodic polling of the durable lineage projection, with the push-style lifecycle subscriber named as the rejected alternative — and restate the story's sentence as outcome only (lineage records reach the external receiver).
>
> Rationale: the corpus already made this call once for the object-store sensor's watermark polling, and consistency with that precedent beats minting a one-off exception to the sentences-state-outcomes rule; the choice also has genuine rationale worth keeping (restart safety, no registration lifecycle) that story prose can't durably hold. Flip case: if the subscriber is expected to move to push delivery soon, recording the poller as a durable decision would just schedule a retirement — leave the sentence and file the migration intent instead.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
