---
assessment: publisher-protocol--feed-messages
subject: story:publisher-protocol
way: feed-messages
release: d977250c
outcome: held
warrant: experiment:publisher-protocol
---
# A third-party publisher feeds messages into a workflow

The audit ran a complete third-party publisher — its own module depending only on the published protocols module — beside a released deployment whose only knowledge of it was one declared publisher entry. Its advertisement gates the catalog: a template naming a kind the publisher never advertised was rejected at `catalog:http-routes/POST /v1/templates`, while one naming the advertised kind registered and deployed. Creating an instance through `catalog:http-routes/POST /v1/instances` asked the publisher to subscribe exactly once, and the subscription carried the instance it was for, the kind, the message type and the resolved configuration. The message the publisher then posted woke the subscribing node and was recorded on the instance's bus as coming from that publisher, with publisher sender kind rather than an operator, carrying the publisher's own payload. Twenty-three checks across this way and its siblings, none failing.

## Unverified remainder

One publisher advertising one kind was driven. The way does not enumerate multi-kind publishers or several publishers feeding one instance.
