---
audit: no-event-substitution
artifact: decision:no-event-substitution
determination: supported
commit: b767a27d
audited: 2026-08-02T09:33:40Z
---

# No per-emission event substitution path exists; per-emission data reads through node-attribute substitution

Supported. `lib/graph/attribute/substitution.go`'s source-kind dispatch (annotated `@decision: no-event-substitution` at `resolveDirectiveValueRaw`) has no `event`-prefixed case, and a repo-wide search for an event-payload substitution path, a named-event ledger, or an `{{event...}}` directive form turns up none in Go source — the only other "event" hits in the graph/runtime code are unrelated (event-log appends in the gate evaluator and runner, not a substitution source). Per-emission data instead flows via the sender's attributes delta, read through the existing `nodes.<type>.attribute[.<field>]` source kind, consistent with the concept doc's statement that `messages.<type>.<field>` is sugar over the same `nodes.<type>.attribute` lookup.
