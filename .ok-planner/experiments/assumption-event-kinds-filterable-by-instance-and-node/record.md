---
experiment: assumption-event-kinds-filterable-by-instance-and-node
commit: PENDING
---

# Counting which event rows carry an instance and a node identifier

## What it ran against

A `rimsky-all-in-one` stack with no external peers. The run registers a
one-node template, drives it to settle, terminates the instance, then reads the
whole feed from `GET /v1/events` and groups the rows by kind, counting how many
of each carry `instance_id` and how many carry `node_id`. It then repeats one
read under a `node_id` filter.

## What was observed

Coverage is not uniform. `work_started`, `work_completed`,
`attributes_substituted` and `terminal/success` carried both identifiers on
every row. `instance_terminated` carried an instance id and no node id.
`auth.access_attempted` — nine rows in this short run — carried neither: no
instance id and no node id on any row.

Narrowing to one node succeeds and silently drops what it cannot place.
`GET /v1/events?kind=auth.access_attempted&node_id=<node>` returned HTTP 200
with zero rows, while the same kind unfiltered returned nine and the same
filter returned the node's own `work_started` rows. Narrowing to the instance
instead dropped the auth rows just the same.
