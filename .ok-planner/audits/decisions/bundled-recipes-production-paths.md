---
audit: bundled-recipes-production-paths
artifact: decision:bundled-recipes-production-paths
text: compliant
implementation: unsupported
commit: PENDING
audited: 2026-08-16T09:35:00Z
---

# Whether bundled recipes induce their demonstrated behavior through production paths

Unsupported, because the subject is gone: this tree ships no bundled recipes at all. Searching every tracked file for the word finds one hit, in an archived release note describing the park-then-resume recipe and its demo script as living under an examples directory; that directory and its whole module were deleted in a later sprint, and the repo now has no examples tree, no recipe directory, and no park-resume demo script. What remains under the test fixtures are five demo scripts with their templates — onboarding, cascade-send, frame-origin audit, client-context, and a host-agent control-plane walk-through — none of them a park-then-resume recipe and none of them presented as bundled recipes. The production parking machinery the choice points at does exist and is exercised by scenario tests, but no recipe drives it, so there is nothing here for the choice to be true of.
