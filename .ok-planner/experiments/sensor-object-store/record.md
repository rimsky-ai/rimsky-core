---
experiment: sensor-object-store
commit: d977250c
---

# Content dropped into a designated location becomes work in the graph

## What it ran against

A private docker network carrying a `rimsky-all-in-one` orchestrator and a
`rimsky-sensor-object-store` container whose filesystem backend is a host
directory bind-mounted read-only. The template declares one message type, one
node subscribed to it, and one publisher of kind `object-store` naming the
bucket, the prefix `in/`, and a one-second poll interval. The run deposits files
into the host directory. `run.py` builds and removes everything. Re-run unchanged at this tree.

## What was observed

The sensor registered its filesystem backend from one environment variable, and
the template's subscription mounted live on the instance. Depositing a file
under the designated prefix produced a publisher message naming the backend, the
bucket, the object name, its size, its content hash and its modification time,
and the subscribed node ran once. No operator message was posted at any point in
the run.

A second deposit produced its own message and a second node run. A file
deposited outside the designated prefix produced no message, while a fourth file
deposited under the prefix did — the later arrival is what shows the poller kept
listing the bucket. Across the whole run the graph saw three messages, one per
object under the prefix, with no object handed over twice, and the subscribed
node ran three times.
