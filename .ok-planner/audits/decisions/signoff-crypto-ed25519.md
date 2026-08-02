---
audit: signoff-crypto-ed25519
artifact: decision:signoff-crypto-ed25519
determination: supported
commit: b767a27d
audited: 2026-08-02T09:38:14Z
---

# Ed25519 sign-off verification via the Go standard library, with a wire-compatibility contract

Supported. `lib/services/executors/claude-agent/signoff.go` verifies each declared sign-off using `crypto/ed25519` (standard library) over a message built as the fixed domain string `SignoffDomain` (`rimsky/claude-agent/signoff/v1`), the dispatch/node-run id, and the RFC 8785 JCS-canonicalized value at the declared attribute path (`BuildSignoffMessage`/`canonicalizeJSON`, using the same `cyberphone/json-canonicalization` library as template-spec hashing). The signing shape is held in place as a cross-implementation wire contract by fixed test vectors in `lib/services/executors/claude-agent/testdata/signoff-wire-compat.json` (a PEM public key plus precomputed canonical bytes and base64 signatures for both an object value and a scalar value), exercised by `TestVerifyRequiredSignoffsWireCompatObjectValue` and `...ScalarValue`; twelve further unit tests in `signoff_test.go` cover key-order invariance, dispatch-id binding, per-path isolation, malformed-key handling, and rejection on wrong key/value/dispatch id.
