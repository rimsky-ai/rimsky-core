---
issue: concepts-v1-contract-staged-framing
kind: audit
category: unclear
artifacts:
  - concept:replica
  - concept:publisher
  - concept:sensor
status: verified
opened: 2026-07-25T03:18:31Z
---

# Three docs hedge a permanent stance with "the v1 contract"

Rimsky's role binaries — the scheduler, supervisor, control API, publishers, sensors — can each run as a single instance or be scaled to multiple copies, but the platform deliberately does not coordinate between copies of the same binary; keeping replicas from stepping on each other is the operator's job. Three design documents (the general replica posture, publisher, sensor) describe this single-replica stance as "the v1 contract." The house rule says these documents state the project as it stands, not stage commitments by version — and nothing anywhere defines what "v1 contract" commits to or plans a post-v1 change. The general replica doc is otherwise written as a plain permanent stance ("provides no generic replica-aware coordination"), and a sibling doc describes the same posture with no version qualifier at all, treating it as durable. Everything points to "v1" being leftover pre-1.0 hedging rather than a signal of intent.

The one wrinkle worth checking before deleting: "v1" could conceivably mean each binary's own release contract rather than the project's v1 milestone — in which case the fix is a clearer phrasing, not a deletion. But if a multi-replica story genuinely is planned post-v1, that intent belongs in the issue queue, not baked into a permanent doc.

## Options

- **Drop "v1" from all three occurrences** — single-replica is the durable posture, stated present-tense.
- **Sharpen the phrasing** — if "v1" means something narrower than "changes at v1," say that thing instead.

The ruling decides which, resolving the idiom once across all three files rather than doc by doc.

## Ruling

> Recommended ruling (/recommend-rulings): Drop 'v1' from all three
> occurrences (replica, publisher, sensor): single-replica is the
> durable posture, stated present-tense.
>
> Rationale: Nothing in the corpus plans a post-v1 multi-replica
> story, and stories/sensor-cron.md already reads the posture as
> durable without the qualifier — the phrase is copy-paste hedging,
> exactly what current-state-only forbids.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
recommended batch at sign-off. Edit the text to redirect, empty
the section to discuss live, or delete this note to adopt the
ruling as your own. -->
