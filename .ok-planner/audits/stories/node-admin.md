---
audit: node-admin
artifact: story:node-admin
determination: supported
compliance: compliant
commit: PENDING
audited: 2026-08-10T07:40:00Z
---

# A node's whole state on a running instance, and the settled failure cleared off it

Supported, measured on a stack whose one instance ends with one node settled
successful and one settled failed against the same declared check. The node read
returns the whole state in one document — identity, instance, node type,
executor, declared tags, cascade mode, creation time, the four run tallies, the
attributes the run left behind including the offending row, and the settled
signal — and the failed node's signal names the check that rejected it where the
healthy node's names success. Clearing is refused (409) on the node that never
failed and succeeds on the failed one; the same read afterwards reports no
settled signal while the id, the executor, the run tallies and the check's
findings are unchanged, and the clearing appears in the instance's event log as
one operator override. The operator CLI's node read renders a narrower
projection of the same document — identity, executor, run tallies and the
settled signal, without tags, cascade mode or attributes — so the whole state is
read through the route and the failure marker through either.
