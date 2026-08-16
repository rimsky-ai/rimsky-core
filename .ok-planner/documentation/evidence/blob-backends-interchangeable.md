---
trap: blob-backends-interchangeable
release: d977250c
---
# Evidence set — the blob backends are interchangeable at runtime, so switching `persistence.blob.backend` keeps existing spilled attribute values readable.

Source of the prior: sibling-symmetry — a single `persistence.blob.backend` key over multiple named backends

## What the audit ran and observed (assumption record)

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

## Experiment record (experiment:assumption-blob-backends-interchangeable)

# Reading a spilled value after the backend changes

## What it ran against

Four `rimsky-all-in-one` containers in sequence over one host state directory,
each with a different `persistence.blob.backend` in its mounted config and a
spill threshold of 64 bytes: `filesystem` (root inside the state directory),
then `inline`, then `memory`, then `filesystem` again. One template holds a
single node whose attribute carries four thousand characters, well past the
threshold. The value is read back each time through `GET /v1/nodes/{id}`.

## What was observed

Six checks, none failing.

Written under the `filesystem` backend, the attribute reads back whole — four
thousand characters — and the value is on disk under the backend's own root, two
files there, not in the database.

Opened with `inline` configured, the same node's read fails outright: HTTP 500
carrying `node_attributes.GetLatestByNode: row has value_handle
"fs:c1/29/…-2.bin" on backend "filesystem", but active blob backend is
"inline"`. The row records which backend holds its value and refuses to guess.
The `memory` backend fails the same way, naming `filesystem` and `memory`.

Configured back to `filesystem`, the value reads whole again — the bytes were
never lost, only unreachable while another backend was configured.

Runnables: `src:.ok-planner/experiments/assumption-blob-backends-interchangeable/` at the stamped commit.
