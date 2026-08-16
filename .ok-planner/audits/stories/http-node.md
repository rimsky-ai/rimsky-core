---
audit: http-node
artifact: story:http-node
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:01:18Z
---

# The bundled HTTP-node executor integrates an upstream without a custom executor

Supported. Driven through the public surface against released-image stacks
running the bundled executor in-process, against a controlled upstream serving a
JSON document, a route that rate-limits once and then clears, and three error
routes whose bodies differ in which key names the class. Eleven checks, none
failing. All four capabilities the story names answered: the fetching node
issued the request and its response body became the node's output attributes
verbatim; the rate-limited node parked instead of failing, tagged as
rate-limited and carrying a resume time derived from the upstream's own
retry-after, then resumed by itself and succeeded against the cleared upstream on
exactly one further run; and the error class came from the operator-configured
field, was overridden by the per-node setting, and fell back to a stable class
when the body named none. Two boundary runs framed it: a node accepting the
rate-limit status as expected did not park and settled successfully, and without
the operator's egress opt-in the same private-address request was refused as a
network error.
