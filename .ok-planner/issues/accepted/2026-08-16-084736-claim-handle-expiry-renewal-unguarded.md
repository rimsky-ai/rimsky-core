---
issue: claim-handle-expiry-renewal-unguarded
kind: audit
category: conflicting
artifacts:
  - concept:claim-handle
  - concept:auto-terminal
status: verified
opened: 2026-08-16T08:47:36Z
---

# One claim-handle write is not guarded by the holding supervisor — the expiry renewal

Every mutation of an active claim-handle row (the ledger row that says which supervisor holds a claim, until when) is supposed to carry the holding supervisor in its predicate, so a supervisor that lost the claim cannot touch it; the concept says there is no carve-out, and a conformance suite proves the guard on every mutator it knows about. One mutator escapes it: the expiry renewal that runs on keepalive and on acquire-reuse updates active rows keyed only by node-run, with no supervisor predicate, in both backends, and it is absent from the conformance suite. A sibling concept (auto-terminal) already asserts liveness-extend is guarded — the corpus believes the guard exists. The ruling decides that it does.

Why it matters: the orphan reaper reclaims rows past their expiry. A supervisor that no longer holds a claim (reassigned, or a stale process) can keep extending the lease that now belongs to another holder, and the reaper never reclaims it — the exact failure the guard exists to prevent everywhere else. Declaring an exception is not available: extending another holder's lease is not harmless.

## Options

- Add the holding-supervisor predicate to the renewal in both backends, thread the acting supervisor through its two callers, and add the operation to the claimant-guard conformance suite; cost: a small change to two call sites and one suite.

The ruling decides the guard is added; the concept and its sibling already say it exists.

## Ruling

> Generated ruling (/verify-issues): Guard the expiry renewal like every other active-row mutation — the update names the holding supervisor in its predicate in both backends, its two callers pass the acting supervisor, and the claimant-guard conformance suite covers it — and add "expiry" to the concept's list of guarded fields. The concept's own "no carve-out" invariant and the auto-terminal concept's assertion that liveness-extend is guarded force it; the reaper-defeat consequence rules out an exception. Verified against the tree as it stands; nothing was applied.

<!-- Owner: this is a generated ruling, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
