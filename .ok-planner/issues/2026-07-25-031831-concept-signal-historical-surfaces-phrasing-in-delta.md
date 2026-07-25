---
issue: concept-signal-historical-surfaces-phrasing-in-delta
kind: audit
category: other
artifacts:
  - concept:signal
status: verified
opened: 2026-07-25T03:18:31Z
---

# A changelog sentence rode into the corpus inside an unamendable delta

Rimsky's audit vocabulary is the "signal" — the typed event a node-run emits whenever it changes state (success, error, a pause, an attribute change), each written to a permanent ledger. The concept document defining it closes its opening section by saying signal "unifies the historical parallel surfaces": run outcome, transition reason, and a subscription filter field. That's changelog framing — describing the doc's subject by what it replaced — and the house rule for these documents is current-state only. Worse, one of the three named "historical" surfaces isn't historical: transition reason, the vocabulary for *why* a run changed state, is a separate live concept today, coexisting with signal rather than absorbed by it.

The catch is timing. The sentence arrived as part of a just-completed batch of design changes that the corpus is contractually required to match verbatim — fixing it now would break the corpus-matches-delta guarantee that batch certification rests on. Only a future change can reword it; this issue exists to settle *which* reword, so the next sprint touching the file doesn't have to relitigate.

## Options

- **Drop the historical mention entirely** — restate signal's role present-tense; the transition-reason relationship goes unstated.
- **Convert the one live relationship into a boundary note** — a present-tense sentence in the doc's Boundaries section distinguishing signal from transition-reason, dropping the other two untraceable "surfaces" and the historical framing.

The ruling decides the reword direction and whether it warrants anything beyond queuing for the next sprint that touches this doc.

## Ruling

> Recommended ruling (/recommend-rulings): At the next sprint touching
> concept:signal: drop the historical-surfaces parenthetical and add a
> present-tense boundary note distinguishing signal from the still-
> live transition-reason in the Boundaries/Adjacent section. No
> standalone urgency.
>
> Rationale: The boundary against transition-reason is the one piece
> of information in that sentence worth keeping, and Boundaries is the
> corpus's mechanism for stating current relationships — not a
> narrative sentence.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
recommended batch at sign-off. Edit the text to redirect, empty
the section to discuss live, or delete this note to adopt the
ruling as your own. -->
