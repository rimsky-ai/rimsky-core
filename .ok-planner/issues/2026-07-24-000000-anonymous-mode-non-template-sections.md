---
issue: anonymous-mode-non-template-sections
kind: human
category: corpus-hygiene
artifacts:
  - concept:anonymous-mode
  - story:anonymous-mode-bootstrap
status: verified
opened: 2026-07-24T00:00:00Z
---

# Two operator how-to walkthroughs are living inside a definition document

Rimsky's design corpus keeps a fixed shape for "concept" documents — a definition, its purpose, its boundaries, and its invariants (properties that must always hold). The concept describing anonymous mode (a fresh deployment with no admin credentials yet) carries two extra sections that don't fit that shape: a six-step walkthrough of bootstrapping the first admin credential, and a short procedure for recovering from a lost admin credential (revoking credential rows directly in the database). Both are accurate descriptions of real behavior — the question is purely where they belong, because step-by-step operator procedure is exactly the kind of content the corpus's rules route elsewhere.

The landing spots are uneven. The bootstrap flow already has a natural home: a "story" document (a statement of durable user expectation) exists for it and covers the user-visible shape, just without the procedural detail. The recovery procedure has no home at all — no story mentions it, and it's debatable whether a break-glass runbook even qualifies as a user expectation — so dropping it outright would erase the only place the corpus records that this recovery path exists and is intentional. A sibling filing, `issue:concept-anonymous-mode-procedural-sections-off-template`, targets the same two sections with a different option list; one ruling should settle both.

## Options

- **Rephrase the load-bearing facts as invariants, drop the walkthroughs** — keeps the commitments ("recovery requires direct DB access, never a command"; "the first-credential mint returns plaintext exactly once") without the step ordering.
- **Move the walkthroughs into the story** — clean for bootstrap; awkward for recovery, which may not be a story at all.
- **Drop as duplicative** — true for bootstrap once folded into the story; false for recovery, which nothing else documents.
- **Extend the template** to allow a procedural section — a rules change hitting every future concept, against the corpus's stated philosophy.

The ruling decides (for both filings at once): bootstrap's destination, recovery's destination, and whether the template itself changes.

## Ruling

> Recommended ruling (/recommend-rulings): Rule together with
> issue:concept-anonymous-mode-procedural-sections-off-template as one
> resolution: fold the Bootstrap-sequence content into
> story:anonymous-mode-bootstrap's acceptance, and rephrase Break-
> glass's load-bearing properties (recovery requires direct DB access,
> never a CLI verb; the mint returns plaintext exactly once) as
> invariants on concept:anonymous-mode, dropping both step-by-step
> walkthroughs. No CONCEPT-TEMPLATE extension.
>
> Rationale: This keeps every durable property in the corpus at the
> altitude the rules assign it — the story owns the bootstrap journey,
> invariants own the recovery commitments — without a rules change
> whose blast radius is every future concept.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
recommended batch at sign-off. Edit the text to redirect, empty
the section to discuss live, or delete this note to adopt the
ruling as your own. -->
