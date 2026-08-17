---
issue: compose-namespace-unreserved-in-shipped-posture
kind: audit
category: test
artifacts:
  - story:compose-namespace-guard
  - concept:anonymous-mode
  - concept:permission
status: verified
opened: 2026-08-16T08:55:51Z
---

# The compose namespace is reserved only where the compose verbs cannot run

No deployment posture both runs compose and reserves its namespace. The compose machinery is a manifest-driven verb family. It owns a reserved name prefix for the tags, instance keys and template tags it creates. A story promises the server refuses that prefix to any other client. The server's check grants the bypass to any caller that sets a self-declared origin header and holds the compose-origin grant. In the shipped default posture, anonymous mode, the synthetic identity holds a wildcard grant, so any client that sets the header passes and the server cannot enforce the reservation. Under authentication the check works, but the compose verbs build their HTTP client without attaching a credential, so they fail unauthorized on any deployment past its first key. The ruling decides which half moves.

## Options

- Scope the promise to authenticated deployments and make the compose verbs send their key; cost: anonymous mode reserves nothing.
- Downgrade the reservation to an advisory convention; cost: the story loses its core promise.
- Keep the promise unconditional and give compose an identity anonymous mode cannot forge, rather than a client-set header; cost: new machinery.

The ruling decides where the reservation holds.

## Ruling

> Recommended ruling (/verify-issues): Make the compose verbs carry the caller's key, using the same flag and environment fallback every other verb honours. Scope the reservation promise to deployments past anonymous mode, and state that anonymous mode reserves nothing because every caller holds the same identity.
>
> Rationale: anonymous mode gives every caller a wildcard identity by design, so a grant-based check cannot tell clients apart there, and a second identity mechanism for one prefix is disproportionate. Once compose authenticates, the check it relies on works. Flip case: if the owner wants compose usable and reserved in a keyless dev deployment, only the third option delivers that.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
