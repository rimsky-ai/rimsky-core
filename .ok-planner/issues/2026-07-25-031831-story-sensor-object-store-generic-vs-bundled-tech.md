---
issue: story-sensor-object-store-generic-vs-bundled-tech
kind: audit
category: unclear
artifacts:
  - story:sensor-object-store
status: verified
opened: 2026-07-25T03:18:31Z
---

# A story's name promises a technology its own body refuses to commit to

Rimsky's "sensors" are bundled services that watch for an external trigger and publish a message into the platform when it fires. The story catalog — short documents describing user needs — has one entry per sensor, and three of the four name their technology plainly in title and body: cron-driven, HTTP-polling, webhook. The fourth does the opposite. Its body is titled the generic "Workflow reacts to deposited content" and explicitly states that "the technology of the location and of the detection is a technical decision, not part of this story" — no buckets, no filesystems, no object storage anywhere. Yet its slug is `sensor-object-store`, and the catalog's one-line summary reads "Operator wires object-store-driven message," mirroring the tech-named siblings. The name commits to exactly the technology the body deliberately withholds.

The body's stance isn't an accident to be corrected, either. A separate lower-level decision document settles what the *implementation* is called — an "object store" abstraction with pluggable backends (only a filesystem backend ships today) — and the very fact that the mechanism's name needed a decision argues the story, which is supposed to describe user need rather than mechanism, is right to leave it out. If anything is wrong, it's the slug and summary, not the body.

## Options

- **Genericize slug and summary** ("deposited-content") to match the body — breaks the catalog's visual symmetry with the siblings; needs a file rename plus a citation check.
- **Rewrite the body to name the technology** like its siblings — cheaper, but reverses the file's deliberate story/decision separation.
- **Narrow fix** — keep the slug as a navigation label; reword only the one-line catalog summary generically, fixing the reader-facing mismatch without a rename.

The ruling decides which end of the mismatch moves.

## Ruling

> Recommended ruling (/recommend-rulings): Narrow fix: regenerate the
> TOC line to describe the deposited-content outcome ('Operator wires
> deposited-content-driven message'); keep the slug as a navigation
> name and keep the deliberately tech-agnostic body. Convert its
> outlier heading shape to house style under the heading-shape ruling.
>
> Rationale: The reader-facing inconsistency is the TOC advertising a
> tech commitment the body disclaims; the body's technology-is-a-
> decision framing is exactly the story/decision separation the other
> story rulings in this batch push toward, so it should be kept, not
> rewritten away.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
recommended batch at sign-off. Edit the text to redirect, empty
the section to discuss live, or delete this note to adopt the
ruling as your own. -->
