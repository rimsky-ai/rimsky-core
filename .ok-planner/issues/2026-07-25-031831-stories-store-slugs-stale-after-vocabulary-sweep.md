---
issue: stories-store-slugs-stale-after-vocabulary-sweep
kind: audit
category: inconsistent
artifacts:
  - story:store-filesystem
  - story:store-postgres
status: verified
opened: 2026-07-25T03:18:31Z
---

# Two stories still point users at service names that no longer exist

Rimsky recently renamed its shipped filesystem and Postgres "claim producer" services — services that hand out exclusive leases on data items — from "store" naming to `claim-producer-filesystem` and `claim-producer-postgres`. The design corpus's story catalog (short documents describing what a user needs and why, each identified by a stable short name, a "slug") didn't come along: two stories are still slugged `store-filesystem` and `store-postgres`, the catalog still describes them as a filesystem-backed and Postgres-backed "store," and one story's acceptance criterion literally instructs "a template referencing `store-filesystem`" — a name no shipped component answers to anymore, so following the story as written produces a broken template.

The open question is narrower than it looks: nothing in the corpus decides whether a story's slug should track vocabulary changes or stand as a permanent identifier through them. The pre-v1 "break freely" rule targets wire formats and config, not doc identifiers — but the concept catalog already renamed its own claim-store concept to claim-producer, so the corpus has a precedent of slugs tracking vocabulary.

## Options

- **Rename both stories to claim-producer slugs** with body rewrites, sweeping any citations of the old slugs in the same change — finishes the sweep; requires the citation check.
- **Keep the slugs, fix only the prose** — cheaper, but leaves a permanent slug-versus-vocabulary mismatch and two file names that advertise a retired term.

The ruling decides whether slugs track vocabulary or survive it.

## Ruling

> Recommended ruling (/recommend-rulings): Rename both stories to
> claim-producer slugs with body rewrites, sweeping any citations in
> the same change.
>
> Rationale: Slugs are the project's citation vocabulary, and the
> concept catalog already renamed claim-store to claim-producer —
> slug-tracks-vocabulary is the established precedent, and pre-v1 is
> the cheap moment to finish it.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
recommended batch at sign-off. Edit the text to redirect, empty
the section to discuss live, or delete this note to adopt the
ruling as your own. -->
