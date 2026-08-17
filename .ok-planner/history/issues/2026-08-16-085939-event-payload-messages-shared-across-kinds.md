---
issue: event-payload-messages-shared-across-kinds
kind: audit
category: conflicting
artifacts:
  - concept:event-log
status: promoted
opened: 2026-08-16T08:59:39Z
sprint: 2026-08-17-accepted-intake-drain.md
---

# The event-log concept promises one payload message per event kind; several kinds share one

Every event-log row carries a payload declared in the events proto and constructed from the generated type — that is the enforced property, and it holds. The event-log concept additionally says one message per kind. Three substitution-failure kinds share one payload message (the kind varies, the shape does not), commit and abandon claim resolutions share one, and the whole open-ended terminal-error signal family rides one message by construction. The ruling replaces the cardinality claim with the property that is actually protected.

## Options

- Rewrite the invariant to the enforced property (declared shape, generated-type construction) and note a message may serve several kinds, naming the signal family as the case where sharing is structural; cost: none.
- Split messages for the two operational cases; cost: near-duplicate proto messages the codebase's DRY discipline discourages, and the signal family cannot be split anyway.

The ruling corrects the invariant to what the code enforces.

## Ruling

> Generated ruling (/verify-issues): Replace "one message per kind" with the property the code enforces — every payload has a declared shape and is built only from its generated type — and state that a message may serve several kinds, the terminal-error family being the structural case. Forced by the current-state-only rule and the project's proto-payload rule, which already names the real property. Verified against the tree as it stands; nothing was applied.

<!-- Owner: this is a generated ruling, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
