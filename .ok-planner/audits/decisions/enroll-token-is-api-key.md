---
audit: enroll-token-is-api-key
artifact: decision:enroll-token-is-api-key
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:48:37Z
---

# Enrollment as an api-key exchange for a short-lived certificate, with no second credential type

Supported. There is no second credential type anywhere: sweeping the tree for any join-token or enrollment-token notion returns nothing, and the only table the peer-auth migration adds holds the deployment CA's own material, not credentials. The enrollment route is gated by the enrollment action in the canonical action registry and refuses any caller that is not an authenticated api-key principal, so the standing secret is exactly an api-key carrying that permission. The exchange returns what the decision describes — a leaf certificate, its private key, and the CA root — and the leaf is issued against the caller's key id, which the certificate authority stamps into the certificate as a SPIFFE-style URI subject-alternative name under a fixed trust domain; the verifier extracts the principal back out of that SAN and errors by name when it is absent, so the api-key row really is the service principal. The revocation story holds and needs no revocation list: the maintenance loop re-enrolls over HTTP with the api-key as a bearer token once two thirds of the leaf's life has elapsed, so a revoked key fails the ordinary identity resolver and renewal stops, with the outstanding leaf ageing out inside its day-long default. One qualifier on the rationale's storage claim: rimsky's store holds the CA private key as well as the api-key hashes, but it is AES-encrypted under an operator-supplied environment key that never enters the database, so the claim as the sentence actually frames it — a dumped ledger leaks no usable service secret — holds.
