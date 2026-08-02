---
audit: spec-jcs-canonicalization
artifact: decision:spec-jcs-canonicalization
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:38:14Z
---

# Template-spec hash canonicalizes via RFC 8785 JCS

Supported. `lib/graph/template/canonical/jcs.go::CanonicalSpecBytes` and `CanonicalSpecHash` marshal the spec to JSON and transform it through `github.com/cyberphone/json-canonicalization`'s `jsoncanonicalizer.Transform` (an RFC 8785-compliant implementation) before SHA-256 hashing, rather than hashing submitted bytes verbatim or a bespoke sorted-key marshal. `lib/graph/template/canonical/jcs_test.go` directly tests the claimed determinism properties: key-order invariance, whitespace invariance, the `sha256-` prefix, distinct hashes for distinct specs, and (via `TestJCSLib_*`) the underlying library's whitespace and number-normalization behavior against the RFC's semantics.
