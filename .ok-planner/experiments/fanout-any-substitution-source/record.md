---
experiment: fanout-any-substitution-source
commit: PENDING
---

# A fan-out partition_request substituted from each standard source

## What it runs against

`run.py` boots a `rimsky-all-in-one` container at `RIMSKY_IMAGE_TAG` with the
bundled filesystem claim producer configured over a throwaway workspace holding
a plain folder and a queue folder behind a pick policy, and drives it through
the control API. Four templates declare the same fan-out node and differ only in
where its `partition_request` reads from: an upstream node's attribute, an
instance param, the claim's payload, and a typed message's body. Each node also
reads `{{child.partition_key}}` into its own attribute, so every work unit
reports which partition it ran as.

## What was observed

All four templates registered, and each run's fan-out dispatched exactly the
partitions its source named: `u-1 u-2 u-3` from the upstream node attribute,
`p-1 p-2` from the instance param, `job-a-a job-a-b` from the claim payload
(the folder name the producer put in the payload, interpolated into the keys),
and `m-1 m-2 m-3` from the typed message. No run recorded a resolution error.
In every case the number of work units that reported a partition key equalled
the number of partitions the source named, and the keys matched.

Eight checks, none failing.
