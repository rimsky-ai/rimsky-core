---
audit: plumb-validation-roles
artifact: decision:plumb-validation-roles
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:34:05Z
---

# Declared validation roles honored identically across every peer kind that can declare them

Supported. Three capability surfaces in the protocol set carry a validation-supported-roles field — the claim-producer capabilities response, the executor-observability capabilities response, and the publisher capabilities response — and the validation-registry dial walks exactly those three peer kinds, building one peer list from the producer, executor and publisher configuration blocks. Each entry carries its own role fetcher hitting its own capability surface, and the branch that acts on a declared validation protocol is shared: whichever kind the peer is, its fetched role list is what the validation client is registered with, and a fetch failure aborts the whole dial rather than silently registering a peer with no roles. The two remaining configuration blocks are consistent rather than gaps: a standalone validator has no primary protocol whose capabilities could carry the field, and the data-processing capabilities message declares no such field, so there is nothing for the walk to honor there. A test stands up three stub peers — one per kind, each also serving the validation protocol — dials them through the real registry builder, and asserts all three land as validation clients with their declared roles.
