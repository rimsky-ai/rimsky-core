---
decision: terminal-tags
status: as-is
aliases: []
---

# Terminal tags instead of named events

## Choice

Executor terminals (`Success`, `Error`, `Park`) carry `repeated string tags` with set semantics, deduplicated at decode; there is no `NamedEvent` message and no streaming named-event variant. The observability capability advertising the executor's emit vocabulary is `declared_tags` — the vocabulary the template-registration gate validates against.

## Rationale

NamedEvent's runtime semantics were batch-at-terminal — the runner captured events during the stream but processed them only at terminal time, so the streaming-ness was cosmetic. Tags collapse multi-emit into a set-on-terminal, removing the parallel ledger, the `event/<name>` signal-taxonomy leaf, the substitution path, and the per-event audit emit. Per-emission data travels in `attributes_delta` instead (see `decision:uniform-attributes-delta`).

## Alternatives

- Bundle named events into the terminal body as a repeated event message — rejected: preserves NamedEvent's overhead without buying anything; tags are cleaner.
