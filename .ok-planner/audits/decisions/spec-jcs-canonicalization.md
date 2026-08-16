---
audit: spec-jcs-canonicalization
artifact: decision:spec-jcs-canonicalization
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:47:25Z
---

# Whether the template-spec hash is taken over canonical JCS bytes

Supported. One package owns the computation: it marshals the template spec, transforms the result through the canonicalization library that implements the scheme, and hashes those canonical bytes, returning a digest-prefixed identifier. The control API's template surface is the only consumer, using that identifier as the stored template id on every registration path and comparing against it when deciding whether a submitted spec is the same template, so identity really is the hash of canonical bytes rather than of submitted bytes — which is what makes the key-order and whitespace independence the rationale claims hold. A sibling function applies the same canonicalization to map-shaped payloads.
