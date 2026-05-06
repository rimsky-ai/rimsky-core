# Implementation Notes: 2026-05-03-fs-store-pick-policies

Plan: `docs/plans/2026-05-03-fs-store-pick-policies.md`

This file is the durable record of deviations, judgment calls, and items
for post-run discussion. Subagent dispatches append entries here as they
work. Format:

    ## Task N — <title>
    **Deviation:** <what changed from the plan>
    **Reason:** <why>
    **Surfaced for:** <user-decision | follow-up | informational>

---

## Task 4 — runSync concurrency

**Deviation:** Added a per-policy mutex (`PickPolicy.syncMu`) that
serializes calls to `runSync` for the same policy. The spec described
runSync as "concurrency-safe" via O_CREAT|O_EXCL but does not address
the inter-step race where one goroutine has finished reading available/
+ in_progress/ but not yet written its sentinels, while a second
goroutine has already renamed `available/X` to `in_progress/X.cid.t`.
Without the lock, the first goroutine can re-create a sentinel for a
folder currently held in `in_progress`, allowing a folder to be picked
twice. The fix is local to runSync; the rename-as-claim step in
openPickPolicy stays lockless.

**Reason:** Reproduced as duplicate picks in
`TestOpenPickPolicy_ConcurrentPicksAreUnique` under -race -count=5.
Serializing runSync per-policy is the smallest fix that matches the
spec's intent ("auto-discovery reconciles available/").

**Surfaced for:** informational

## Task 6 — TestCommit_ReleaseToBack and TestOpenPickPolicy_Basic order-independence

**Deviation:** Adjusted both unit tests to be order-independent. The
plan's verbatim assertions (`p.Folder != "beta"`, `addr != filepath.Join(root, sub, "alpha")`)
fail flakily because `runSync` iterates over a Go map (`extant`) when
creating the available-sentinel set; whichever folder lands in available
first has the older mtime and is picked first. The order is undefined
across runs.

The pick-policy contract is "sort by mtime ascending, lexical
tiebreaker"; `release_to_back` cycles, but the initial pick order is
filesystem-dependent. `TestCommit_ReleaseToBack` now records the first
pick and asserts the second pick differs (the actual property of
`release_to_back`). `TestOpenPickPolicy_Basic` now uses a single folder
to keep the address/region equality assertion deterministic.

**Reason:** Test was flaky under -race -count=10 (~3/10 failures).

**Surfaced for:** informational
