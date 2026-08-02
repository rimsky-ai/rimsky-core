---
issue: story-audit-artifact-no-special-reader-home
kind: sprint
category: stories-prescriptive
artifacts:
  - story:audit-artifact
  - decision:artifact-layout
  - decision:persistence-driver
status: verified
opened: 2026-08-01T23:30:00Z
---

# Is "no rimsky-specific reader" a commitment or a side effect?

Every run leaves an audit artifact — a per-run directory holding a state database and a blob store an operator can inspect after the fact. The artifact's story promises in prose that the operator opens it with widely available tooling for the format; no rimsky-specific reader is required. That is a real constraint on storage format — it forbids a bespoke on-disk encoding even where one would be convenient — and the format rules force the story down to its sentence, so the clause needs a home or an explicit burial.

Re-verification found the corpus close but not dispositive. The artifact-layout decision fixes the directory shape with no openness clause (`decision:artifact-layout`). The persistence-driver decision's rationale notes the SQLite adapter "produces a queryable artifact in a widely-supported format" — but as rationale for why SQLite was picked, not as a forward-looking constraint, and it says nothing about the blob spill root (`decision:persistence-driver`). So today the openness holds as a byproduct of driver choices, and nothing stops a future change (a compressed proprietary sidecar, an encrypted blob layout) from silently breaking what the story treats as a promise.

## Options

- State the openable-with-standard-tooling constraint explicitly in the artifact-layout decision — covering the state database and the blob root — then reduce the story; cost: one more clause future storage work must honor.
- Rule the openness an incidental consequence of the driver choices and drop the clause — cheapest, but the operator-facing promise quietly downgrades to a coincidence.

The ruling decides whether third-party readability of the audit artifact is a binding constraint on future storage changes.

## Ruling

> Recommended ruling (/verify-issues): make the constraint explicit — the audit artifact's state database and blob root stay openable with standard tooling for their formats, no rimsky-specific reader — as part of the recorded artifact-layout commitments, then reduce the story to its sentence.
>
> Rationale: the clause is the artifact's operational value (an artifact you need a bespoke reader for is not an audit artifact for the operator who inherits it), and it is load-bearing precisely against future convenience — the one situation where an unrecorded byproduct gets traded away without anyone deciding to; the drop option saves a clause now at that price. Flip case: if the owner intends encrypted or compacted artifact formats where a rimsky reader is the plan, the constraint is wrong to record and the story clause should be dropped deliberately instead.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
