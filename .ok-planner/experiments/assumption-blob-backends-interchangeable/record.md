---
experiment: assumption-blob-backends-interchangeable
commit: PENDING
---

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
