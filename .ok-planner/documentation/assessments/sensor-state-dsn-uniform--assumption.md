---
assessment: sensor-state-dsn-uniform--assumption
subject: assumption:sensor-state-dsn-uniform
way: assumption
release: d977250c
outcome: held
warrant: experiment:assumption-sensor-state-dsn-uniform
---
# all four sensors take the same `_STATE_DSN` shape against the same database engine, so one Postgres can back all of them with one connection string per sensor.

As operator provisioning sensors, I would take it that all four sensors take the same `_STATE_DSN` shape against the same database engine, so one Postgres can back all of them with one connection string per sensor.

sibling-symmetry — `RIMSKY_SENSOR_{CRON,HTTP,OBJECT_STORE,WEBHOOK}_STATE_DSN` in one family

## What the audit ran and observed

Experiment `assumption-sensor-state-dsn-uniform` (five checks, none failing) gave
all four sensor images at this tree's tag the same postgres connection string in
their own `_STATE_DSN` variables, against one database on a private docker
network, and read each once its gRPC port accepted a connection. The prior
holds. One connection string shape (a `postgres://` URL, pgx in every sensor)
and one engine served all four; each bootstrapped its own state table in that
one database — `sensor_cron_state`, `sensor_http_state`,
`sensor_object_store_state`, `sensor_webhook_state` — and every table found
belongs to exactly one sensor, including the second table the object-store
sensor keeps (`sensor_object_store_seen_names`), which stays inside its own name
space. Pointed at a host that does not resolve, all four failed the same way:
each exited non-zero rather than running stateless, and each named its state
database in the message.

## Unverified remainder

None: the passing run demonstrates the prior as stated.
