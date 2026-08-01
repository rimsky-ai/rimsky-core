---
issue: decisions-enumerate-routes-and-envs-in-body
kind: audit
category: muddy-boundary
artifacts:
  - decision:host-agent-proxy-tls
  - decision:enroll-token-is-api-key
  - decision:peer-auth-mtls
status: promoted
sprint: 2026-08-01-guidance-realignment-drain.md
opened: 2026-07-24T00:00:00Z
---

# Where does "naming the choice" end and "writing a spec" begin?

Three decision documents about rimsky's peer authentication — how a deployment's internal services come to trust each other, via mutual TLS or an API-key scheme — enumerate concrete implementation surface in their prose: HTTP route paths (`route:POST /v1/enroll`, `route:GET /v1/auth/whoami`), environment-variable names, an encryption algorithm, a certificate lifetime, an identity-string format. Re-verified today, all three still do. The authoring rules pull in both directions and, notably, the self-containment rule's ban on enumerating implementation instances is textually scoped to *concepts* only — decisions have no equivalent clause, just the tension between "not specs" and "the Choice may name the specific artifact picked when its identity is the tradeoff." Route paths and env-var names sit squarely in that unlegislated middle, and no project ruling has ever drawn the line.

The stakes are drift, not information loss: every flagged detail is already named in code, so stripping deletes only a duplicate — one that is a liability precisely because the project is pre-v1 and renames routes and env vars freely, silently falsifying whichever decision quoted them. The corpus already contains one side's precedent: `concept:peer-auth` abstracts this same material to property level, and a sibling decision deliberately abstracts a status code to "a created-resource status code." The same pattern appears in one more decision (`decision:secret-at-rest-posture`) and would be swept by whatever line gets drawn here. This issue is the boundary-question twin of `issue:decisions-spec-altitude-mechanism-detail` — that one applies the "not specs" clause where it clearly bites; this one asks where the clause's edge sits.

## Options

- **Draw the line at the tradeoff**: a Choice keeps what carries a real decision (the algorithm, the 24-hour lifetime, the identity-binds-to-key property); route paths and env-var names beyond that are plumbing that lives in code. Strip the three files, sweep the two adjacent documents, and let the reading stand as the catalog-wide rule.
- **Read "artifact picked" broadly** — the whole contract surface counts as the artifact; the drift liability stands and the spec-altitude sweep loses its boundary.
- **Move stripped detail to a dedicated spec surface** — the project maintains none, so this option first builds one.

The ruling decides where the line falls and whether it becomes the standing catalog-wide reading.

## Ruling

> Recommended ruling (/verify-issues): adopt the tradeoff line as the
> standing reading — a Choice may name the artifact whose identity
> carries the tradeoff (the algorithm, the lifetime, the
> identity-binds-to-key property); route paths and env-var names are
> spec enumeration and strip, landing nowhere but code. Apply it to
> the three peer-auth files plus decision:secret-at-rest-posture in
> the same pass as the spec-altitude sweep.
>
> Rationale: this reads the decision rule the way the corpus already
> behaves at its best — the status-code abstraction precedent and
> concept:peer-auth's property-level treatment of this exact material
> — and pre-v1 renameability is the whole argument against quoting
> plumbing. The flip case: if a route or env name someday becomes a
> stability commitment the project makes to consumers, that name
> stops being plumbing and earns its place in a Choice.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
