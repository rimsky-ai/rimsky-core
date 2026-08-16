---
assessment: service-enrollment--renewal-without-operator
subject: story:service-enrollment
way: renewal-without-operator
release: d977250c
outcome: held
warrant: experiment:service-enrollment
---
# The service keeps its credentials current without the operator touching anything

The audit showed issuance repeating from the same key with no operator action: a second enrollment on that key returned a different certificate, and the issued credential expires in about 23 hours, so it must be renewed to keep working. Restarting the service with the operator touching nothing brought it back serving a credential from the same issuer. The operator therefore manages the key, not the certificates behind it.

## Unverified remainder

What the run exercises is the issuance a renewal performs, not the elapsing of a live certificate's deadline that triggers it — the wait itself was not exercised at this release.
