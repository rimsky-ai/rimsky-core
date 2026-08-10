---
audit: tag-management
artifact: story:tag-management
determination: supported
compliance: compliant
commit: PENDING
audited: 2026-08-10T05:20:00Z
---

# Create, resolve, re-point and remove a movable name over a template hash

Supported. Against a zero-config all-in-one deployment holding two templates that
differ by one check and so hash differently, all 4 tag operations the story names
answered: `tag create` bound a name to the first hash, `tag list` and `tag get`
reported and resolved the binding, `tag mv` re-pointed the name at the second
hash and then back to the first, and `tag rm` removed it — after which the name
no longer resolved as a template ref. Rolling forward and back is what the moves
demonstrate, and the non-disruption clause was measured directly: an instance
created through the tag before a move still reported the hash it was created from
and carried no termination stamp afterwards, and instances created under a
removed tag kept running.
