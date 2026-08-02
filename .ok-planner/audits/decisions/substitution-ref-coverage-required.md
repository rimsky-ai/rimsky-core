---
audit: substitution-ref-coverage-required
artifact: decision:substitution-ref-coverage-required
determination: supported
commit: b767a27d
audited: 2026-08-02T09:32:02Z
---

# Every substitution ref must be matched by a covering subscription entry, or registration rejects

Supported. `validateSubstitutionRefCoverage` builds a per-receiver index of declared `subscribes:` entries and checks it against every extracted `nodes.` ref (per-field and whole-pull, via `coverageMatch`'s `attribute`/`attribute/*` cases) and every `messages.` ref (via the `message`/`terminal/*` case), rejecting any ref with no matching entry as a structured `substitution_ref_uncovered` registration error rather than silently generating an edge. The refs checked are drawn from all four surfaces a template author can place a directive on (attribute schema, claim-producer selector, lock name, fan-out partition request), scanned by the same `parseSubstitutionRefsFromAttributes` function used elsewhere in the validator. Both the per-field and whole-pull attribute cases and the message case are exercised by passing and failing registration tests (`TestCoverageCheck_MessagesUndeclaredRejected`/`Accepted`, `TestRegistrationRejectsUncoveredSubstitution`'s two subtests).
