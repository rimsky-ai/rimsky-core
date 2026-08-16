---
assessment: sensor-cron--restart-durability
subject: story:sensor-cron
way: restart-durability
release: d977250c
outcome: held
warrant: experiment:sensor-cron
---
# A schedule keeps its place when the sensor process restarts

The audit measured the firing position rather than assuming it. With `catalog:bundled-services/sensor-cron` holding its state in the database named by `catalog:env-vars/RIMSKY_SENSOR_CRON_STATE_DSN`, the run stopped the sensor before a window the subscription had already recorded, and started it again once that window had passed. The revived sensor sent a message for the recorded window rather than for the window that was next when it came back, and the message's arrival time falls after the restart, so the revived process sent it rather than anything salvaged from before the stop. That firing drove the subscribed node, so a schedule that spans a restart still produces work in the graph.

## Unverified remainder

The demonstration covers one stop-and-start across one recorded window. It does not establish what a longer outage spanning several missed windows produces, nor behaviour when two sensor processes share one state store.
