---
experiment: work-completed-emitted
commit: d977250c
---

# Every dispatch that finished says so in the ledger

## What it ran against

One `rimsky-all-in-one` container from the tree's own image tag, plus `peer/` —
the permissive-peer-build experiment's third-party executor, extended with a
park-once outcome and rebuilt for Linux by the run — so the run can choose each
dispatch's disposition from the template. One instance carries six nodes: a
success, an error, an error-then-retry, a park that resumes and then succeeds, a
park that stays outstanding, and a built-in executor's dispatch. The ledger is
then read through `GET /v1/events`.

## What was observed

Twelve checks, none failing. Seven `work_started` events and five
`work_completed` events were written for six dispatches. Pairing by dispatch id,
every dispatch that reached a terminal carried a completion — five distinct
dispatch ids on each side of the join, with no completion naming a dispatch that
never started. Each completion named its terminal kind, and the kinds
distinguished success from failure (`complete` and `errored`). Parking is not
completion: the dispatch that parked and resumed carried two starts and one
completion, and the dispatch still parked at the end of the run carried a start
and no completion at all — and that one unpaired start was exactly the dispatch
the park roster still held, on the node the template told to park.

Durations came out of the two timestamps alone: a non-negative duration was
computed for all five completed dispatches, the resumed one landing at three
seconds and the rest at zero.
