---
issue: decisions-enumerate-routes-and-envs-in-body
kind: audit
category: muddy-boundary
artifacts:
  - decision:host-agent-proxy-tls
  - decision:enroll-token-is-api-key
  - decision:peer-auth-mtls
status: verified
opened: 2026-07-24T00:00:00Z
---

# Where does "naming the choice" end and "writing a spec" begin?

Three decision documents about rimsky's peer authentication — the mechanism by which a deployment's internal services come to trust each other, via mutual TLS or an API-key scheme — enumerate concrete implementation surface in their prose: HTTP route paths, environment-variable names, an encryption algorithm, a certificate lifetime, an identity-string format. The corpus's authoring rule pulls in both directions: a decision *may* name "the specific artifact picked" when the artifact's identity is the tradeoff (AES-256-GCM over an alternative is a real choice), but must *not* enumerate "implementation steps, schema details, or call sequences" the way a specification would. Route paths and env-var names sit squarely in the ambiguous middle, and no project ruling has ever drawn the line.

The stakes are drift, not information loss: every flagged detail is already named in code, so stripping it deletes only a duplicate — one that is a liability precisely because the project is pre-v1 and renames routes and env vars freely, meaning each rename silently falsifies a decision document until someone notices. The pattern is concentrated in these three files, with the same shape appearing in one more decision and one concept document — so whatever line gets drawn here has slightly wider blast radius than the three named files.

## Options

- **Draw the line at the tradeoff**: keep what carries a real choice (the algorithm, the lifetime, the identity-binds-to-key property); strip routes and env-var names as plumbing that lives in code. Sweep the two adjacent documents in the same pass.
- **Move the stripped detail to a spec document** — the project maintains no living spec surface, so this means either building one or accepting code as the home anyway.
- **Leave as-is** — read "artifact picked" broadly enough to cover the whole contract surface; the drift liability stands.

The ruling decides where the line falls, which specific elements in the three files survive, and whether the reading becomes a standing rule for the whole catalog.

## Ruling

> Recommended ruling (/recommend-rulings): Standing reading: a Choice
> may name the artifact whose identity carries the tradeoff
> (AES-256-GCM, the 24h TTL, SAN-binds-key-id); route paths and env-
> var names beyond that are spec enumeration — strip them from the
> three peer-auth files, landing nowhere but code. Sweep the same
> pattern in decision:secret-at-rest-posture and concept:peer-auth in
> the same pass.
>
> Rationale: This draws the exemption line where DECISION-DEFINITION
> drew it — the tradeoff-bearing artifact, not its plumbing — and
> pre-v1 renameability is exactly why plumbing shouldn't live in
> decision bodies.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
recommended batch at sign-off. Edit the text to redirect, empty
the section to discuss live, or delete this note to adopt the
ruling as your own. -->
