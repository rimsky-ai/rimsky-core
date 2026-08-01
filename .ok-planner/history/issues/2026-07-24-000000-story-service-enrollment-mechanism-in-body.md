---
issue: story-service-enrollment-mechanism-in-body
kind: audit
category: muddy-boundary
artifacts:
  - story:service-enrollment
status: promoted
sprint: 2026-08-01-guidance-realignment-drain.md
opened: 2026-07-24T00:00:00Z
---

# A user-promise document quotes the route, the TTL, and the certificate grammar

Rimsky's story documents record user expectations; implementation specifics belong in decisions. The service-enrollment story — how a standing service like a sensor or executor obtains credentials to talk to rimsky — quotes the exact HTTP enrollment route, the certificate's 24-hour lifetime, and the exact identity format embedded in the certificate (a SPIFFE-style URI carrying the calling API key's ID) in its statement. Re-verification adds a fourth instance the filing missed: the role sentence itself names a config value (`peer_auth: mtls`). Every one of those specifics already lives in existing artifacts (`decision:enroll-token-is-api-key`, `concept:peer-auth`), so this is pure duplication — the kind that silently falsifies the story when a pre-v1 rename moves the route or lifetime.

The wrinkle the filing worried about has since simplified. It feared losing the identity-format fact that the story's verification leaned on — but stories no longer carry verification sections at all (that model was retired; the periodic implementation audit owns verification), and the *property* survives without the *grammar*: "the certificate's identity binds to the calling key" says everything an auditor needs. The strip therefore reduces the story to its single statement at outcome altitude — obtain credentials, auto-renew, revocation stops future issuance — with nothing left homeless. This file's fate rides with the joint stories sweep (`issue:stories-delivery-surface-named-in-body`, `issue:stories-mechanism-prescription-tail`, `issue:stories-name-rimsky-yml-and-config-keys`), which covers the same violation class across the catalog.

## Options

- **Full strip-to-citation as part of the joint stories sweep** — the statement describes obtain / auto-renew / revoke-stops-issuance; route, TTL, SAN grammar, and the `peer_auth: mtls` mention drop; the decisions already carry them.
- **Partial strip** — keep the identity grammar, drop route and TTL; preserves the exact duplication liability the rule exists to remove.

The ruling decides how much strips.

## Ruling

> Generated ruling (/verify-issues): full strip-to-citation — the
> story's statement narrows to the user-observable outcomes (a
> standing service obtains credentials, they renew without operator
> action, revoking the key stops future issuance), and the route,
> lifetime, identity grammar, and config-value mention drop, all
> already carried by decision:enroll-token-is-api-key and
> concept:peer-auth. The story-form rule forces the direction;
> execute it inside the joint stories sweep.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
