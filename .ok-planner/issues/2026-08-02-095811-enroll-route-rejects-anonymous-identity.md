---
issue: enroll-route-rejects-anonymous-identity
kind: audit
category: decision-drift
artifacts:
  - story:anonymous-mode-bootstrap
status: promoted
opened: 2026-08-02T09:58:11Z
sprint: 2026-08-03-audit-gap-drain.md
---

# Service enrollment rejects the anonymous identity even while anonymous mode is open

Anonymous mode (the fresh-deployment state where zero API keys exist and every request is admitted under a synthetic wildcard identity) promises that "every action succeeds" until the first key is minted (`story:anonymous-mode-bootstrap`). One action is a hard exception: service enrollment — the endpoint where a service requests a short-lived mTLS certificate — returns 403 to any identity without a real key id (`code:lib/control/controlapi/enroll.go::handleEnroll`), and the anonymous identity never has one. The rejection is not a permission denial: the anonymous wildcard grant passes the permission gate first; the 403 is a separate structural check.

The gap is reachable, not theoretical: the enroll route mounts whenever mutual-TLS peer auth is configured, independent of the key ledger, so a fresh deployment with `cfg:peer_auth: mtls` and no keys minted is simultaneously anonymous-open and enrollment-closed. The rest of the corpus is in tension with the story rather than silent — the control-API concept commits enrollment's certificate to binding "the calling key's id" into its SAN (`concept:control-api`), which the anonymous identity structurally cannot satisfy: it is ephemeral and computed, with no ledger row to bind. This is the sole such exception among the control API's action-gated handlers, verified by enumerating all of them.

The ruling decides whether the story's promise narrows or enrollment learns to admit an anonymous principal.

## Options

- Scope the story: "every action" means operator control-plane actions, with machine-to-machine enrollment explicitly carved out as a peer-auth concern. Cost: narrows a security-adjacent promise after the fact.
- Build an anonymous enrollment principal so enrollment succeeds under anonymous mode. Cost: real security design — a certificate is meant to attest a durable service identity, and there is no key row for its SAN to bind, so the artifact would attest an identity that doesn't exist.

## Ruling

> Recommended ruling (/verify-issues): scope the story — amend it to promise every *operator control-plane* action succeeds under anonymous mode, naming machine enrollment as the deliberate exception, and leave the enroll handler as it is.
>
> Rationale: the corpus already decided this internally — the control-API concept binds enrollment certificates to a real key id, and an anonymous certificate would be an attestation of nothing, which is worse than a clean 403 during bootstrap. The second option trades a prose narrowing for a genuine security-model invention nobody has asked for. Flip case: if the intended onboarding flow for an mTLS deployment is "boot with zero keys, enroll services, then mint keys" — i.e. enrollment genuinely must work before the first key exists — then the synthetic-principal design becomes necessary and should be its own sketch.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
