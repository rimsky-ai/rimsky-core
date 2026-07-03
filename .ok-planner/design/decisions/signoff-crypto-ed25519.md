---
decision: signoff-crypto-ed25519
status: as-is
aliases: []
---

# Sign-off validation uses the standard-library ed25519 primitive

## Choice

The claude-agent handler validates each declared sign-off by verifying an ed25519 signature over the domain-separated accumulated bound output (a fixed domain string, the dispatch id, and the RFC 8785-canonicalized value at the declared path). Signing shape — algorithm, domain, canonicalization — is unchanged from the retired TypeScript implementation to preserve wire compatibility with existing signed outputs, proven by fixed cross-implementation test vectors minted by the retired implementation.

## Rationale

The standard-library primitive matches the TypeScript implementation's semantics; wire-compat means signatures minted before the port keep verifying after it.

## Alternatives

Switch to a different signature algorithm — rejected: breaks wire compat pre-v1 for no gain.
