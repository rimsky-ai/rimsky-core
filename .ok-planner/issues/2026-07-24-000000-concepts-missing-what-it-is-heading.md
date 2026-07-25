---
issue: concepts-missing-what-it-is-heading
kind: audit
category: unspecified
artifacts:
  - design/concepts/*
status: verified
opened: 2026-07-24T00:00:00Z
---

# A third of the concept catalog spells its opening section differently — sweep or bless?

Rimsky's design documents follow a template in which every concept file opens with a section titled `## What it is`. Twenty-five of the 74 concept files instead title that section `## Definition` — same content, different spelling, splitting the catalog into two dialects. No automated check catches this; the drift was found by manual review. The template comes from the authoring toolchain the corpus is written against, not from any project decision, so nothing in the project's own docs settles which spelling wins.

Two adjacent gaps surfaced while confirming the count. One of the 25 files (`atomic-staging`) is also the only concept missing an `## Invariants` section — its must-always-hold properties live in a bespoke caveats table that would need judgment, not a rename, to convert. And 15 of the 25 are independently missing the template's `## Purpose` section — so a heading rename alone wouldn't make them conformant anyway.

## Options

- **Sweep the 25 to `## What it is`** — one uniform catalog; requires the atomic-staging judgment call and, ideally, filling the Purpose gap in the same pass rather than reading those 15 files twice.
- **Revise the upstream template to accept both spellings** — no file edits here, but the template is shared across every project on the toolchain, and the catalog would still carry two spellings unless one is picked going forward.
- **Accept the split permanently** — zero work, and an undocumented two-dialect catalog forever.

The ruling decides: sweep or bless; what atomic-staging's invariants section says if swept; and whether the Purpose gap rides along or files separately.

## Ruling

> Recommended ruling (/recommend-rulings): Sweep the 25 ## Definition
> files to the canonical ## What it is. In the same sweep, give
> atomic-staging an ## Invariants section reframed from the load-
> bearing rows of its substrate-caveats table, and fill the 15 files'
> missing ## Purpose sections rather than filing that as a separate
> issue.
>
> Rationale: Uniformity: two live spellings for one section is a
> dialect split, and two-thirds of the catalog already follows the
> template. Folding the Purpose gap in avoids reading the same 15
> files twice.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
recommended batch at sign-off. Edit the text to redirect, empty
the section to discuss live, or delete this note to adopt the
ruling as your own. -->
