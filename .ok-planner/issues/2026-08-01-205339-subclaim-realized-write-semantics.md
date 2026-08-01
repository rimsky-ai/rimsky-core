---
issue: subclaim-realized-write-semantics
kind: human
category: conflicting
artifacts:
  - concept:claim
  - concept:claim-tree
status: verified
opened: 2026-08-01T20:53:39Z
---

# Do sub-claims inherit the parent's realized write semantics, or deliberately carry none?

A claim in rimsky is a held right to work against some scope of data, and each claim row records its *realized write semantics* — the concurrency mode the producer actually granted, which is what the coexistence check consults when a later acquisition's scope overlaps an active holder's. A fan-out splits a parent claim into sub-claims (one per branch), and the design-intent record from that work says a sub-claim inherits both the parent's declared intent and its realized write semantics at insert — one principle, "a sub-claim is a claim."

The code honors only half. Sub-claim rows get the parent's intent stamped at insert (`code:lib/runtime/runner_subclaim.go`) but never a realized-write-semantics value — the only code path that stamps that field is the regular producer-open path, which sub-claims bypass (they are created by splitting, not by opening). The consequence is live and observable: while any fan-out sub-claim is active, an outside acquisition whose scope conflicts with it never reaches the coexistence check at all — it fails hard with "claim handle … has no realized write-semantics yet (holder open still in flight)" (`code:lib/runtime/runner_acquire_claims.go`). That error is also a lie in this case: no open is in flight; the row simply never got a value.

The live corpus cannot arbitrate. The claim-tree concept commits to intent-and-lifetime inheritance explicitly but says nothing about realized write semantics, and neither does the claim or write-semantics concept. So either the empty column is drift from recorded intent, or the loud failure is a deliberate "no outside coexistence while a fan-out is in flight" guard that nobody wrote down.

## Options

- Restore the recorded intent: stamp the parent's realized write semantics onto every sub-claim row at insert, so conflicting outside candidates get a normal coexistence evaluation; record the inheritance in the claim-tree concept. Cost: a behavior change — outside acquisitions that today fail loudly during a fan-out may now be granted coexistence.
- Ratify the code: sub-claims deliberately carry no realized value and conflicting outside candidates fail while a fan-out is in flight; record that shape instead, and fix the error message to say what it means. Cost: adopts as design a blanket exclusion that the evidence suggests nobody chose, and that the coexistence machinery was built to make unnecessary.

The ruling decides whether sub-claim rows inherit the parent's realized write semantics or the fan-out-blocks-outsiders behavior becomes the commitment.

## Ruling

> Recommended ruling (/verify-issues): Restore the inheritance —
> stamp the parent's realized write semantics onto sub-claim rows at
> insert, and record in the claim-tree concept that a sub-claim
> inherits intent, lifetime, and realized write semantics alike.
>
> Rationale: the corpus's one stated principle here is that a
> sub-claim is a claim, and the live claim-tree concept already
> commits to inheritance for the two neighboring properties — the
> empty column has every mark of drift (sub-claims bypass the one
> stamping site) rather than choice, and the misleading
> open-in-flight error is the tell: a designed guard would name
> itself. Ratifying the code (the second option) would enshrine a
> blanket coexistence exclusion the write-semantics machinery exists
> to avoid. Flip case: if fan-out branches genuinely must not share
> scope with outside holders — e.g. a producer whose split-scope
> staging can't tolerate concurrent outside writers — then the
> exclusion is real design, and it should be ratified with an honest
> error and its own invariant.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
