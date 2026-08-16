---
audit: jcs-cyberphone
artifact: decision:jcs-cyberphone
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:47:25Z
---

# Whether the canonicalization library is permanently pinned and its bytes carry template identity

Supported. Two of the four module manifests require the library and both name the identical pseudo-version; the other two do not require it at all. The permanence is mechanically held rather than merely stated: a freeze test reads every workspace manifest, compares each occurrence against a frozen constant, fails on any drift with a message requiring that a move ship together with a template-identity migration decision, rejects any replace directive that would substitute different output bytes, and fails outright if no manifest requires the library. The load-bearing claim also checks out — the template canonicalization package transforms the marshalled spec through that library and the result is what gets hashed into the template id, so the exact output bytes are what identity rests on. One other call site, the Claude-agent signoff path, uses the same library.
