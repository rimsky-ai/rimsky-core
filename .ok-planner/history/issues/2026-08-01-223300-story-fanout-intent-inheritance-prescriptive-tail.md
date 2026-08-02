---
issue: story-fanout-intent-inheritance-prescriptive-tail
kind: sprint
category: stories-prescriptive
artifacts:
  - story:fanout-intent-inheritance
  - concept:write-semantics
status: repaired
opened: 2026-08-01T22:33:00Z
---

# fanout-intent-inheritance's sentence carries mechanism the concept already owns

Should `story:fanout-intent-inheritance` keep the clause explaining that sub-claim intent inheritance is enforced by the coexistence rule rather than by producer-side branching?

`{{STORY-DEFINITION}}` forbids mechanism prescription in story bodies ("a story body that prescribes mechanism ... has crossed into decision or spec territory and fails compliance"); `{{MECHANICAL-VS-JUDGMENT-RULE}}` treats aligning a stale/redundant sentence in one artifact to a commitment the code and a counterpart artifact already agree on as mechanical. Re-checking the corpus: `concept:claim`'s body already states "Intent's only runtime consumer is the coexistence predicate ... Producers do not branch their own behavior on intent; coexistence is rimsky's layer, not the producer's," and `concept:claim-tree`'s body already states "a sub-claim inherits its parent claim's declared intent, lifetime, and realized write semantics rather than declaring its own." Together those two concepts — not `concept:write-semantics` as the filed Problem guessed, but the same corpus commitment — fully and squarely cover the story's mechanism clause. Trimming it drops no commitment (both concepts still carry it) and only removes a redundant restatement, so the fix is mechanical.

Changed `.ok-planner/design/stories/fanout-intent-inheritance.md`: replaced the three-line body with the canonical `As <role>, I want <capability>, so that <benefit>` form, dropping the "honored by the coexistence rule ... not by the producer branching its own behavior on intent" clause. The story's promise (sub-claims inherit `intent: r` end-to-end, independent of producer-specific behavior) is unchanged.

Verified by re-reading `concept:claim` and `concept:claim-tree` to confirm the mechanism clause is a verbatim-equivalent restatement, and by checking the edited story file still satisfies `{{STORY-TEMPLATE}}`'s shape (mandatory "so that" clause present). No code or test touches this file, so no build/test was applicable.
