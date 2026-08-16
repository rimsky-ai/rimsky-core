---
experiment: assumption-sensor-state-dsn-uniform
commit: PENDING
---

# One postgres behind all four sensors

## What it ran against

One postgres container and all four sensor images from the tree's own image tag,
on a private docker network. Every sensor gets the same connection string in its
own `_STATE_DSN` variable, pointed at the one database. Each sensor is read once
its gRPC port accepts a connection, and the database is then inspected through
`psql` for the tables they created. A second pass gives all four the same
variable pointed at a host that does not resolve, and waits for each container
to exit.

## What was observed

Five checks, none failing.

All four sensors took the same postgres URL and reached their listener: one
connection string shape, one engine, four sensors. Each bootstrapped its own
state table in that one database — `sensor_cron_state`, `sensor_http_state`,
`sensor_object_store_state`, `sensor_webhook_state` — and every table found
belongs to exactly one sensor, so nothing collides. The object-store sensor
keeps a second table, `sensor_object_store_seen_names`, and it too stays inside
that sensor's own name space.

Pointed at a database that is not there, all four behaved the same way: each
exited non-zero rather than running stateless, and each named its state database
in the message.
