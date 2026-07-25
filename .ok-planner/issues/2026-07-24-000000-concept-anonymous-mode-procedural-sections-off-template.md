---
issue: concept-anonymous-mode-procedural-sections-off-template
kind: audit
category: muddy-boundary
artifacts:
  - concept:anonymous-mode
status: verified
opened: 2026-07-24T00:00:00Z
---

# Same two walkthroughs, second filing — settle both in one ruling

This is a near-duplicate of `issue:anonymous-mode-non-template-sections`: the same two step-by-step operator procedures (the first-credential bootstrap walkthrough and the lost-credential break-glass recovery) sitting inside the anonymous-mode concept document — a definition document whose fixed shape (definition, purpose, boundaries, invariants) has no room for procedure. The two filings differ only in which remedies they list; a decision on one silently forecloses options on the other, so one ruling should settle both.

The extra options this filing brings: move both procedures to an operator-facing README — which would introduce a documentation surface this project deliberately doesn't maintain (its public docs live out-of-tree, and its code-style rules discourage explanatory comments, so "rely on inline code doc" effectively means the knowledge stops being written down anywhere durable); or extend the concept template itself to admit a procedural section — a rules change affecting every future concept, cutting against the corpus's stated philosophy that procedure belongs in code, plans, or other documentation.

## Options

- **Move both to a README or dedicated story** — bootstrap fits the existing story; recovery fits neither naturally, and a README is a new surface to maintain.
- **Strip and rely on code comments** — weakest here, since the house style discourages exactly those comments.
- **Extend the template** — broad blast radius, against the grain.
- **The sibling filing's shape**: bootstrap folds into its story; recovery's commitments become concept invariants; both walkthroughs drop.

The ruling here is settled jointly with the sibling — see its file for the fuller treatment.

## Ruling

> Recommended ruling (/recommend-rulings): Same resolution as
> issue:anonymous-mode-non-template-sections (one ruling for both
> filings): Bootstrap sequence folds into story:anonymous-mode-
> bootstrap; Break-glass rephrases into concept invariants; both
> walkthroughs drop; no template extension.
>
> Rationale: The two filings target the same two sections; a split
> disposition (story for the user journey, invariants for the recovery
> commitments) preserves everything load-bearing at the right
> altitude.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
recommended batch at sign-off. Edit the text to redirect, empty
the section to discuss live, or delete this note to adopt the
ruling as your own. -->
