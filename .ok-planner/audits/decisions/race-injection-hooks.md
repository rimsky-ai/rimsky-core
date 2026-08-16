---
audit: race-injection-hooks
artifact: decision:race-injection-hooks
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:29:03Z
---

# All four defended seams carry a deterministic injection hook and a test that forces the interleaving

Supported. The runtime exposes four injection hooks in production code — a post-commit hook on the claim-acquisition path, a pre-acquire-unavailable hook on the acquire error-policy path, a check-and-fire hook on the auto-terminal aggregate, and a pre-reap hook on the orphan reaper — each a nil-checked callback the runtime invokes at the seam. All four of the seams the decision names have a scenario test that installs its hook to force the precise interleaving and then asserts the hook actually fired rather than assuming it: the acquire-unavailable abandon injection, the two verify-before-run ownership-bail tests routing through the post-commit hook, the held-claim check-and-fire race, and the orphan-reaper versus terminal-release pair covering both orderings. No seam relies on sleeps or repetition budgets, consistent with the project's ban on wall-clock verdicts, and the race detector the decision rejects as an alternative is absent from every build gate.
