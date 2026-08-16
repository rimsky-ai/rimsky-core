---
issue: migration-history-was-rebased-against-the-decision
kind: audit
category: conflicting
artifacts:
  - decision:migrations-append-only-numbered
status: verified
opened: 2026-08-16T09:30:08Z
---

# The migration history was collapsed and renumbered, which the append-only decision rejects

The migrations decision says migrations are append-only and numbered, and rejects editing or rebasing them pre-v1 because it breaks the runner's applied-prefix contract on any database that ran the old sequence. The tree did the rejected thing, twice: ordinals two through thirteen were collapsed into the baseline (whose header says existing databases must be dropped and recreated), and two later ordinals were reassigned to different migrations (their headers say the reuse is safe only for a dropped pre-v1 dev database and that post-v1 ordinals are never reused). The runner keys idempotency on filename with no checksum, so nothing enforces the discipline either way. The ruling decides how the decision records the pre-v1 carve-out and whether the runner starts enforcing it.

## Options

- Amend the Choice to state the pre-v1 carve-out taken (collapse and ordinal reuse legal only while every database is dropped and recreated) and name the release where append-only becomes absolute; cost: none.
- Keep the decision and record the collapse as a one-time historical departure in the persistence concept; cost: it was not one-time — the ordinal reuse followed.
- Add a content digest per applied filename to the runner so a rewritten migration fails loudly; cost: a runner change, combinable with either.

The ruling decides whether pre-v1 rebasing is a stated allowance and whether the runner enforces the rule.

## Ruling

> Recommended ruling (/verify-issues): Amend the decision to state the pre-v1 allowance the project has actually used — collapse and renumber only while every database is disposable, with the drop-and-recreate requirement named — declare append-only absolute from v1, and add the content digest to the runner so the absolute rule is enforced from the day it starts.
>
> Rationale: the pre-v1 rules file already says "break freely, say so when a dev database must be nuked", which is what happened; the decision should match that stance rather than pretend, and the digest is what makes the v1 promise mechanical. Flip case: if the owner wants append-only now (no more collapses before v1), keep the decision as written and add the digest immediately — the two headers become the last exception.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
