---
experiment: claim-producer-observability
commit: PENDING
---

# Producer-side claim state read straight off the producer

## What it ran against

The bundled filesystem claim producer from this tree's image, running as its
own container over a bind-mounted host root with one pick policy configured and
two job folders seeded in it. A dashboard-shaped gRPC client written for this
experiment drives the producer's observability protocol from the host. A
`rimsky-all-in-one` stack from the same tree is pointed at the same producer,
with the producer's observability endpoint declared in rimsky.yml, so the
control API's claim-producer view can be read too.

## What was observed

The producer's capabilities response declares claim detail, claim streaming and
claim listing, plus two admin views by name and title, and the parameterised
view declares its one required parameter.

With four claims open — one on the pick policy and three on paths — the
inventory paginated: a request for two returned two and a cursor, the next page
repeated none of them, and walking the cursor reached all three path claims.
One claim's detail came back as OPEN with its opened-at timestamp, its scope,
and its event history.

A stream opened on that claim replayed its state, then delivered the commit as
a live event while the stream was open, and the claim then read as COMMITTED.

Both declared admin views rendered: the pick-policies view returned a column
schema, a render hint of `table`, and a row naming the configured policy
`@queue`; the parameterised policy-items view listed the seeded job folders. An
undeclared view name was refused rather than answered.

The control API's entry for the producer reports it reachable and carries the
same three capability flags and both admin-view declarations, so a dashboard
discovers what to render from the control API and reads the data from the
producer.
