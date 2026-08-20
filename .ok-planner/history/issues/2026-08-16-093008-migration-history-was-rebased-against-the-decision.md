---
issue: migration-history-was-rebased-against-the-decision
kind: audit
category: conflicting
artifacts:
  - decision:migrations-append-only-numbered
status: promoted
opened: 2026-08-16T09:30:08Z
sprint: 2026-08-18-recommended-intake-drain.md
---

# The project collapsed and renumbered the migration history, which the append-only decision rejects

The project did what the migrations decision rejects, twice. The decision says migrations are append-only and numbered. It rejects editing or rebasing them pre-v1, because that breaks the runner's applied-prefix contract on any database that ran the old sequence. The project collapsed ordinals two through thirteen into the baseline, whose header says existing databases must be dropped and recreated. It then reassigned two later ordinals to different migrations. Those headers say the reuse is safe only for a dropped pre-v1 dev database, and that post-v1 ordinals are never reused. The runner keys idempotency on filename with no checksum, so nothing enforces the discipline either way. The ruling decides how the decision records the pre-v1 carve-out and whether the runner starts enforcing it.

## Options

- Amend the Choice to state the pre-v1 carve-out the project took: collapse and ordinal reuse are legal only while every database is dropped and recreated. Name the release where append-only becomes absolute; cost: none.
- Keep the decision and record the collapse as a one-time historical departure in the persistence concept; cost: it was not one-time, because the ordinal reuse followed.
- Add a content digest per applied filename to the runner, so a rewritten migration fails loudly; cost: a runner change, combinable with either.

The ruling decides whether pre-v1 rebasing is a stated allowance and whether the runner enforces the rule.

## Ruling

> Recommended ruling (/verify-issues): Amend the decision to state the pre-v1 allowance the project has used: collapse and renumber only while every database is disposable, with the drop-and-recreate requirement named. Declare append-only absolute from v1. Add the content digest to the runner, so the absolute rule is enforced from the day it starts.
>
> Rationale: the pre-v1 rules file says "break freely, say so when a dev database must be nuked", which is what happened. The decision should match that stance rather than pretend otherwise. The digest is what makes the v1 promise mechanical. Flip case: if the owner wants append-only now, with no more collapses before v1, keep the decision as written and add the digest immediately, and the two headers become the last exception.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
