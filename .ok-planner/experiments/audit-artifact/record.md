---
experiment: audit-artifact
commit: PENDING
---

# Operator inspects a completed one-shot run's durable record

## What it ran against

The `rimsky` CLI's two one-shot modes, each driving a mixed roster (one leg
succeeds, one leg fails) against a third-party executor built from the protocols
module: `rimsky compose run` over the manifest in `manifest/`, and `rimsky run`
self-hosting an ad-hoc template. Each run's record is then read back by serving a
copy of its artifact state through a `rimsky-all-in-one` container from the
tree's own image tag, on a port picked free at start, and querying it with
`rimsky instance get|nodes|events`, `rimsky node get` and the ruled event and
observability routes.

## What was observed

The compose one-shot finished in the invocation that started it, exiting 1 for
the mixed roster and reporting `audit-artifact/ok: success` and
`audit-artifact/oops: failure`. It left `.rimsky/latest` pointing at a run
directory holding `state.db`, `blobs/` and the `rimsky.yml` the run used, and the
executor process it spawned was gone before anything was read.

Served back, the record held both instances, both terminal. Its event stream
replayed a `terminal/success` and the failure's own error class,
`terminal/error/third-party/refused`. `instance get` read the failing run's
instance, `instance nodes` its worker, `instance events` its terminal, `node get`
a node of the succeeding run, and the observability view carried the succeeding
leg's attribute writeback (`served_by: third-party-peer`). Two consecutive reads
returned the same event count, so reading did not change the record.

The ad-hoc one-shot behaved the same way: exit 1, its own artifact directory, its
instance terminal in the record, both legs' terminals replayed, and the success
leg's writeback readable.

Twenty-three checks, none failing.

RESULT: PASS
