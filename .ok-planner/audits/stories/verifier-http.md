---
audit: verifier-http
artifact: story:verifier-http
determination: supported
compliance: noncompliant
commit: PENDING
audited: 2026-08-10T07:40:00Z
---

# A node's outcome routed by an external check service's verdict

Supported. A check service run beside a zero-config all-in-one deployment
answered all 3 status classes the story names, and the bundled HTTP-callout
verifier routed each one: the 2xx answer settled the node fresh and recorded
the status the service returned, the 4xx answer settled it failed with the
terminal error class carrying the class the service named in its body, and the
5xx answer settled it failed with that service's other class. The payload the
template declared arrived at the check service byte-identical, and no custom
verifier was written.

## Compliance

The body prescribes wire mechanism: it names the HTTP method, the URL as the
thing configured, and the status-code families with their mapping to node
outcomes. A story owes the need, not the transport that satisfies it.
Compliant text: "As a template author who validates claim outputs with an
external check service, I can point a verifier node at that service and have
its verdict decide the node's outcome, with the service's own failure class
carried into the node's error, so that I validate claim outputs against an
external service without writing a custom verifier."
