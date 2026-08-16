---
audit: held-commit-cascades-success
artifact: story:held-commit-cascades-success
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:26:27Z
---

# Held work's success reaching downstream only once it has committed

Supported. A stack from this tree ran a template whose acquirer opens a claim on
the bundled filesystem producer, whose co-holder calls an endpoint that holds
every request open until released, and whose watcher sits outside the holding
subgraph subscribed to the acquirer's success; the endpoint reporting the arrival
of the co-holder's request is the synchronisation point, so nothing waits on a
clock. At that provisional moment the acquirer's run was held, it had emitted no
success signal, and the watcher had no run at all. After the release the claim
resolved with a single commit, the acquirer emitted exactly one success at the
next sequence number after that commit, and the watcher's work started after it.
The subscriber therefore sees the success at commit and never at the held moment.
