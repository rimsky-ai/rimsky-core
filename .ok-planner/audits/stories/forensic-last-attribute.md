---
audit: forensic-last-attribute
artifact: story:forensic-last-attribute
determination: supported
compliance: compliant
commit: PENDING
audited: 2026-08-10T07:40:00Z
---

# An operator reads a node's most recent resolved attribute bag from a read surface

Supported. Against an all-in-one deployment driven through the control API, a
node that dispatched three times emitted the deltas `{count: 1}`, `{count: 2}`,
`{count: 3}`, and both read surfaces that expose a node — the node route and the
observability node route — answered with the resolved bag `{count: 3, max: 3}`:
the most recent one, and carrying the input value `max` that no delta ever
carried. No single event in the log holds that bag, so the same answer from the
event log alone would mean folding the deltas together and re-adding the
resolved inputs. A second node's latest bag came back the same way with its own
resolved values.
