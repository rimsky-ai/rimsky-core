---
audit: auth-api-key-bearer
artifact: decision:auth-api-key-bearer
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:24:31Z
---

# Api-key presented as an HTTP bearer token, with grants read from the database per request

Supported. The control API's identity-resolver middleware reads the `Authorization` header, requires the `Bearer ` scheme, validates and hashes the presented plaintext, and looks the key up by hash; a missing scheme, a malformed key, an unknown hash, a revoked key, or an expired key each yield a distinct denial reason rather than an identity. The permissions the request is then gated on come off the looked-up key row on every request, which is exactly the property the decision's rationale rests on when it rejects claims-in-token. Checked every token-bearing caller in the tree — the CLI client, the host-agent proxy's registration and dispatch paths, and the two test harnesses — and all present the api-key plaintext as `Bearer`; no signed-token, JWT, OAuth, or session-cookie machinery exists anywhere, and the only key-minting sites are the create-key and rotate-key handlers. The middleware suite covers the scheme, the denial reasons, expiry, revocation, and rotation.
