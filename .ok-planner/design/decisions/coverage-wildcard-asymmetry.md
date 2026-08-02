---
decision: coverage-wildcard-asymmetry
---

# Wildcard subscriptions cover per-field reads; per-field subscriptions do not cover whole-pull reads

## Choice

A wildcard `attribute/*` subscription covers both per-field and whole-pull reads of the same sender; a per-field subscription covers only the matching per-field read and does not cover a whole-pull read.

## Rationale

A per-field reader watches one field; a whole-pull reader needs to know about every field, so requires the wildcard. The asymmetry is intentional.

## Alternatives

- Symmetric coverage, where per-field subscriptions jointly cover a whole-pull read — rejected: a whole-pull returns every field, including ones added later, so a finite set of per-field subscriptions can never honestly stand in for the wildcard's every-field interest.
- Exempting whole-pull reads from coverage checking — rejected: an uncovered whole-pull silently misses sender updates, which is exactly what coverage checking exists to surface.
