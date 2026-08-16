---
audit: rimsky-deployment-bootstrap
artifact: story:rimsky-deployment-bootstrap
text: noncompliant
implementation: supported
commit: d977250c
audited: 2026-08-16T04:50:41Z
---

# Operator-chosen role topology, with migrations running exactly once per deployment

Supported: an operator gets both halves of the promise through the shipped image.
All seven launch forms the bundled entrypoint accepts were driven — the four
legal ones (no command, and each of the three role names) and three illegal ones
(an unknown role, the migrate binary, two roles at once) — and each behaved as
promised: the no-command launch served all three roles from one process, each
single-role launch ran that role alone, and every illegal launch exited non-zero
naming the three valid roles without starting anything. Migration ownership was
measured on a real three-container split against one shared database: exactly one
of the three ran the migrations, the other two reported skipping them, the schema
arrived, and a node dispatched and settled to success across the split roles. The
environment override moved ownership in both directions — off for a deployment
that would otherwise own it, on for a role that would otherwise skip — and a
value that is neither of the two legal ones failed startup naming the variable and
the value it was given. Eighteen checks across two runs, none failing.

## Compliance

- The body names the delivery surface — the bundled entrypoint, its no-command
  and single-role invocations, and an "environment-variable override" — which the
  story rules place in decisions; the compliant text names only the need, e.g. "I
  can choose whether the deployment runs as one unit or as separate roles, and
  trust the database schema arrives exactly once whichever I choose, with a way to
  put the schema step under my own control for a dedicated init step, so that the
  topology is mine and the schema state is deterministic."
