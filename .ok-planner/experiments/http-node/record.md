---
experiment: http-node
commit: PENDING
---

# The bundled HTTP-node executor against a controlled upstream

## What it runs against

`run.py` serves a small HTTP upstream on the host and boots a
`rimsky-all-in-one` container at `RIMSKY_IMAGE_TAG`, which registers the
bundled `http-node` executor in-process. The upstream is reached at
`host.docker.internal`, so the run sets
`RIMSKY_EXECUTOR_HTTP_NODE_EGRESS_ALLOWLIST` to the private CIDRs and
`RIMSKY_EXECUTOR_HTTP_NODE_ERROR_CLASS_FIELD` to `code`. Templates are
registered and driven through the control API.

The upstream serves five routes: a JSON document, a route that answers 429 with
`Retry-After: 2` on its first call and 200 afterwards, and three 4xx routes
whose bodies differ in which key names the error class. Two further stacks run
the same nodes: one where the node lists 429 in `expect_status`, and one with no
egress allowlist at all.

## What was observed

Eleven checks, none failing. The fetching node's response body became its output
attributes verbatim (`id`, `count`, `nested`). The rate-limited node emitted one
`transient/park` tagged `rate_limited` carrying a `resume_at` derived from the
upstream's `Retry-After`, resumed by itself, ran a second time, and succeeded
against the cleared upstream. The operator-configured error-class field produced
`terminal/error/http/request_invalid/quota_exhausted`; a per-node
`error_class_field` overrode it to `.../bad_shape`; a body naming no class fell
back to `.../_unspecified`. A node listing 429 in `expect_status` did not park
and settled `terminal/success`. Without the egress allowlist the same
private-address request failed `http/network_error`.
