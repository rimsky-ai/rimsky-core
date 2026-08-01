---
issue: conflict-cancel-siblings-toc-vs-intrinsic
kind: audit
category: conflicting
artifacts:
  - concept:cancel-siblings
  - concept:fan-out
status: repaired
opened: 2026-07-25T21:11:30Z
---

# Is cancel-siblings an opt-in flag or intrinsic to the strict aggregation policy?

Intrinsic — `concept:cancel-siblings`'s own body already says so ("not a separate, configurable behavior — it is intrinsic to choosing strict"), `concept:fan-out` agrees ("strict and first both force-cancel every remaining in-flight clone... unconditionally"), and a grep of the template spec grammar (`lib/foundation/spec`) confirms no `cancel_siblings`-shaped field exists anywhere for an index entry describing "a boolean field" to be true of. Only the `concepts.md` index line was stale, describing it as "a boolean field... that turns on proactive sibling cancellation."

The rules determine the fix and it changes no commitment: the concept body and its sibling concept already state — and the code already implements — the intrinsic semantics; only the index's one-line summary needed to match. Repaired as a stale-TOC-line correction, the canonical mechanical example in the mechanical-vs-judgment rule.

Changed `.ok-planner/design/concepts.md`: the `cancel-siblings` index entry now reads "The proactive sibling cancellation intrinsic to the `strict` aggregation policy: when one sub-claim resolves to Abandon under a parent whose policy is strict, the runtime unconditionally walks the parent's other in-flight sub-claims and force-Abandons each via recursive claim-handle terminal-resolution calls," dropping the "boolean field" framing.

Verified via code reading only (`grep -rn cancel_siblings lib/foundation/spec` — no hits); docs-only change, no build/test impact.
