---
audit: sensor-cron
artifact: story:sensor-cron
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:29:02Z
---

# Bundled cron sensor fires at declared cron expressions and survives restarts

Supported. `lib/services/sensors/sensor-cron` parses `robfig/cron/v3` expressions on `Subscribe`, ticks every second computing due watches from `NextFireAt`, and posts a message per firing (coalescing missed windows on catch-up) with no external scheduler process required. Firing position (`next_fire_at`, `last_fire_at`) persists to Postgres when `RIMSKY_SENSOR_CRON_STATE_DSN` is set, and `AttachStateDB` reloads all watches from that table on startup; `TestSensorCronStateDSN_SurvivesRestartAndFiresOnScheduledWindow` exercises exactly this end to end — subscribe, persist, tear down the service, reopen the state DB, and confirm the watch and its next-fire time survive into the new process.
