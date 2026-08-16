---
audit: api-key
artifact: concept:api-key
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:06:31Z
---

# The api-key credential: one-way storage, revoke-not-delete, the active predicate, partial name uniqueness, and the service principal

Supported. All five invariants hold, as does each of the three lifecycle phases. Minting draws 33 bytes from the cryptographic random source, tags the encoded string with a fixed prefix, and stores only its digest; the plaintext appears in the mint response and in the rotation response and is written nowhere else, and the table carries no column for it. The credential table exposes no delete operation at all — revocation sets a timestamp and the row stays, and the audit rows carry the key id that joins back to it. The active predicate is stated once in Go and once in each backend's count query, and the two agree: not revoked, expiry not passed, scheduled revoke not passed; the request middleware applies it on every call and the anonymous-mode count uses the same three clauses. The name uniqueness index is partial in exactly the way claimed — it excludes rows carrying a revocation timestamp or a scheduled rotation-grace revoke, and does not exclude rows whose expiry has merely lapsed — and both storage backends declare it identically. A service principal is an api-key: the enrollment endpoint is gated on a named grant, issues a short-lived leaf whose subject binds the calling key's id, and refuses any caller without a key id, so a revoked key stops renewing and its certificate ages out. Rotation runs the revoke-scheduling and the new insert in one transaction, refuses a revoked key and a key already inside a grace window, and the periodic sweep performs the deferred revocation while emitting an audit event distinguishing it from a manual one. Scenario suites cover the dual-active rotation and sweep, the revoke guard, the audit emissions, and a full lifecycle acceptance walk.
