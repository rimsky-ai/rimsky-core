---
decision: in-place-retry
status: as-is
aliases: []
---

# Executor retry is in-place on the existing node-run row

## Choice

When an executor returns an error and the per-error-class policy chain resolves to `retry` (or `discard_claims_then_retry`), the supervisor loops in-process on the SAME node-run row: it sleeps the policy's retry delay and re-invokes the executor against the same dispatch context (same claims, same persisted attribute bag, same dispatch id). The node-run stays in state `running` across the retry loop; no state transition fires for retry; no new node-run row is created.

The policy-evaluation cursor — action-index, retry-counter, current-error-class — lives on the node-run row (per `concept:node-run`). It is read at the start of each error-policy evaluation and written back at the end, persisting the walk through the policy chain across retry iterations and across supervisor-crash recoveries on the same row.

When the policy chain resolves to `give_up` or `pass` instead, the runner exits the retry loop and the row transitions normally — `running → failed (policy_give_up)` or `running → fresh (handler_pass)`. The retry kind has no terminating state transition because the row never leaves `running` for it.

## Rationale

The fresh-row retry pattern — where retry transitions the running row to `failed (policy_retry)`, creates a new stale row with a back-pointer, and copies scratch forward — was structurally incompatible with held-claim coordination. A held holder's claim row is acquired by the held node-run; if that held node-run fails (even just to make room for a retry attempt), the new retry attempt cannot re-acquire the claim that the failed row still owns. The previous workaround was to special-case held-bearing errors and route them to a held-abort path that suppressed the operator's retry policy entirely. That workaround is unnecessary under in-place retry: the claim stays held by the same node-run across retry iterations, sibling holders stay held, and the policy chain reaches give-up naturally if retries keep failing — at which point release-locks with success=false poisons the claim and auto-terminal abandons the coordinated unit through its own machinery.

The fresh-row pattern also forced the policy-evaluation cursor onto the node row (since the cursor had to survive the row-to-row transition), which violated "nodes carry no runtime state." In-place retry keeps the cursor on the node-run row where it conceptually belongs.

A new node-run for the same node — created by a subsequent cascade event — starts with a fresh policy cursor. That is the right semantic: the operator's retry budget applies to one attempt at the work; if the work is re-attempted later because upstream changed, that's a different attempt with its own retry budget.

## Alternatives

Fresh-row retry (the previous model) — rejected because of the structural incoherence with held coordination described above, and because it pushed runtime state onto the node row.

In-place retry with in-memory cursor (no persistence) — rejected because a supervisor crash mid-retry-sleep would reset the cursor, allowing more retries than the policy nominally permits. Persisting on the node-run row gives strict policy-cap accounting under crash recovery at the cost of three columns.

Retry as a separate state (`retrying`) with its own transitions — rejected because it adds state-machine surface for what is fundamentally a loop on the same row. `running` is the correct state during an executor invocation; whether the invocation is the first attempt or the Nth retry is policy-cursor state, not lifecycle state.
