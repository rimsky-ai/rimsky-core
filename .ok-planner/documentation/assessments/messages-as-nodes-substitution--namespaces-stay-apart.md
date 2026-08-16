---
assessment: messages-as-nodes-substitution--namespaces-stay-apart
subject: story:messages-as-nodes-substitution
way: namespaces-stay-apart
release: d977250c
outcome: held
warrant: experiment:messages-as-nodes-substitution
---
# A mistyped reference of either form is refused before the template is usable

One channel is only a benefit if the two namespaces cannot be confused, so the audit checked where a leak is most likely: at registration. Five templates were driven through `catalog:http-routes/POST /v1/templates`, and the namespaces stayed apart in both directions — a message-form reference naming an undeclared message type was refused, a node-form reference naming a message type was refused as an unknown node, and an uncovered reference of either form drew the same coverage finding naming the subscription entry to add. The author therefore gets one diagnostic vocabulary across both forms, and a mistake is caught before the template can be deployed.

## Unverified remainder

The two namespaces do not converge on the subscription side: an attribute-changed edge declared on a message type is refused, because delivery of a message only ever manifests as a success terminal. An author who expects a message type to behave as a node in every respect will find that one exception.
