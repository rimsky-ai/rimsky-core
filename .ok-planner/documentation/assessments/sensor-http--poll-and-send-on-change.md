---
assessment: sensor-http--poll-and-send-on-change
subject: story:sensor-http
way: poll-and-send-on-change
release: d977250c
outcome: held
warrant: experiment:sensor-http
---
# Polling an external location and sending a message when its content changes

The audit drove `catalog:bundled-services/sensor-http` against a document the run rewrites from outside the deployment, with the watch declared as a publisher entry on the template. All four declared subscriptions mounted live on the instance. The unfiltered watch sent a message carrying the status the upstream returned, the location polled, the decoded body and a hash of that body, and the subscribed node ran on it. Rewriting the document produced a second message carrying the new body and a different hash, so the change is what sends. A watch on a location that never answers with success sent nothing across the whole run, so an unsuccessful poll is not a message. The operator wrote no publisher of their own for any of this.

## Unverified remainder

One poll interval and one document were exercised. The demonstration does not establish behaviour when the upstream answers success with an unparseable body, nor how a watch behaves across a long upstream outage.
