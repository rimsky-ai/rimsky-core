---
audit: topology-test-coverage
artifact: decision:topology-test-coverage
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:09:41Z
---

# Both supported deployment topologies carry a booted integration proof

Supported. The services integration harness carries a booted proof for each of the two supported deployment shapes the decision names. The all-in-one shape has two: one boots the single-container image on SQLite and drives a one-node template to a fresh terminal, and one boots it on the memory blob backend and additionally asserts the process table holds exactly one rimsky process with no spawned role children. The split shape has one: it boots Postgres plus three separate containers commanded as control-api, scheduler, and supervisor on a shared network against that one database, then deploys and drives the same one-node stub-executor template through the same deploy/create/poll helpers to the same fresh terminal. The split entry point refuses the three configurations that would silently degrade the topology into something else — SQLite, the memory blob backend, and host-port tunnelling — so a split proof cannot quietly become an all-in-one one. Both proofs run unconditionally, neither is skipped, and both resolve their images by source-tree tag rather than a mutable one.
