---
issue: instantiation-static-config-gate-untested
kind: audit
category: test-coverage
artifacts:
  - story:mandatory-instantiation-gate
status: repaired
opened: 2026-08-02T09:58:07Z
---

# Does the instance-create-time static-config gate have direct test coverage?

`story:mandatory-instantiation-gate` promises instance create validates
statically-knowable attribute config against executor schema value
constraints. Re-verification confirmed the create-time gate
(`validateStaticConfigAgainstExecutorSchemas`/`composeStaticConfigBag` in
`lib/control/controlapi/instances_static_config_gate.go`) is wired into
`handleCreateInstance`, but the one test whose name suggested coverage
actually exercised the earlier, separate registration-time validator
(`node.ValidateTemplate`'s `validateCompositionAgainstExecutor`), never
reaching `POST /v1/instances`. Confirmed the one case unique to the
create-time gate: `validateAttributesSchema` returns immediately when a
node has no `attributes.schema` block (`len(n.Attributes.Schema) == 0`),
so a node relying solely on `defaults.attributes.by_executor` with a
schema-violating value is invisible to the registration-time validator
and only the create-time gate catches it.

Rule that determined the fix: the story is real and the create-time gate
is not dead code — it has a genuine, non-subsumed case — so this is a
pure test-coverage gap, outcome 2 (add the missing test), no commitment
change.

What changed: added a third subtest to
`test/scenarios/attributes/instantiation_static_config_gate_e2e_test.go`'s
`TestAcceptance_InstantiationStaticConfigGate` — a node with no
node-level `attributes.schema` and a schema-violating
`defaults.attributes.by_executor.constrained.count: -1` default. It
registers and deploys cleanly (registration-time validator skips the
node), then `POST /v1/instances` is rejected 400 by the create-time gate,
naming `count` in `validation_errors`.

Verified: `go test ./test/scenarios/attributes/... -run
TestAcceptance_InstantiationStaticConfigGate -count=1` passes (all three
subtests, 2.8s, real Postgres via testcontainers).
