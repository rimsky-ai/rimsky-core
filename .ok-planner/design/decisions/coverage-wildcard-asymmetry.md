---
decision: coverage-wildcard-asymmetry
status: as-is
---

# Wildcard subscriptions cover per-field reads; per-field subscriptions do not cover whole-pull reads

## Choice

A wildcard `attribute/*` subscription covers both per-field and whole-pull reads of the same sender; a per-field subscription covers only the matching per-field read and does not cover a whole-pull read.

## Rationale

A per-field reader watches one field; a whole-pull reader needs to know about every field, so requires the wildcard. The asymmetry is intentional.
