---
experiment: claim-handoff
commit: PENDING
---

# One claim opened once and held across a subgraph

## What it ran against

A `rimsky-all-in-one` stack from this tree's image pointed at a claim producer
written for this experiment, which keeps a log of every verb it received and
serves that log over HTTP. The template declares an acquirer node that opens a
claim on a selector resolved from an instance parameter, and two downstream
nodes — a writer and a verifier — that co-hold the same claim through the
`holds` directive and read the live claim's address, a payload field and the
scope bytes by alias into their own attribute schemas. Both downstream nodes
run on the bundled http-node executor, which posts its resolved attributes to
a recorder on the host, so the values rimsky substituted are readable from
outside the stack. A second template is identical except that the verifier's
endpoint answers 500.

## What was observed

On the all-success run the acquirer, the writer and the verifier each settled
fresh. The producer's log for that selector reads `Open Commit` and nothing
else: one Open for the whole holding subgraph, one Commit, no Abandon. The
writer and the verifier each received the acquirer's address
(`stage-store://stage/good`), the payload field `eu-west-1`, and the scope
bytes `{"selector":"/stage/good"}`; the co-holders' address is byte-identical
to the acquirer's, so no node re-acquired. The claim handle ends `committed`
and the control API's holders route reports three holders on that one claim.

On the run whose verifier answers 500, the producer's log for that selector
reads `Open Abandon`: the claim was still opened exactly once, it was
abandoned, and no Commit was sent. The verifier, the writer and the acquirer
all settled failed with `terminal/error/abandoned`, and the claim handle ends
`abandoned`.
