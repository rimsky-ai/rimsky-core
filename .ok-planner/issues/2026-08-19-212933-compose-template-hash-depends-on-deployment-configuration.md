---
issue: compose-template-hash-depends-on-deployment-configuration
kind: audit
category: conflicting
artifacts:
  - concept:template
  - story:compose-lifecycle
status: verified
opened: 2026-08-19T21:29:33Z
---

# Does a template's identity depend on the deployment that registers it

Compose cannot apply a manifest that names a node by kind alias. `story:compose-lifecycle` promises that an operator applies a manifest to a running rimsky and rimsky reconciles state to match; a manifest written in syntax the register route accepts does not reconcile.

The mechanism: the control API applies four canonicalizations before it hashes a registered spec — frame-resolution defaults, kind sugar, send-message sugar, and the aggregation-policy default — and the last three read the deployment's kind-alias map. Compose's client-side resolver applies only the first, so its plan carries a hash the deployment never assigns. The apply step compares the two hashes and refuses, naming both and the manifest path; before that refusal landed, the same manifest failed with an opaque foreign-key error. The refusal names the problem and removes the capability.

The corpus is in tension with the code independent of that refusal. `concept:template` says the template id is a content-hash over the canonicalized spec bytes, names key order and whitespace as what canonicalization absorbs, and pins the canonicalization library as a standing commitment; nothing in it says identity may depend on the deployment. Two facts narrow the options' costs: the aggregation-policy default reads no deployment configuration, so the client resolver could apply it today (a plain omission, whichever way the ruling goes); and the validation route already computes the deployment-correct hash internally and discards it, so returning it is a response-field addition, not new machinery.

## Options

- A template's identity is the spec bytes alone: move the deployment-dependent canonicalizations out of the hashed bytes, so every client computes the hash the deployment assigns. Cost: the register route changes what it hashes, re-keying every content-addressed template a dev deployment holds.
- The deployment owns the canonical hash: the validation route returns the hash it already computes, and compose's planner resolves each manifest template through the deployment. Cost: a network call per template inside plan, and `concept:template`'s bytes-alone language must widen to name the deployment's alias map as an input.
- A compose manifest declares templates in already-canonical form: the refusal is the settled behavior, and the manifest author writes the executor name rather than a kind alias. Cost: the register route accepts syntax compose forbids, and the story's reconcile promise carries a documented carve-out.

The ruling decides what a template's identity is a function of.

## Ruling

> Recommended ruling (/verify-issues): The deployment owns the canonical hash. Have the validation route return the hash it already computes, have compose's planner resolve each manifest template through it, and widen `concept:template`'s identity clause to name the deployment's alias map as a canonicalization input. Fix the client resolver's missing aggregation-policy default either way.
>
> Rationale: the alias map is deployment configuration by design, so a client cannot compute the hash without asking — the first option removes the alias feature's value by freezing its expansion out of identity, and the third option makes compose reject syntax the platform's own register route accepts, a permanent seam in the one story that promises reconciliation. The latent hash in the validation route makes this the cheapest option as well as the most coherent. Flip case: if templates are ever shared across deployments by hash (a registry, a marketplace), deployment-dependent identity breaks that sharing, and the first option — identity from bytes alone — becomes the right call despite the re-keying.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
