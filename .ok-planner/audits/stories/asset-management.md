---
audit: asset-management
artifact: story:asset-management
determination: supported
compliance: compliant
commit: PENDING
audited: 2026-08-10T07:40:00Z
---

# Listing, versioning, auditing, tracing and retiring an instance's data assets

Supported. Against an all-in-one deployment wired to a claim producer that
advertises data processing and mints a version id at Commit, an instance opened
one durable claim and a downstream node read that node's output. All 6
operations the story names were then driven from the operator's side and each
returned what the story promises: the listing carried exactly the one asset
under its node-type-and-alias name, the detail carried the version id the
producer minted, the version history came back from the producer with its commit
time and metadata, the materialization audit returned the claim's terminal
record, the forward lineage walk from the asset's materializing run named the
downstream run that consumed it, and the retire call succeeded and emptied the
listing. The story's benefit clause also mentions re-materializing; only the
retire branch was driven, and the understanding the clause conditions both
branches on — which downstream work consumed the asset — was obtained before it.
