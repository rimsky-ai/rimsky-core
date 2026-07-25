---
issue: coverage-gap-decisions-bulk-160-uncited
kind: audit
category: proof
artifacts:
  - design/decisions/*
status: verified
opened: 2026-07-24T00:00:00Z
---

# Two-thirds of the decision catalog has no link back to the code that enforces it

Rimsky documents its technical decisions in a catalog, and each decision is meant to be linked from the code that enforces it via a citation comment (`@decision: <name>` at the enforcement point). Re-counting finds 166 of 236 live decisions with no such citation anywhere in the tree — up slightly from 160 at filing. The catch that keeps this from being a simple annotate-everything sweep: the citation convention itself scopes tags to "the point of enforcement," so a decision with no enforcing check legitimately has nowhere to put one — and adding a decorative tag anyway would violate the project's own comment rules.

The 166 split into three buckets. Some already have an obvious enforcement mechanism that just never got tagged (import-boundary lint rules, the license checker, pinned library versions). A larger middle group probably has an enforcement site somewhere, findable only by a per-decision search. And a tail of process/philosophy decisions has no single code site at all — the kind the authoring standard says should either gain a real check or be reclassified, not decorated. This issue is joined at the hip with a separately-filed one about decisions missing their "Proof" section (the written statement of what mechanical check would catch a violation): a citation only makes sense once that check is named, so nearly all of this work naturally sequences inside that sweep.

## Options

- **Retire into the Proof-section sweep** — each citation lands as its Proof is authored; the already-enforced bucket gets tagged in that sweep's first phase. This issue stops existing as separate work.
- **Tag the already-enforced bucket now** — a cheap same-day pass; leaves the other two buckets untouched.
- **Full 166-decision search** — most complete, a multi-session project of its own.
- **Accept permanent partial coverage** — citations required only for new decisions going forward.

The ruling decides mainly one thing: does this stand alone, or is it subsumed by the Proof sweep?

## Ruling

> Recommended ruling (/recommend-rulings): retire: subsumed by
> issue:decisions-corpus-wide-missing-proof-field — annotation lands
> with each Proof authored, and the already-enforced bucket
> (depguard-*, license-lint, library pins) gets annotated in that
> sweep's first phase.
>
> Rationale: A citation only makes sense once an enforcing check is
> named, so the ordering is forced; keeping this open would track the
> same work twice.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
recommended batch at sign-off. Edit the text to redirect, empty
the section to discuss live, or delete this note to adopt the
ruling as your own. -->
