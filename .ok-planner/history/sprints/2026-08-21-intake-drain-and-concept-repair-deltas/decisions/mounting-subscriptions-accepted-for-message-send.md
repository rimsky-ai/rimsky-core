---
decision: mounting-subscriptions-accepted-for-message-send
---

# Message send accepts a mounting publisher subscription

## Choice

The message-send endpoint's publisher capability check accepts a subscription row in either the active or the mounting state. It rejects a failed row, a stopped row, and a subscription identifier belonging to another instance (see `concept:publisher-subscription`, `concept:message`).

## Rationale

Rimsky creates a subscription row as mounting, and a reconciler flips it to active after the Subscribe handshake succeeds. A publisher can observe and send before that flip lands. Rejecting the send would drop a real observation over bookkeeping the publisher can neither see nor wait on. A mounting row already names an authorized publisher, its instance, and its message type, so accepting it grants nothing an active row would not. Failed and stopped rows carry no such authority and stay rejected.

## Alternatives

- Accept only active rows — rejected: a fast publisher's first message is dropped because the reconciler has not yet recorded the flip.
- Make the publisher wait until its row goes active — rejected: it turns a reconcile interval into lost observation time and makes every publisher poll rimsky-side bookkeeping.
