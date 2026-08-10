---
audit: service-enrollment
artifact: story:service-enrollment
determination: supported
compliance: compliant
commit: PENDING
audited: 2026-08-10T05:45:00Z
---

# One key per standing service, and revoking it ends the service's credentials

Supported. A key minted with a grant of exactly one action — enrollment — could
enroll and was refused all three other surfaces it was tried against. Holding
only that key, a standing service came up serving a certificate issued by the
deployment CA whose subject is that key's id, refused a caller presenting no
certificate, and carried a real dispatch to success. Issuance repeats from the
same key with no operator action: a second enrollment returned a different
certificate, the issued credential expires in about a day so it must be renewed
to keep working, and restarting the service brought it back credentialed with the
operator touching nothing. What the run does not do is elapse a live
certificate's renewal deadline; the issuance the renewal performs was exercised,
the wait that triggers it was not. Revoking the one key ended issuance: the
enrollment route answered the revoked key 401, and a service restarted on it
failed closed rather than serving uncredentialed.
