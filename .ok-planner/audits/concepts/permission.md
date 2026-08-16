---
audit: permission
artifact: concept:permission
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:42:19Z
---

# The per-key grant: closed action grammar, set-membership evaluation, scoping, mode floor, and the canonical registry

Supported. All six invariants hold and each has a unit test naming the scenario. The action grammar is closed to exactly the three wildcard forms the concept names — full, noun-scoped, verb-scoped — and the validator rejects every other shape, including embedded separators or a second asterisk inside either wildcard; the separator is genuinely part of the match boundary, since the noun-scoped matcher tests the prefix including the separator, so a longer noun cannot match. Evaluation is set-membership: the checker walks every entry, allows on any action-matching, in-scope entry, and is order-independent by construction, upgrading a dry-run match to an execute match wherever both exist — covered by a test named for order-irrelevance. A scope-bearing entry allows only requests whose target carries every selector key with a matching value, so an out-of-scope request of the same action is denied unless another entry allows it independently. The matched entry's mode is a ceiling the request flag may lower but never raise: the gate takes the requested mode and pins it to dry-run whenever the grant's mode is dry-run. The parser is forward-compatible in the strict sense claimed — unknown JSON fields are captured into an extras map and re-emitted in sorted order by the custom marshaller, with a byte-stable round-trip test. One registry is canonical for both consumers: the key-creation handler rejects any non-wildcard action the registry does not know with a bad-request response and separately validates the scope dimensions, while the same registry resolves an MCP tool name to its entry for the tool catalog and dispatch. The enrollment verb the Boundaries section calls out is a registered action and gates the enrollment route.
