---
audit: fanout-list-array
artifact: story:fanout-list-array
determination: supported
compliance: compliant
commit: PENDING
audited: 2026-08-10T07:40:00Z
---

# A fan-out node partitions over a list an upstream node produced

Supported. Against an all-in-one deployment whose only claim producer is the one
the image bundles, a template's first node wrote a three-element list as its own
attribute and its second node named that attribute as its partition request. The
producer split the parent claim into three sub-scopes, the fan-out dispatched
three work units keyed by the three list elements, each work unit resolved its
own partition key, and the node's run summary reported four fresh runs — the
parent plus one per item — with no failures. Nothing in the run supplied a claim
producer of its own.
