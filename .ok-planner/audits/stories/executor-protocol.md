---
audit: executor-protocol
artifact: story:executor-protocol
text: noncompliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:10:09Z
---

# A third-party executor plugs into a stack knowing only the protocol

Supported. Measured with a third-party executor built for the run — its own Go
module depending only on the published protocols module — against a released-image
stack whose only knowledge of it is one declared endpoint. Twenty-two checks,
none failing. Discovery worked at startup: the stack reached the peer and carried
back its whole advertisement, both declared error classes, both declared tags and
its expected-attributes schema. Validation then held the templates to that
advertisement on four counts — a property whose type contradicts the executor, a
property its closed schema does not declare, an error class outside its
vocabulary, and a subscription filtering on a tag it never declared. A template
written to the advertisement registered, deployed and ran, and all four settling
outcomes behaved: success settled the node fresh with the peer's attribute delta
on the record, error settled it failed, and park parked it rather than settling
it. Errors routed by the class the peer raised — the give-up class dispatched
once, the retry class once plus its two retries — and the declared tags proved
load-bearing at run time, with one of two tag-filtered subscribers running and
the other never firing.

## Compliance

- The body prescribes the protocol and enumerates its surface — "the unary execute verb plus the executor-observability handshake (capabilities, declared error classes, declared tags, attribute-schema advertising)" names a protocol and lists its parts, which decisions own; the compliant capability is that the author's own service is discovered, dispatched to, and has its declarations honoured.
- The body enumerates the runtime's obligations as a mechanism list — discover, validate, dispatch, accept outcomes, route errors describes how the product delivers rather than what the author gets; the compliant benefit already says it: a custom executor plugs into a stack without rimsky-internal knowledge.
