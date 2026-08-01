---
issue: concept-rimsky-enumerates-cli-verbs
kind: audit
category: other
artifacts:
  - concept:rimsky
status: repaired
opened: 2026-07-25T03:18:31Z
---

Question: do the rimsky CLI concept's per-surface verb lists (all seven authentication verbs, all three host-agent verbs, etc.) violate the house rule against concepts enumerating command-line-flag/verb instances, given the doc's own disclaimer that the lists are merely "illustrative"?

Rule that determined the fix: `SELF-CONTAINMENT-RULE` names "command-line flags" among the forbidden concept-body enumerations; the file's own Boundaries/Capability-surfaces text already states "the CLI code and its operator-facing reference are authoritative for exact verbs and flags," so the near-exhaustive lists are redundant by the document's own admission — there is no reading under which the rule permits keeping them, only a question of how much illustrative detail survives. The corpus's established idiom for this shape (`concept:signal`, `concept:transition-reason`: "membership ... owned by the code, not enumerated here") determines the target form.

What changed: `.ok-planner/design/concepts/rimsky.md` — every capability-surface bullet (dev-loop, compose, resource, context, authentication, host-agent control) was trimmed from its near-exhaustive verb list to the surface description plus at most one illustrative example (e.g. "the API-key lifecycle, including anonymous-mode bootstrap (e.g. key creation ...)"), and the preceding sentence now reads "membership of each surface's verb set is owned by the CLI code and its operator-facing reference, not enumerated here" in place of the self-contradicting disclaimer. The surfaces themselves (the durable model) were not touched, and both cross-references (`concept:role-template`, `concept:api-key`) were preserved on the authentication bullet.

How verified: re-read the file; no bullet now names more than one verb, and the surface names / cross-references are unchanged from before the edit. Markdown-only change, no build/test impact.
