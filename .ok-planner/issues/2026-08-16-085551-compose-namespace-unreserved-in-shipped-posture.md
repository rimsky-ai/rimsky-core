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

The compose machinery (a manifest-driven verb family) owns a reserved name prefix for the tags, instance keys and template tags it creates, and a story promises the server refuses that prefix to any other client. The server's check grants the bypass to any caller that both sets a self-declared origin header and holds the compose-origin grant. In the shipped default posture (anonymous mode) the synthetic identity holds a wildcard grant, so any client that sets the header passes — the reservation is unenforceable there. Under authentication the check works, but the compose verbs build their HTTP client without ever attaching a credential, so they fail unauthorized on any deployment past its first key. No posture exists in which compose is usable and its namespace reserved. The ruling decides which half moves.

## Options

- Scope the promise to authenticated deployments and make the compose verbs send their key; cost: the reservation is honestly absent in anonymous mode.
- Downgrade the reservation to an advisory convention; cost: the story's core promise goes.
- Keep the promise unconditional and give compose an identity anonymous mode cannot forge (something other than a client-set header); cost: new machinery.

The ruling decides where the reservation holds.

## Ruling

> Recommended ruling (/verify-issues): Fix the compose verbs to carry the caller's key (the same flag and environment fallback every other verb honours), and scope the reservation promise to deployments past anonymous mode — stating that in the open bootstrap window nothing is reserved because nobody is anyone.
>
> Rationale: anonymous mode is by design a wildcard identity, so a grant-based check cannot distinguish clients there and inventing a second identity mechanism for one prefix is disproportionate; once compose authenticates, the check it already relies on works. Flip case: if the owner wants compose usable and reserved in a keyless dev deployment, the third option is the only one that delivers it.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
