---
experiment: lineage-exploration
commit: PENDING
---

# Walking run lineage both ways, by claim handle, by source and by producer

## What it ran against

A `rimsky-all-in-one` container from this tree's own image tag, configured with
the bundled filesystem claim producer over a bind-mounted workspace. The
template runs two nodes: a producing node that holds a claim and fans out over
two partitions, and a consuming node that substitutes an attribute from it. One
workflow run therefore leaves one claim split into two sub-claims and two runs
joined by a substitution. Every read goes through the control API's lineage
routes. `run.py` builds and removes the container.

## What was observed

Querying by the attribute the consuming node substituted returned that node's
lineage record, whose substitution references name the upstream producing run.
Reading that run id returned the same record. Walking the consuming run backward
reached the producing run, and walking the producing run forward reached the
consuming run. Querying by the producing run as a source returned the consuming
run's record, and a depth given on the walk was honoured in the answer.

Querying by the named claim producer returned its three committed claim records.
Querying by a claim producer that committed nothing returned none. One of the
three records names two sub-claims; reading that claim handle returned its
record with the producer's name and its outcome, walking it forward reached both
sub-claims, and walking a sub-claim backward reached the claim it was split
from. A run id with no lineage answered 404.
