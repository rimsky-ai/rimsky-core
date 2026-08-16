---
audit: peer-auth
artifact: concept:peer-auth
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:15:12Z
---

# The four-boundary framing and the optional mutual-TLS mechanism: eight invariants

Supported. All eight invariants hold. The deployment switch parses to the unauthenticated mode when absent or empty and rejects any value outside the two, so the default posture costs nothing and every internal dial stays plaintext. There is no principal store but the api-key ledger and no join-token type or table: enrollment is an ordinary gated endpoint that exchanges a grant-bearing key for a leaf. The key crosses the wire at enroll and nowhere after — the service holds the leaf and its private key in memory only, never writing either to disk, and a background maintainer re-enrolls before expiry, so revoking the key stops renewal and the certificate lapses within its day-long lifetime; nothing in the tree implements or consults a revocation list. The CA private key is stored in a dedicated row encrypted with authenticated symmetric encryption under a thirty-two-byte operator-supplied environment key, and config load refuses to return when the mutual-TLS mode is on and that key is unset, non-base64, or the wrong length, with a test asserting the error names the variable. The leaf's identity is a workload URI whose path is the calling key's id, extracted only from a verified chain, and the tree reads it back in three places — the callback server's peer check, the callback server's match against the principal recorded at dispatch, and the executor clients' server-side principal read on both transports. The callback listener requires and verifies a client certificate under the secured mode and refuses to start without a server identity and a client CA pool, so the terminal async return leg is authenticated by peer identity; the correlation id is only a path parameter and authorizes nothing. The two ongoing mid-dispatch channels, keepalive and attribute writeback, each additionally check a per-dispatch bearer token before doing anything, in every posture. Coverage of the secured mode spans the enumerated legs: both dispatch transports, the callback return leg, and the control API's own inbound listener, which a test asserts stops answering plaintext once the mode is on.
