---
audit: empty-message-wakes-roots
artifact: story:empty-message-wakes-roots
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:07:07Z
checked: 3
unaccounted: 0
---

# One empty message waking every structural root

Supported: a send whose entire request body was empty — naming no type and
supplying no envelope fields — woke all three of the template's structural roots,
each dispatching exactly once. The wake is targeted rather than indiscriminate,
which is the part that could quietly be wrong: the node carrying a declared
upstream was not woken directly, while the node downstream of a root still ran by
cascade, so "every structural root" means the roots and not everything. That it
uses the same path as every other message was taken from the record rather than
assumed — the empty message is a row in the ledger operators use for typed sends,
carries the empty type, and opened a frame whose triggering message is that row.
Nine checks, none failing.
