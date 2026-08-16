---
assessment: rules-doc-accuracy--dead-references-absent
subject: story:rules-doc-accuracy
way: dead-references-absent
release: d977250c
outcome: held
warrant: experiment:rules-doc-accuracy
---
# Known-dead references stay out

Alongside resolving what the rules cite, the audit checked a curated set of four references known to be dead, and none of the four appears in the contributor rules at this release. That is the ratchet half of the promise: a reference that has been removed once does not creep back in unnoticed as the rules are edited.

## Unverified remainder

The set checked is the curated four. A reference that dies after this release and is not added to the set would not be caught by this way.
