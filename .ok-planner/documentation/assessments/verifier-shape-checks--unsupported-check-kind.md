---
assessment: verifier-shape-checks--unsupported-check-kind
subject: story:verifier-shape-checks
way: unsupported-check-kind
release: d977250c
outcome: held
warrant: experiment:verifier-shape-checks
---
# A check kind the verifier does not implement fails loudly

The audit declared a check kind the bundled verifier does not implement. The node failed with `catalog:error-classes/verifier/attribute_invalid` rather than passing silently, so an author who mistypes or invents a check kind learns it from the run instead of believing their data was checked when it was not.

## Unverified remainder

The unsupported kind was caught when the node ran. The demonstration does not establish whether the same mistake is caught earlier, at template registration.
