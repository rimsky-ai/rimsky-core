---
assessment: verifier-http--success-route
subject: story:verifier-http
way: success-route
release: d977250c
outcome: held
warrant: experiment:verifier-http
---
# An external check service that approves lets the node settle fresh

The audit ran the bundled HTTP-callout verifier against a check service, with the node declaring the location to call (`catalog:executor-attribute-keys/verifier-http: url`) and the payload to send (`catalog:executor-attribute-keys/verifier-http: body`). On the approving route the node settled fresh, recorded the status the service returned, and the service received the exact payload the template declared, verbatim. The author wired an external check into the graph without writing a verifier.

## Unverified remainder

One approving route was exercised. The demonstration does not establish behaviour when the check service is unreachable or answers after a long delay.
