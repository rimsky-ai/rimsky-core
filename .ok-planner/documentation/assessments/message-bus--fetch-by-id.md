---
assessment: message-bus--fetch-by-id
subject: story:message-bus
way: fetch-by-id
release: d977250c
outcome: held
warrant: experiment:message-bus
---
# Retrieving one message by its id

The audit fetched a single message through `catalog:http-routes/GET /v1/messages/{id}` using an id the bus had minted, and the route returned that row with its body and the instance it belongs to. An id that was never minted answered not-found rather than an empty row, so an operator can distinguish "no such message" from "a message with nothing in it". The operator CLI retrieved the same message by id correctly as well, so the capability is available from either surface.

## Unverified remainder

One minted id and one never-minted id were fetched. The way does not establish behaviour for an id belonging to a different instance or to a terminated one.
