---
decision: node-reset-as-pure-retry-budget-clear
status: as-is
---

# Node reset is a pure retry-budget clear

## Choice

The node-reset endpoint is a pure state-machine-counter-clear verb. It preserves the existing state gate (refusing non-failed-terminal nodes with `409 Conflict`); on a valid reset it clears the error budget and resets the failed-terminal settling-signal-type; it does not enqueue an envelope or open a frame. There is no node-identity-row frame pointer to clear — node rows carry no runtime state, including no frame reference, under frame isolation (see `concept:node`). The operator's workflow for retrying an errored node is two explicit steps: reset, then a message (empty or typed) that invalidates the node so a fresh dispatch is attempted.

## Rationale

Separates state-machine recovery from invalidation. Preserves the principle that no generic node-invalidation surface exists outside the debug-channel (which itself requires pause or breakpoint hit). Keeps a non-debug-mode operator recourse for retry-exhausted nodes; the explicit two-step is the price of separating the concerns.

## Alternatives considered

Retire the endpoint entirely — leaves no non-debug operator recourse for retry-exhausted nodes; fold reset into the debug-channel surface — requires the operator to pause first, a heavier workflow for the common-case error recovery.
