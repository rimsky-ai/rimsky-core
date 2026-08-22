---
issue: envelope-has-no-sender-subject-and-dedup-admits-instance
kind: audit
category: conflicting
artifacts:
  - decision:message-sender-kind-discriminator
status: promoted
sprint: 2026-08-21-intake-drain-and-concept-repair.md
opened: 2026-08-16T10:00:05Z
---

# The message-sender decision claims an envelope field the code lacks and omits an enum value the code writes

Messages carry a sender and a sender kind on the envelope. The message-sender-kind decision claims two more things. It claims the envelope also carries an orthogonal sender-subject, the actual actor. It claims the dedup discriminator is a three-value enum, and that "instance" is absent from it because the wire blocks instance senders. Neither claim holds. The envelope carries sender and kind only, and the sender-subject lives on the idempotency table. An operator send's envelope sender is the literal word "operator", so nobody can tell two api-keys apart on the audit-visible envelope. The cascade send path writes "instance" into the dedup discriminator routinely. The wire gate covers HTTP only, and cascade sends never cross it. The dedup half is a plain text error. The envelope half is a real question about audit precision. The ruling decides both.

## Options

- Correct the text only: the envelope carries sender and kind, the idempotency table carries the sender-subject, and dedup admits four values with cascade named as the instance writer; cost: nobody can tell operator sends apart on the envelope.
- Add a sender-subject to the envelope so operator and publisher sends carry the actor; cost: a schema change that touches read paths and responses.

The ruling decides whether the envelope identifies the actor.

## Ruling

> Recommended ruling (/verify-issues): Correct the text now: four dedup values, cascade as the instance writer, and the sender-subject on the idempotency table. Add the sender-subject to the envelope in the same change. Readers ask "which key sent this" of the audit trail, and the idempotency table is not an audit surface.
>
> Rationale: the decision's own rejected-alternative rationale warns that consumers cannot tell namespaces apart without the subject. That warning holds of the envelope now. Flip case: the audit log's access-attempted rows do carry the actor. If those rows are the intended answer to "who sent it", correct the text only and say so.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
