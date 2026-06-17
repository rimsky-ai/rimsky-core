---
decision: wait-set-topic-kind-taxonomy
status: as-is
---

# Wait-set topic discriminator

## Choice

4-value taxonomy (`terminal`, `transient`, `attribute`, `message`); `state` admitted as a defensive fallback for unrecognized rows.

## Rationale

Enable targeted wait-set queries by signal class. The four values are a faithful projection of the canonical signal taxonomy's top-level kinds (see `concept:signal`), so a wait-set row's discriminator answers the same question the signal type-path's first segment answers. The `state` fallback keeps the storage CHECK constraint forgiving for rows whose discriminator does not map to a canonical kind, rather than rejecting at INSERT.

## Alternatives

- A 5-value taxonomy admitting a separate `event` kind alongside the four canonical kinds. Rejected: the canonical signal taxonomy carries no `event/<name>` leaf — the discriminator that the `event/<name>` form would have carried lives in the `terminal/*` payload's `tags` set, matched by subscribers' CEL `when:` filters over `payload.tags` (see `concept:terminal-tag`, `decision:tag-based-subscription`, `decision:no-event-substitution`). A wait-set `topic_kind` value with no signal taxonomy backing is dead vocabulary.
- A taxonomy that omits the `state` fallback. Rejected: the fallback absorbs unrecognized rows at the storage layer without an INSERT failure, which keeps the wait-set ledger resilient to future signal-taxonomy extensions.
- A flat single-value taxonomy (every row tagged `signal` or similar). Rejected: it loses the targeted-query benefit — operators wanting "all attribute waits for instance X" would have to scan every row instead of indexing by discriminator.
