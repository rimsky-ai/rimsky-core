---
audit: claim-handoff
artifact: story:claim-handoff
determination: supported
compliance: noncompliant
commit: PENDING
audited: 2026-08-10T07:45:00Z
---

# One claim opened once, held across a subgraph, settled all-or-nothing

Supported. A template declared an acquirer that opens a claim on a
parameter-resolved selector and two downstream nodes that co-hold it and read
the live claim by alias. On the all-success run all three nodes settled fresh,
the producer's log for that selector reads Open then Commit and nothing else —
one Open for the whole holding subgraph, one Commit, no Abandon — and the two
co-holders each received the acquirer's address, a named field of its payload
and its scope bytes, with the address byte-identical to the acquirer's, so no
node re-acquired. The claim handle ends committed and the control API reports
three holders on that one claim. On the run whose last holder fails, the same
selector's log reads Open then Abandon: the claim was still opened once, it was
abandoned, no Commit was sent, all three nodes settled failed, and the claim
handle ends abandoned.

## Compliance

The body prescribes mechanism throughout: it names the template directive that
expresses co-holdership, the substitution form that carries the claim into a
node's attribute schema, and the protocol verbs the runtime fires. Compliant
text: "As a template author building a multi-node atomic-staging workflow, I can
have one node take a claim and later nodes work against that same claim without
taking it again, reading the claimed location and its details wherever they need
them, and have the whole group's work either all take effect or none of it, so
that I compose stage-then-write-then-verify pipelines with all-or-nothing
outcomes."
