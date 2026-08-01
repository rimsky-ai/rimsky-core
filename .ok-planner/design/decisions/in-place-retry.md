---
decision: in-place-retry
status: as-is
aliases: []
---

# Executor and acquire retries are both in-place on the existing node-run row

## Choice

When the per-error-class policy chain resolves to retry, the supervisor loops in-process on the SAME node-run row: it sleeps the policy's retry delay and re-attempts the failed operation against the same dispatch context — same persisted attribute bag, same dispatch id. No state transition fires for a retry and no new node-run row is created; the row stays in its pre-error state across the loop. The rule applies uniformly to both failure surfaces — executor errors (post-dispatch handler errors, row still `running`) and acquire errors (pre-dispatch acquire failures, row still `stale`) — through one shared error-policy mechanism. A single persisted retry counter on the node-run row (per `concept:node-run`) advances on any retry regardless of error class, and a single node-level max-retries budget plus retry-backoff delay bounds the total across every class — no per-class budget, no cursor reset on a class change. When the chain resolves to give-up or pass instead, the runner exits the loop and the row transitions normally to failed or fresh.

## Rationale

Fresh-row retry — transition the failing row to failed, create a new stale row with a back-pointer, copy scratch forward — is structurally incompatible with held-claim coordination on the executor side: a held holder's claim is acquired by the held node-run, so a replacement row cannot re-acquire the claim the failed row still owns, forcing a special-case held-abort path that suppresses the operator's retry policy. In-place retry needs no carve-out: the claim stays held by the same node-run across iterations, sibling holders stay held, and if retries exhaust the budget the give-up path poisons the claim and auto-terminal abandons the coordinated unit through its own machinery. Fresh-row retry also forces the policy-evaluation cursor onto the node row so it can survive the row-to-row hop, violating "nodes carry no runtime state"; in-place keeps the cursor on the node-run row where it conceptually belongs.

The acquire surface shares the executor surface's mechanism for symmetry: held coordination does not arise pre-acquire, but one mechanism for both retry classes collapses the runtime to a single error-policy code path and removes the class of drift bugs two parallel pathways invite. The dispatch row is the unit of retry for both surfaces — it stays in the queue across iterations, claimed by the same supervisor. A new node-run for the same node, created by a later cascade event, starts with a fresh policy cursor: the retry budget applies to one attempt at the work, and a re-attempt because upstream changed is a different attempt with its own budget.

## Alternatives

- Fresh-row retry — rejected: structurally incoherent with held coordination (a replacement row cannot re-acquire the failed row's claim) and pushes runtime state onto the node row.
- In-place retry with an in-memory counter — rejected: a supervisor crash mid-retry-sleep resets the counter, permitting more retries than the policy allows; persisting it on the node-run row keeps strict cap accounting under crash recovery.
- Retry as a separate lifecycle state with its own transitions — rejected: adds state-machine surface for what is a loop on the same row; whether an operation is the first or the Nth attempt is policy-cursor state, not lifecycle state.
- Two parallel retry mechanisms, one per failure surface — rejected: two code paths with subtly different transaction shapes, two test surfaces, and a side that could carry no retry counter at all — an unbounded budget waiting to happen.
