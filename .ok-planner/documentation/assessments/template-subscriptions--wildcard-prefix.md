---
assessment: template-subscriptions--wildcard-prefix
subject: story:template-subscriptions
way: wildcard-prefix
release: d977250c
outcome: held
warrant: experiment:template-subscriptions
---
# Firing a node on a family of upstream events

Alongside the exact form, the audit ran a node subscribed to a family of event types by prefix. It fired exactly once on the same arriving signal, so an author who wants a whole family rather than one member declares it in the same place and gets one firing per matching event, not one per family member.

## Unverified remainder

One family with one matching member was exercised. The demonstration does not establish how many times a family subscription fires when several members of that family arrive together.
