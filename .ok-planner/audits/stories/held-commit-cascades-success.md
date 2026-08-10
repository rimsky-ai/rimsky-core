---
audit: held-commit-cascades-success
artifact: story:held-commit-cascades-success
determination: supported
compliance: compliant
commit: PENDING
audited: 2026-08-10T07:40:00Z
---

# A downstream subscriber sees held work's success only after the commit

Supported. Against an all-in-one deployment with a filesystem-backed claim
producer, one node opened a claim and a co-holder of that claim called an
endpoint that held its request open, which is the point at which the run was
inspected rather than any elapsed time. At that provisional held moment the
acquirer's run was in state held, the acquirer had emitted no success signal,
and the non-member downstream subscriber had no run at all. Once the endpoint
released, the claim resolved with one commit, the acquirer emitted exactly one
success signal at the next sequence number, and the subscriber's run started
after that commit.
