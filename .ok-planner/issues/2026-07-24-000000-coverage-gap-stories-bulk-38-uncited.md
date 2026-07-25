---
issue: coverage-gap-stories-bulk-38-uncited
kind: audit
category: proof
artifacts:
  - design/stories/*
status: verified
opened: 2026-07-24T00:00:00Z
---

# Forty-six user promises have nothing in the code that proves them

Rimsky keeps a catalog of "stories" — short documents, each a durable promise the product makes to a user, meant to be provable by a citation (`@story: <name>`) on the test or demo that exhibits the promise working. A sweep found a batch of live stories with zero citations anywhere in the tree: nothing demonstrably proves them. Re-running the check today finds 46, not the 38 originally filed — the original sweep undercounted, likely a tooling bug worth re-checking against the sibling decisions-citation issue that used the same method.

Each uncited story needs one of three fates: the proof exists and just needs its citation added; the promise is no longer delivered and the story should retire; or the proof genuinely needs building — real implementation work, not bookkeeping. Spot-checking shows the batch isn't uniform: some stories describe their proof in prose (find it, tag it — fast), others say nothing more than "executable proof" (a slow author-or-retire judgment). Nothing in the corpus mandates citation coverage or treats a missing tag as evidence a promise is dead, so there's no mechanical pressure — only the fact that 46 live promises currently have no discoverable trail from code back to the expectation they encode.

## Options

- **One planned restoration pass over all 46**, staged by the completeness split: fast lane tags existing tests, slow lane gets the author-or-retire walk.
- **Per-story issues** — same triage, 46 separate judgment calls, maximum flexibility, maximum overhead.
- **Add a standing coverage check** so the gap can't silently regrow — new tooling for a requirement the corpus doesn't currently state.
- **Do nothing** — nothing forces a fix; the gap and its growth remain live.

The ruling decides: work the corrected 46 and by which mechanism; whether a standing check is worth adding; and whether the sibling issue gets re-counted with the fixed method first.

## Ruling

> Recommended ruling (/recommend-rulings): One coverage-restoration
> planning pass over the corrected 46-slug list, staged by the proof-
> completeness split: fast lane annotates existing tests that already
> exhibit the story; slow lane gets the author-or-retire walk. No
> standing coverage check yet.
>
> Rationale: The batch isn't uniform, and the split lets the cheap
> majority move without waiting on the hard cases; a standing check is
> worth revisiting only after the drain shows how much of the gap was
> real.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
recommended batch at sign-off. Edit the text to redirect, empty
the section to discuss live, or delete this note to adopt the
ruling as your own. -->
