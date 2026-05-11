---
tension: handler-slot-count-drift
category: inconsistent
status: open
affects:
  - lifecycle-handler
---

# Handler slot count: "4 handlers + on_event map" vs "5 slots" across prose

## What is muddy

Two framings coexist:

- CLAUDE.md "Vocabulary": "4 declarable lifecycle handlers (`on_acquire_unavailable`, `on_executor_complete`, `on_executor_blocked`, `on_executor_errored`) plus the `on_event` handler map for executor-emitted named events."
- `docs/concepts/handlers.md`: "five slots" (counting `on_event` as one).

Both framings are correct; `on_event` is structurally different (key-indexed map of entries) but shares the same resolve+invalidate vocabulary. The choice between "4 + 1" and "5" affects how readers conceptualize the surface.

## Why it matters

A template author asking "how many places can I declare reactive policy?" gets either 4 or 5 depending on what they read. A new handler addition has to decide which framing to extend.

## Resolution candidates (do NOT pick)

- Unify to "5 slots" everywhere; treat `on_event` as a slot whose value is a map.
- Unify to "4 + 1" with a structural explanation.
- Restructure prose to avoid quoting the count; describe each slot individually.

## Evidence

- `_discover/reactive-loops-and-lifecycle-handlers.md` Observations bullet 1.
- CLAUDE.md "Vocabulary".
- `docs/concepts/handlers.md` "Resolve verdicts" table.

