---
audit: sensor-http
artifact: story:sensor-http
determination: supported
commit: b767a27d
audited: 2026-08-02T09:29:02Z
---

# Bundled HTTP sensor polls a URL and sends on changed, successful, matching bodies

Supported. `lib/services/sensors/sensor-http`'s `Tick`/`pollOne` polls each watch's URL at its configured `poll_interval`, requires a 2xx status by default (or a configured status allowlist via `statusMatch`), applies an optional JSON-path/value filter (`jsonMatch`), and only posts a message when the SHA-256 hash of the body differs from the last-seen hash — an unchanged body is a no-op. The last-seen hash persists to Postgres when `RIMSKY_SENSOR_HTTP_STATE_DSN` is set and reloads on `AttachStateDB`, and `TestSubscribe_RestartReplay_PreloadsLastHash` / `TestStateDB_PersistsAcrossRestart` / `TestAttachStateDB_RestoresLastPollAtSoRestartDoesNotForceImmediateRepoll` together exercise that an unchanged body across a restart still does not re-send.
