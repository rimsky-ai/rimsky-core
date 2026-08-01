---
decision: tag-based-subscription
status: as-is
aliases: []
---

# Named-event subscription is terminal/* plus a CEL tag filter

## Choice

The signal taxonomy carries no `event/<name>` leaf. A subscriber matching a named event subscribes to the sender's `terminal/*` type with a CEL `when:` filter over the terminal payload's tag set; the CEL environment for `terminal/*` payloads binds `tags: list<string>` so the membership predicate is expressible.

## Rationale

With tags collapsing the per-emission ledger into terminal-level metadata (`decision:terminal-tags`), the subscription surface follows. A type-path leaf cannot express "this specific named event" once events are tags; the honest surface is "this terminal kind" plus a filter on the tag set.

## Alternatives

- Synthesize `event/<name>` as a virtual leaf computed from tag presence — rejected: adds parser complexity for a taxonomy that no longer matches the persistence.
