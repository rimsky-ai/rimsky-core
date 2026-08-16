---
audit: tag-management
artifact: story:tag-management
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T04:38:45Z
---

# An operator versions a deployable name and rolls it forward or back

Supported. All four ways the story names — create a movable name, list and
resolve it, re-point it, remove it — plus the benefit clause about in-flight
instances were driven through the public surface against a container of the
released all-in-one image, using two definitions that differ by one bound so
they carry different hashes. Creating the name bound it to the first hash; the
listing carried the name with the hash it points at and resolving returned that
hash; an instance created through the name bound to the tagged hash. Re-pointing
the name at the second hash changed what it resolved to and what a newly created
instance bound to, while the instance created before the move still reported the
hash it was created from and was not terminated; re-pointing back resolved to
the first hash again. Removing the name took it out of the listing and it no
longer resolved as a template reference, while instances created under it kept
running. The body states a role, a capability set and a mandatory benefit, uses
the corpus's own tag vocabulary rather than prescribing mechanism, names no
surface, and carries no history or forward-looking text.
