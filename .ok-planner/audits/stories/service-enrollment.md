---
audit: service-enrollment
artifact: story:service-enrollment
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:52:00Z
---

# One api-key is the whole credential story for a standing service

Supported. A deployment under mutual-TLS peer auth was locked down with an admin
key, and a standing third-party service was given one key and nothing else — its
grant read back as the enrollment action alone, and it was refused instances,
key minting and the audit log. Holding only that key, the service came up
serving a certificate issued by the deployment CA whose subject is the id of the
key it enrolled with, its listener refused a caller presenting no certificate,
and the deployment drove a node through it to a fresh outcome, so the credential
it obtained at startup is the one carrying real work. Issuance repeats from the
same key with no operator action: a second enrollment returned a different
certificate, the issued credential expires in about 23 hours so it must be
renewed to keep working, and a restart with the operator touching nothing
brought the service back serving a credential from the same issuer. Revoking the
one key stopped issuance — the enrollment route answered the revoked key 401,
and a service restarted on it exited fail-closed rather than serving without
credentials. What the run exercises is the issuance the renewal performs, not
the elapsing of a live certificate's deadline that triggers it.
