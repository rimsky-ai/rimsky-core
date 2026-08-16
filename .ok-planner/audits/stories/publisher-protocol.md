---
audit: publisher-protocol
artifact: story:publisher-protocol
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T04:58:36Z
---

# A third-party publisher feeds workflows, and its subscriptions survive a restart unreissued

Supported. Measured with a complete third-party publisher built for the run — its
own Go module depending only on the published protocols module — running beside a
released-image stack whose only knowledge of it is one declared publisher entry.
Twenty-three checks, none failing. Its advertisement gated the catalog: a template
naming a kind it never advertised was rejected, one naming the advertised kind
registered and deployed. Creating an instance called Subscribe exactly once, with
the subscription carrying the instance, the kind, the message type and the
resolved config. The message the publisher then posted woke the subscribing node
and was recorded as coming from that publisher with publisher sender kind rather
than an operator, carrying the publisher's own payload. Restarting the stack
re-issued nothing: it asked the publisher what it already held, after which
Subscribe was still at one call, the same subscription id was still held, and
Unsubscribe had not been called — and the next message landed the same way.
Terminating the instance released the subscription with exactly one Unsubscribe,
leaving the publisher holding none.
