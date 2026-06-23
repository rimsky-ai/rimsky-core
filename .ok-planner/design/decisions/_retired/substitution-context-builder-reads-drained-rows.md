---
decision: substitution-context-builder-reads-drained-rows
status: retired
---

# Substitution-context builder reads drained wait-set rows

## Retirement note

Superseded by `decision:substitution-deps-from-persisted-senders`. The original choice fused two roles on the wait-set: wake-vs-data. Under diff-based attribute cascade (`concept:cascade`, `concept:signal`), a sender that emits no `attribute/<key>/changed` for an unchanged value left receivers unable to substitute `nodes.<sender>.attribute.<key>` because the substitution builder had no drained row to read from. The fix split the roles: the wait-set is now wake-only (`concept:wait-set`), and substitution deps come from each subscribed sender's most-recent fresh-settled attribute store via direct lookup against the persisted node-attributes ledger.

## Original choice (retained for history)

The substitution-context builder reads drained wait-set rows per `concept:wait-set`; row presence is the key, irrespective of which subscription-flag combination caused the row to land.

## Original rationale (retained for history)

Keying on row presence keeps the builder uniform across every cascade-flag combination — the two-flag distinction is captured implicitly by whether the row exists at all, so the builder does not need a per-flag branch.
