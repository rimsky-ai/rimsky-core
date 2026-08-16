---
audit: hard-dep-settled-guard
artifact: decision:hard-dep-settled-guard
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:16:18Z
---

# The upstream-refresh pull skips upstreams already settled in the frame

Supported. Inside the upstream pull, an upstream with no in-flight run is probed for an existing run row in the sender's frame, and when one is found the pull skips that upstream entirely — creating no new pending run and inserting no fresh wait-set row, which is what stops the re-seeding. The guard sits behind the in-flight probe, so a still-running upstream never reaches it and a just-woken upstream is in-flight by then; both keep their wait-set binding and the rendezvous is untouched. The regression pin the decision names exists and is exactly the two-upstream shape: a receiver with two forced-refresh upstreams, one of them deliberately slow, driven through two frames. It asserts both halves — that every node type dispatches exactly once in the acceptance frame, with a stability wait whose failure message names mutual re-seeding, and that the receiver still ends the frame holding both upstreams' second-fire values and dispatches after the slow upstream settles. Two further single-upstream pull scenarios cover the ordinary path from both a sender terminal and a direct receiver invalidation.
