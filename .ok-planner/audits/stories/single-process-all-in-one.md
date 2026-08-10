---
audit: single-process-all-in-one
artifact: story:single-process-all-in-one
determination: supported
compliance: noncompliant
commit: PENDING
audited: 2026-08-10T07:30:00Z
---

# One process for three roles, and the blob backend that proves it

Supported. The all-in-one container's process table held exactly 1 rimsky
process, the multi-role entrypoint, with no per-role child beside it, and all 3
roles were serving out of it: the control API answered, one supervisor was
registered, and a node dispatched and settled. The blob claim was measured on the
same deployment: configured with the memory backend and a 256-byte spill
threshold, it ran a node carrying an 8700-byte attribute payload and read the
whole payload back through the control API — the supervisor role's spilled bytes,
read by the control-api role. The dependence is real rather than incidental: the
same configuration in a single-role container was refused at startup, naming the
memory backend and the single-process mode it requires.

## Compliance

The body prescribes mechanism rather than need: it names the process topology,
the three role names, and a storage backend, and its "so that" clause restates
the capability ("the deployment is genuinely unified") instead of naming a user
benefit. Compliant text: "As an operator running the all-in-one deployment, I get
one deployment unit that behaves as a single whole rather than as separately
coordinating parts, so that work I hand it runs and its results read back without
my provisioning anything shared between the parts."
