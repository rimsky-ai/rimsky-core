---
decision: no-event-substitution
status: as-is
aliases: []
---

# event/<name> substitution path removed

## Choice

The `nodes.<emitter>.event.<name>.<json_path>` substitution path is removed entirely. Per-emission data lives in `attributes_delta`, available via the existing `nodes.<emitter>.attribute.<key>` substitution path.

## Rationale

With named events collapsed and the named-event ledger removed, the substitution path has nothing to read from. Per-emission data was always more honestly attribute data.

## Alternatives

None — fully redundant once the ledger is removed.
