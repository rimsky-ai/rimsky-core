---
decision: in-place-retry
status: as-is
aliases: []
---

# Executor and acquire retries are both in-place on the existing node-run row

## Choice

When the per-error-class policy chain resolves to `retry`, the supervisor loops in-process on the SAME node-run row: it sleeps the policy's retry delay and re-attempts the failed operation against the same dispatch context (same persisted attribute bag, same dispatch id). The node-run stays in its pre-error state across the retry loop; no state transition fires for retry; no new node-run row is created. The rule applies uniformly to two failure surfaces:

- **Executor errors** (post-dispatch handler errors). The row is in state `running`, the supervisor re-invokes the executor against the same claims, and the row stays `running` across iterations.
- **Acquire errors** (pre-dispatch acquire-unavailable or acquire-producer-error). The row is in state `stale`, the supervisor re-attempts claim acquisition on the same dispatch row, and the row stays `stale` across iterations.

Both surfaces use the same `applyErrorPolicy` machinery and the same in-place loop shape; only the operation re-attempted differs. A single retry counter on the node-run row (per `concept:node-run`) tracks both surfaces: it advances on any retry regardless of which error class triggered it, and a single node-level max-retries budget (plus a retry-backoff delay) bounds the total count across every error class on that row — there is no per-class budget and no cursor reset on a class change.

When the policy chain resolves to `give_up` or `pass` instead, the runner exits the retry loop and the row transitions normally — `{stale,running} → failed (policy_give_up)` or `{stale,running} → fresh (handler_pass | acquire_pass)`. The retry kind has no terminating state transition because the row never leaves its pre-error state for it.

## Rationale

The fresh-row retry pattern — where retry transitions the failing row to `failed (policy_retry)`, creates a new stale row with a back-pointer, and copies scratch forward — was structurally incompatible with held-claim coordination on the executor side. A held holder's claim row is acquired by the held node-run; if that held node-run fails (even just to make room for a retry attempt), the new retry attempt cannot re-acquire the claim that the failed row still owns. The previous workaround was to special-case held-bearing errors and route them to a held-abort path that suppressed the operator's retry policy entirely. That workaround is unnecessary under in-place retry: the claim stays held by the same node-run across retry iterations, sibling holders stay held, and the policy chain reaches give-up naturally if retries keep failing — at which point release-locks with success=false poisons the claim and auto-terminal abandons the coordinated unit through its own machinery.

The fresh-row pattern also forced the policy-evaluation cursor onto the node row (since the cursor had to survive the row-to-row transition), which violated "nodes carry no runtime state." In-place retry keeps the cursor on the node-run row where it conceptually belongs.

The acquire-error surface is unified with the executor-error surface for symmetry: the held-coordination concern doesn't arise on the acquire side (no claim has been successfully acquired at the point acquire fails), but using one mechanism for both classes of retry collapses the runtime to a single error-policy code path and removes a class of duplication-bugs where the two pathways drift apart. The dispatch row is the unit of retry for both surfaces — it stays in the queue across retry iterations, claimed by the same supervisor, with the retry counter advancing on each iteration.

A new node-run for the same node — created by a subsequent cascade event — starts with a fresh policy cursor. That is the right semantic: the operator's retry budget applies to one attempt at the work; if the work is re-attempted later because upstream changed, that's a different attempt with its own retry budget.

## Alternatives

Fresh-row retry (the previous model) — rejected because of the structural incoherence with held coordination described above, and because it pushed runtime state onto the node row.

In-place retry with an in-memory counter (no persistence) — rejected because a supervisor crash mid-retry-sleep would reset the counter, allowing more retries than the policy nominally permits. Persisting the counter on the node-run row gives strict policy-cap accounting under crash recovery.

Retry as a separate state (`retrying`) with its own transitions — rejected because it adds state-machine surface for what is fundamentally a loop on the same row. `running` is the correct state during an executor invocation and `stale` is the correct state during an acquire attempt; whether the operation is the first attempt or the Nth retry is policy-cursor state, not lifecycle state.

Two parallel retry mechanisms — one for acquire (fresh-row, via `OnError`) and one for executor (in-place, via `applyErrorPolicy`) — rejected as the structure that existed before this decision unified them. The split was historical (`OnError` predated `applyErrorPolicy` and was not migrated when the in-place machinery landed for executor errors). It forced two code paths with subtly different transaction shapes and two test surfaces; the acquire side had no retry counter at all and would have produced an unbounded retry budget if its retry branch had ever been implemented correctly. Collapsing to one mechanism removes that surface entirely.
