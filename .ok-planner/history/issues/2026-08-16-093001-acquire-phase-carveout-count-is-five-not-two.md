---
issue: acquire-phase-carveout-count-is-five-not-two
kind: audit
category: conflicting
artifacts:
  - decision:acquire-unavailable-carveout
  - concept:terminal-resolution
status: promoted
opened: 2026-08-16T09:30:01Z
sprint: 2026-08-17-accepted-intake-drain.md
---

# Two artifacts undercount the acquire-phase handlers that sit outside the claim-handle resolution engine

When a node-run fails before it holds any claim (acquisition failed), settlement takes a shortcut around the unified claim-handle resolution engine — there is no claimant-guarded delete to fold, so the handler routes through a shared acquire-error path instead. A decision names two such handlers as the carve-out; the terminal-resolution concept calls it "the single carve-out". The dispatcher's error switch routes five handlers through that shared path (unavailable, producer error, nil frame id, fan-out substitution failure, lock-spec substitution failure), and the decision's own rationale applies identically to all five; the two named ones differ only in passing a producer-declared class as the primary lookup key. The ruling brings both artifacts to the count the code has.

## Options

- Rewrite the decision to name the five handlers as the carve-out set (with the producer-class property as what distinguishes the named two) and the concept to reference that count in one place; cost: none.
- Fold the three unnamed handlers into the engine; cost: an architecture change nothing motivates — the five-handler shape is coherent and shares one path.

The ruling corrects the count in both artifacts.

## Ruling

> Generated ruling (/verify-issues): Rewrite the decision's Choice to name all five acquire-phase handlers as the carve-out set sharing one downstream path — the producer-declared-class lookup being what distinguishes the two originally named — and make the terminal-resolution concept's body, table and invariant cite that set (or the decision) rather than "the single carve-out". Forced by the current-state-only rule; the design is coherent, only the count is stale. Verified against the tree as it stands; nothing was applied.

<!-- Owner: this is a generated ruling, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
