---
audit: upstream-pull-on-invalidate
artifact: story:upstream-pull-on-invalidate
determination: supported
compliance: compliant
commit: PENDING
audited: 2026-08-10T05:30:00Z
---

# The subscription flag is what brings the sender current

Supported. Two instances were driven through the control API from templates
identical except for the `force_upstream_refresh` value on one subscription,
whose sender was wired so nothing else in the template could ever run it. With
the flag set, the receiver's invalidation pulled the sender into the same frame,
the sender dispatched exactly once, and the receiver dispatched afterwards on the
value the sender had just produced. With the flag unset, the sender never
dispatched at all. The difference is attributable to the declaration alone, so
the template author expresses the refresh without a second trigger pathway.
