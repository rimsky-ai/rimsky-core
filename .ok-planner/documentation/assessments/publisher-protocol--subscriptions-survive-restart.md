---
assessment: publisher-protocol--subscriptions-survive-restart
subject: story:publisher-protocol
way: subscriptions-survive-restart
release: d977250c
outcome: held
warrant: experiment:publisher-protocol
---
# Subscriptions survive a deployment restart without being re-issued

This is the half of the story a publisher author cannot work around, and it holds. Restarting the deployment re-issued nothing: the restarted stack asked the publisher what it already held, after which the subscribe call was still at one, the same subscription id was still held by the publisher, and no unsubscribe had been called. The publisher's next message landed the same way as before, running the subscribing node a second time. A publisher therefore does not need to reconcile, replay, or defend against duplicate subscriptions across a restart.

## Unverified remainder

One restart of one deployment holding one subscription was driven. The way does not establish reconciliation when the publisher itself restarts, or when the two restart together.
