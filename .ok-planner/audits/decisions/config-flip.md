---
audit: config-flip
artifact: decision:config-flip
text: compliant
implementation: unsupported
commit: PENDING
audited: 2026-08-16T04:47:25Z
---

# Whether check activation still follows clean state, and whether that transition can occur at all

Unsupported, because the transition the choice governs is no longer representable. The lint configuration carries no check-activation key at all: both of the checks the tool ships run unconditionally, and the gating test fails any configuration in which a check-activation key is present and set false. So a project cannot commit the inactive-check state the rule's sequencing starts from — staging a check as inactive while a backlog is swept would fail the suite on the first commit, and there is no third check waiting to be activated. What remains is the state the rule was meant to produce: every check active and the tree clean against both, asserted by that same test. The ordering commitment itself has nothing left to govern and no mechanism to govern it with.
