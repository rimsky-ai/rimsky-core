---
audit: publisher-protocol
artifact: story:publisher-protocol
determination: supported
compliance: compliant
commit: PENDING
audited: 2026-08-10T07:45:00Z
---

# A custom publisher feeds workflows, and a rimsky restart does not re-issue it

Supported. A publisher written as its own module against the protocols module
was wired into a stack by one config entry and behaved as a first-class peer: a
template naming a kind it never advertised was rejected, and one naming its
advertised kind registered. Creating an instance produced exactly one Subscribe
call carrying the instance, kind, message type and resolved config, and the
message the publisher posted woke the subscribing node, recorded as coming from
the publisher with sender kind publisher and the publisher's own payload.
Restarting rimsky did not re-issue anything: the restarted stack asked the
publisher what it already held, after which Subscribe was still at one call, the
same subscription id was still held, and Unsubscribe had not been called — and
the publisher's next message ran the node a second time. Terminating the
instance called Unsubscribe once and left the publisher holding none.
