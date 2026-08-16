---
audit: asset-management
artifact: story:asset-management
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:10:09Z
---

# An operator lists, versions, audits, traces and retires an instance's data assets

Supported. Driven through the public surface — the shipped CLI and the control
API — against a released-image stack wired to a claim producer built for the run
that advertises data processing and mints a version on each commit, on a template
whose node opens a durable claim and whose downstream node reads that node's
output. Ten checks, none failing. All six capabilities the story names answered:
the durable claim appeared as exactly one asset in the listing; its detail
carried the version the producer minted, its committed state and its durable
lifetime; the version history came back as the producer's own record with commit
time and producer metadata; the materialization audit returned the claim's
terminal records, every row of that kind; the lineage walked backward from the
asset and forward from its materializing run, both reaching the downstream leaf
run that consumed it, which is the "what consumed this before I retire it" the
story asks for; and retiring the asset succeeded, after which it no longer
appeared in the listing.
