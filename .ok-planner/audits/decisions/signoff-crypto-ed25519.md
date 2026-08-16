---
audit: signoff-crypto-ed25519
artifact: decision:signoff-crypto-ed25519
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:43:41Z
---

# Sign-off verification on the standard-library primitive, over a fixed wire contract

Supported. The claude-agent handler verifies each declared sign-off with the Go standard library's ed25519 primitive, checking the signature length before verifying and parsing the operator-declared public key from its PEM form, with a malformed key reported as a configuration error rather than an invalid signature. The signed message is built exactly as the decision describes: a fixed domain constant, then the dispatch id, then the canonicalized value found at the declared path, joined by newlines, with canonicalization done by the JSON canonicalization implementation the project uses for the same job elsewhere. The wire contract is held in place by a committed fixture of cross-implementation vectors — a public key plus two vectors, one an object value exercising key ordering, unicode, escapes, exponent and fractional numbers, and null, the other a scalar — and two tests that rebuild the canonical message, compare it against the recorded canonical form, and verify the recorded signature, which was minted by the retired other-language implementation. Thirteen further tests cover the surrounding semantics: wrong value, wrong key, wrong dispatch id, absent path, root path, multiple paths, and key-order variants.
