---
audit: fanout-any-substitution-source
artifact: story:fanout-any-substitution-source
determination: supported
compliance: compliant
commit: PENDING
audited: 2026-08-10T07:40:00Z
---

# A fan-out partition request substituted from each of the four sources the story names

Supported. Against an all-in-one deployment with the bundled filesystem claim
producer configured, four templates declared the same fan-out node and differed
only in where the partition request read from. All four of the sources the story
names resolved and partitioned the claim: an upstream node's attribute produced
the three partitions that node had written, an instance param produced its two,
the claim's own payload produced two keys built from the folder name the
producer supplied, and a typed message's body produced its three. No run
recorded a resolution error, and in each case the number of work units that
reported a partition key equalled the number of partitions the source named.
