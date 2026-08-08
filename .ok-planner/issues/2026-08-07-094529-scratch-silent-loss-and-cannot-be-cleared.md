---
issue: scratch-silent-loss-and-cannot-be-cleared
kind: human
category: bug
artifacts:
  - decision:scratch-protocol
  - decision:scratch-recovery
  - story:opaque-executor-scratch
status: promoted
sprint: 2026-08-08-ruled-intake-drain.md
opened: 2026-08-07T09:45:29Z
github: https://github.com/rimsky-ai/rimsky-core/issues/89
---

# When rimsky cannot read back an executor's saved state, it hands over an empty one

An executor that pauses mid-job and resumes later saves its own working state
into a blob rimsky calls **scratch**. rimsky stores it, opaquely, and hands it
back on the next dispatch. From the executor's side that round trip is the whole
mechanism: what comes back is what it saved.

Except when rimsky cannot fetch it. Four separate failure paths in the loader
(`lib/runtime/runner_acquire_helpers.go::loadScratchIntoAcquisition`) log a
warning and then dispatch with **empty** scratch: the database read failed, the
scratch was spilled to blob storage but no blob backend is configured, the
configured backend is a different one than the blob was written to, or the blob
read itself failed. Each one returns early, leaving the scratch field at its
zero value.

The executor cannot tell the difference between that and a genuine first
dispatch — an empty scratch is exactly what a first dispatch looks like. So an
executor resuming a half-finished job is told, with full confidence, that it
never started. Whatever it does next it does from scratch: redo the work,
double-charge the external system, or overwrite the partial result. No error
class is raised, no signal is emitted, and the only trace is a log line on the
supervisor, which is the one place the executor's author is not looking.

Two of the four paths are configuration mistakes rather than transient faults —
no backend configured, or a backend swapped out from under existing spilled
blobs — and those will fail for every dispatch that has spilled scratch, not
just an unlucky one. The other two are genuine faults that might clear on their
own.

The corpus promises the round trip and stops there. The story covering opaque
executor scratch commits to the save-and-return behavior; the decisions
governing the scratch protocol and stale-scratch recovery cover the working case
and the recovery-after-crash case. None of them says what rimsky owes an
executor when the state exists but cannot be produced. Elsewhere in the runtime
there is no precedent to copy — this is the only place a blob read failure is
absorbed rather than surfaced.

Two other findings filed under this issue are resolved: the corpus already rules
that writing empty scratch is deliberately a no-op rather than a clear, and that
a co-held claim's handle deliberately carries less than a freshly acquired
one's.

## Options

- **Fail the dispatch** with an error class the template can route on. The
  executor never sees a false empty; costs turning a transient blob hiccup into
  a failed attempt, though the node's own retry policy would then govern it.
- **Distinguish the causes**: fail closed on the two structural cases (no
  backend, wrong backend), keep degrading on the two transient reads. Matches
  severity to cause; costs a split contract that is harder to state than either
  uniform rule.
- **Tell the executor instead of deciding for it** — pass a flag saying the
  scratch was unreadable, so it can choose. Most faithful to the opaque-blob
  design; costs a protocol field and pushes the decision onto every executor
  author.
- **Keep warn-and-degrade** and document it as the contract. Free, and it means
  the round-trip promise silently does not hold under failure.

The ruling decides what an executor is owed when its saved state cannot be read
back.

## Ruling

> Recommended ruling (/verify-issues): fail the dispatch rather than hand an
> executor a false empty state. rimsky knows the state exists — it has the
> handle in the row it just read — so dispatching as though there were nothing
> is asserting something it knows to be untrue, to the one party that cannot
> check. A failed attempt is recoverable and visible; silently re-running a
> half-finished job against an external system may be neither.
>
> Rationale: the round-trip promise is the entire value of the scratch
> mechanism, and every option that keeps degrading preserves the case where the
> promise quietly does not hold. Splitting behavior by cause is defensible on
> severity, but it makes the executor-facing contract conditional on rimsky's
> internal storage topology, which is exactly what scratch's opacity exists to
> hide. Handing the executor a flag is the most honest shape and the one to
> revisit if a real deployment wants to resume-anyway; it is more surface than
> this warrants until someone asks. What would change this call: evidence that
> blob read failures here are common and transient in practice — that would
> favor degrading with a retry ahead of failing, since a hard failure per
> dispatch would then be the more disruptive default.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to
accept — the next /plan-sprint carries it, naming the generated/recommended
batches at sign-off. Edit the text to redirect, empty the section to discuss
live, or delete this note to adopt the ruling as your own. -->
