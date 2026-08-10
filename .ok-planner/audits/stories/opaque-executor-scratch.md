---
audit: opaque-executor-scratch
artifact: story:opaque-executor-scratch
determination: supported
compliance: compliant
commit: PENDING
audited: 2026-08-10T07:40:00Z
---

# Executor-attached bytes come back on the next dispatch of the same node-run

Supported. A third-party executor written against the public executor protocol
and registered by configuration attached non-UTF-8 byte strings to settling
outcomes, and read back on the next dispatch a byte string of the same length
whose digest matched, on all three re-dispatch paths of the same node-run the
runtime has: a park and its time-wake resume, an error routed to in-place retry,
and a quiet dispatch reaped and recovered under `max_quiet_period`. Each pair
carried one node-run id, so the bytes crossed a recovery rather than a new run.
A first dispatch with no predecessor carried no scratch. The park's audit record
carries the byte count and a spilled flag and not the bytes, and no record
rimsky writes for itself carries any of the three strings in base64, hex or raw
form.
