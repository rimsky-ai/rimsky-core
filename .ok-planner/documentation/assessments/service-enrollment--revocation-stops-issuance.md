---
assessment: service-enrollment--revocation-stops-issuance
subject: story:service-enrollment
way: revocation-stops-issuance
release: d977250c
outcome: held
warrant: experiment:service-enrollment
---
# Revoking the one key ends the service's access

The audit revoked the service's single key through `catalog:cli-verbs/rimsky auth revoke` and re-ran the two things that depend on it. The enrollment route answered the revoked key with an authentication failure rather than a certificate, and a service restarted on that key exited fail-closed rather than serving without credentials. Revocation in one place is therefore the whole off switch for a standing service, which is what makes one key per service a complete credential story.

## Unverified remainder

Revocation was measured against future issuance. The demonstration does not establish what happens to a certificate already issued and in use when its key is revoked before that certificate expires.
