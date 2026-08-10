---
audit: http-node
artifact: story:http-node
determination: supported
compliance: compliant
commit: PENDING
audited: 2026-08-10T07:40:00Z
---

# A template author integrates an HTTP upstream without writing an executor

Supported. Against an all-in-one deployment carrying the bundled HTTP-node
executor and a controlled upstream, each of the story's four clauses was driven
and observed. A node naming a URL issued the request and its response body
became the node's output attributes verbatim. A node meeting a 429 emitted one
park tagged `rate_limited` whose resume-at came from the upstream's Retry-After,
then woke by itself, ran a second time and succeeded against the cleared
upstream; a node that lists 429 among its expected statuses did not park at all,
so the parking is the template author's choice. The JSON field that names the
error class was configured twice over — an operator default and a per-node
override, each producing the class it named — and an error body carrying neither
key fell back to `_unspecified`. The template declares no executor of its own.
