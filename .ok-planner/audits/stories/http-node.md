---
audit: http-node
artifact: story:http-node
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:28:52Z
---

# The bundled http-node executor covers issue/route/park/error-class integration with an upstream HTTP API

Supported. `lib/services/executors/http-node` issues the configured request and, on success, decodes the JSON response body into the node's `attributes_delta` (exercised by `TestExecute_HappyPath_200JSON` and related happy-path tests); a `429` response parks the node with `resume_at` derived from the upstream's `Retry-After` header (integer-seconds, HTTP-date, and malformed/absent fallback all handled), proven end-to-end by `TestHttpNode_429ParksWithResumeAtAndAutoWakes`; and the JSON field naming the upstream's error class is configurable per-node (`error_class_field` attribute, else `RIMSKY_EXECUTOR_HTTP_NODE_ERROR_CLASS_FIELD`, else the `error_class` default) with a stable `_unspecified` fallback when the body lacks that field, proven by `TestHttpNode_ConfigurableErrorClassFieldAndUnspecifiedFallback`. No operator-written executor code is required — the handler is a bundled, config-driven service.
