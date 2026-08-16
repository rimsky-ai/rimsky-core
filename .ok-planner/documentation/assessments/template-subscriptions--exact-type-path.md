---
assessment: template-subscriptions--exact-type-path
subject: story:template-subscriptions
way: exact-type-path
release: d977250c
outcome: held
warrant: experiment:template-subscriptions
---
# Firing a node on exactly one kind of upstream event

The audit registered one template carrying all five subscription forms the story implies, all admitted at registration, then ran one source node that emitted one terminal signal. The node subscribed on the exact type-path fired exactly once. The node subscribed on a different type-path did not fire at all, so the match is what fires the node rather than the arrival of any signal — an author targeting one kind of upstream event gets that node run and no other.

## Unverified remainder

One signal over one source node was exercised. The demonstration does not establish behaviour when several signals of the matched type arrive in quick succession.
