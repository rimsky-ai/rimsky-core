---
audit: cron-robfig-v3
artifact: decision:cron-robfig-v3
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:29:02Z
---

# Cron expressions are parsed by robfig/cron/v3

Supported. `lib/services/go.mod` pins `github.com/robfig/cron/v3` at `v3.0.1`, and `sensor-cron/sensor.go` is the sole cron-parsing site in the bundled services: `cron.ParseStandard` validates the expression on `Subscribe` and `cron.Schedule.Next` computes each fire time in `Tick`/`fireOne`/`coalesceMissedWindows`. No hand-rolled cron grammar or alternative scheduling library exists anywhere in `lib/services`; the Go module import path's `/v3` segment is what enforces the major-line pin.
