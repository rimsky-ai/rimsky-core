---
experiment: assumption-metrics-on-bundled-services
commit: d977250c
---

# Where a prometheus scrape finds something

## What it ran against

One `rimsky-all-in-one` container from the tree's own image tag with the metrics
listener opened (`RIMSKY_METRICS_HOST=0.0.0.0`, `RIMSKY_METRICS_PORT=9464`, which
in the unified process gives the three roles 9464, 9465 and 9466), and all eleven
bundled service images, each given the same two variables an operator would
carry over. Every port each service documents is published to a free host port
and scraped, along with the metrics port the variables named. The two services
that need backing state get it: a filesystem producer with a mounted policy
config, and a postgres producer and the openlineage subscriber against a
postgres container. Each service is read once its first port accepts a
connection; the subscriber, which opens no port, once it has created its cursor
table.

## What was observed

Eight checks, none failing.

The three core roles each serve prometheus text at `/metrics` on their own port
— `# HELP rimsky_frame_duration_seconds …` came back from all three — and the
control API's own port answers 404 there, so the metrics listener is a separate
port rather than a route on the API.

No bundled service serves metrics anywhere. Across every published port of the
ten services with listeners, `/metrics` returned prometheus text nowhere; the
`9464` the variables named was never opened by any of them, every probe of it
refused; the openlineage subscriber opens no port at all; and no service image
declares a metrics port in its `EXPOSE` set.
