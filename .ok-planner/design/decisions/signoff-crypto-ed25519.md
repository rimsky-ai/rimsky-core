---
decision: signoff-crypto-ed25519
---

# Sign-off validation uses the standard-library ed25519 primitive

## Choice

The claude-agent handler validates each declared sign-off by verifying an ed25519 signature, via the Go standard-library primitive, over the domain-separated accumulated bound output (a fixed domain string, the dispatch id, and the RFC 8785-canonicalized value at the declared path). The signing shape — algorithm, domain separation, canonicalization — is a wire-compatibility contract: any signature minted over the same bound output by a conforming implementation verifies, and fixed cross-implementation test vectors hold the contract in place.

## Rationale

The standard-library primitive carries exactly the semantics the wire contract requires, with no dependency added; the wire-compatibility contract keeps every previously minted signature verifying.

## Alternatives

- A different signature algorithm — rejected: breaks wire compatibility with existing signed outputs for no gain.
