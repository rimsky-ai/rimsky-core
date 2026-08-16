---
assumption: blob-backends-interchangeable
commit: PENDING
disposition: trap
synthesized: 2026-08-16T05:48:16Z
---

# the blob backends are interchangeable at runtime, so switching `persistence.blob.backend` keeps existing spilled attribute values readable.

As operator sizing storage, I would take it that the blob backends are interchangeable at runtime, so switching `persistence.blob.backend` keeps existing spilled attribute values readable.

## Source

sibling-symmetry — a single `persistence.blob.backend` key over multiple named backends

## What a run would observe

write a spilled attribute under one backend, switch the config, restart, and read the attribute back.

## Measured

Experiment `assumption-blob-backends-interchangeable` (six checks, none failing)
wrote a four-thousand-character attribute past a 64-byte spill threshold under
`persistence.blob.backend: filesystem`, then reopened the same state directory
under `inline`, under `memory`, and under `filesystem` again, reading the value
back each time through `GET /v1/nodes/{id}`. The prior does not hold. Under its
own backend the attribute reads back whole and its bytes sit under the backend's
root. Under `inline` the read fails with HTTP 500 — `row has value_handle
"fs:c1/29/…-2.bin" on backend "filesystem", but active blob backend is
"inline"` — and under `memory` it fails the same way. Each row records the
backend that holds its value and refuses to read across a switch; there is no
migration path in the switch itself. Configuring the original backend again
makes the value whole once more, so the data survives — what does not survive is
the deployment's ability to read it. An operator who changes the key to resize
storage takes every previously spilled value offline until the key is changed
back.
