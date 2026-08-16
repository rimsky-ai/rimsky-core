---
issue: envelope-has-no-sender-subject-and-dedup-admits-instance
kind: audit
category: conflicting
artifacts:
  - decision:message-sender-kind-discriminator
status: verified
opened: 2026-08-16T10:00:05Z
---

# The message-sender decision describes an envelope field that does not exist and an enum value that does

Messages carry a sender and a sender kind on the envelope; the message-sender-kind decision says the envelope also carries an orthogonal sender-subject (the actual actor) and that the dedup discriminator is a three-value enum from which "instance" is absent because instance senders are blocked at the wire. Neither holds: the envelope has sender and kind only (the sender-subject lives on the idempotency table, and an operator send's envelope sender is the literal word "operator", so two api-keys are indistinguishable on the audit-visible envelope); and the cascade send path writes "instance" into the dedup discriminator routinely — the wire gate is HTTP-only and cascade sends never cross it. The dedup half is a plain text error; the envelope half is a real question about audit precision. The ruling decides both.

## Options

- Correct the text only: envelope carries sender and kind, sender-subject is the idempotency table's, dedup admits four values with cascade named as the instance writer; cost: operator sends stay indistinguishable on the envelope.
- Add a sender-subject to the envelope so operator and publisher sends carry the actor; cost: a schema change touching read paths and responses.

The ruling decides whether the envelope identifies the actor.

## Ruling

> Recommended ruling (/verify-issues): Correct the text now (four dedup values, cascade as the instance writer, sender-subject on the idempotency table) and add the sender-subject to the envelope in the same change — the audit trail is where "which key sent this" is asked, and the idempotency table is not an audit surface.
>
> Rationale: the decision's own rejected-alternative rationale warns that consumers cannot tell namespaces apart without the subject; that warning is currently true of the envelope. Flip case: if the audit log's access-attempted rows (which do carry the actor) are the intended answer to "who sent it", correct the text only and say so.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
