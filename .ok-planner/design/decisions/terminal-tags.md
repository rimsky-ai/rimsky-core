---
decision: terminal-tags
status: as-is
aliases: []
---

# NamedEvent collapses into terminal tags

## Choice

Remove the `NamedEvent` message and the `ExecuteEvent.named_event` variant. Add `repeated string tags` (set semantics, deduplicated at decode) to `Success`, `Error`, and `Park`. Rename the observability-protocol capability advertising the executor's emit vocabulary from `declared_events` to `declared_tags` (same wire purpose — the executor's advertised vocabulary that the template-registration gate validates against — under the new name).

## Rationale

The historical runtime semantics for `NamedEvent` were batch-at-terminal — the runner captured events during the stream but processed them only at terminal time. The streaming-ness was cosmetic. Tags collapse multi-emit into a set-on-terminal, removing the parallel ledger, the `event/<name>` signal taxonomy leaf, the substitution path, and the per-event audit emit. Per-emission data moves to `attributes_delta` (see `decision:uniform-attributes-delta`).

## Alternatives

Bundle `NamedEvent` into the terminal body as `repeated NamedEvent events` — rejected because it preserves NamedEvent's overhead without buying anything; tags are cleaner.
