---
assessment: sensor-cron--scheduled-firing
subject: story:sensor-cron
way: scheduled-firing
release: d977250c
outcome: held
warrant: experiment:sensor-cron
---
# A declared schedule fires work into the workflow with no scheduler to run

The audit drove a deployment carrying `catalog:bundled-services/sensor-cron` beside an orchestrator, on a template declaring one message type, one node subscribed to that type, and one publisher of kind `catalog:publisher-kinds/cron` carrying the operator's cron expression. Creating the instance through `catalog:http-routes/POST /v1/instances` mounted one live subscription for the declared message type. No operator message was ever posted to that instance — its senders list holds the publisher and nothing else — and a message nonetheless arrived carrying the expression the operator declared, a firing time on a whole minute, and no missed windows. The subscribed node ran once on that message. Nothing outside the deployment scheduled the firing: the sensor is the only thing that fires, and the operator declared the schedule in the template rather than in a scheduler.

## Unverified remainder

The schedule was declared through `catalog:http-routes/POST /v1/templates`, because a publisher's config is a raw field the operator CLI's YAML template path (`catalog:cli-verbs/rimsky template register`) cannot express — declaring this publisher from a YAML template file is not established. One expression and one firing window were exercised; behaviour across many concurrent schedules is not.
