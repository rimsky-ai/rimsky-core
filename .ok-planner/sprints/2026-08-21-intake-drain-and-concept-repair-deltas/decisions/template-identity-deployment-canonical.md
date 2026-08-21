---
decision: template-identity-deployment-canonical
---

# The deployment owns a template's canonical hash

## Choice

A template's identity is the content hash of its spec after the deployment's canonicalization, and that canonicalization reads deployment configuration — the kind-alias map — alongside the deployment-independent rules (see `concept:template`). A client does not compute the hash; it obtains it from the deployment. The validation route returns the hash it computes, and the compose planner resolves each manifest template through it before it plans.

## Rationale

The alias map is deployment configuration by design, so a client-side hash of the raw bytes is the hash of a spec the deployment never registers. Asking the deployment costs one call per template inside plan and makes compose accept every syntax the register route accepts. The validation route computes the hash to validate, so returning it is a response field, not new machinery.

## Alternatives

- Identity from the spec bytes alone, with the deployment-dependent canonicalizations moved out of the hashed bytes — rejected: it freezes alias expansion out of identity and re-keys every content-addressed template a deployment holds; it becomes the right call if templates are ever shared across deployments by hash.
- A compose manifest must declare templates in already-canonical form — rejected: the register route then accepts syntax compose forbids, a permanent seam in the one story that promises reconciliation.
