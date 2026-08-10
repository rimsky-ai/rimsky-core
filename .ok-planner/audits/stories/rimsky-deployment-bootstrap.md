---
audit: rimsky-deployment-bootstrap
artifact: story:rimsky-deployment-bootstrap
determination: supported
compliance: noncompliant
commit: PENDING
audited: 2026-08-10T07:30:00Z
---

# The operator picks the topology, and the schema arrives once

Supported. All 4 launch shapes and all 3 illegal ones were driven against the
shipped image. With no command the entrypoint took all three roles in one
process and completed the migrations before serving; each of the 3 single-role
commands ran that role alone, and only the control-api one migrated. An unknown
role name, the migrate binary's name, and two role arguments each exited non-zero
without starting anything, naming what was wrong. Across a three-container split
sharing one database, exactly 1 of the 3 containers ran the migrations and the
other 2 logged that they were skipping them; the schema arrived, and a node
dispatched and settled across the split roles. The override moved ownership both
ways: set to 0 a no-command deployment skipped the migrations it would otherwise
have owned, set to 1 a scheduler-only container ran them, and set to any other
value the deployment refused to start, naming the variable and the value.

## Compliance

The body prescribes mechanism throughout: it names the launch grammar ("run the
bundled multi-role entrypoint with no command"), the storage layer ("database
migrations"), and the override's form ("an explicit environment-variable
override"). A story owes the need, not the switches that satisfy it. Compliant
text: "As an operator deploying rimsky to a stack, I can choose whether one
deployment unit runs all three roles or each runs one, and trust that the schema
is brought up to date exactly once per deployment whatever I choose — never
twice, never not at all — and that I can take that step over myself when I want
to run it separately, so that the deployment topology is whatever I choose and
the schema arrives at the right state deterministically."
