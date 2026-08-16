---
assessment: validation-author--claim-producer-and-subscriber-contexts
subject: story:validation-author
way: claim-producer-and-subscriber-contexts
release: d977250c
outcome: unverified
warrant: none
---
# A validator called for the claim-producer and lifecycle-subscriber role contexts

The story promises the validator is called with the context of whichever role the service plays. Of the role contexts it names, this release's measurement exercised the executor context — the finding's path showed the service had been handed that context. The claim-producer and lifecycle-subscriber contexts were not exercised at this release.

## Unverified remainder

Nothing here is demonstrated: a service author writing a validator for a claim producer or a lifecycle subscriber has no measured warrant at this release that their validator is called, or called with that role's context. The executor context is covered by this story's other ways, and the publisher context by the assessments of story:validation-mixin-uniform.
