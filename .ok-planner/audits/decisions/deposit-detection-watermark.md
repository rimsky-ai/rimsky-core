---
audit: deposit-detection-watermark
artifact: decision:deposit-detection-watermark
text: compliant
implementation: unsupported
commit: d977250c
audited: 2026-08-16T04:43:41Z
---

# Polling with a durable watermark, and whether a failed publish leaves it unadvanced

Unsupported, on one clause. Everything else holds: the object-store sensor detects deposits by listing the watched location on a per-watch poll interval, keeps a per-subscription watermark and an explicit seen-name set in its own durable state store — two tables, one carrying the watermark name and time, the other the seen names — and derives each publish's idempotency key from the subscription id, the object name, and the content etag joined together, with tests covering distinct objects that share an etag and objects whose etag is empty. The clause that fails is "a failed publish does not advance the watermark". It holds only for transient failures: a transport error or a 5xx returns from the poll before the object is marked seen, and a named test proves the watermark stays put and both objects are delivered once the receiver recovers. A publish the control API rejects permanently — any 4xx outside the retryable carve-out — takes the opposite path: the sensor logs the drop and falls through to mark the object seen and advance the watermark, and a second named test asserts exactly that. That observation is never delivered, so neither the unadvanced-watermark clause nor the at-least-once conclusion the Choice draws from it covers the permanent-rejection case. The dropping behaviour is itself deliberate and is stated as an invariant of the sensor concept; this decision's text simply carries no carve-out for it.
