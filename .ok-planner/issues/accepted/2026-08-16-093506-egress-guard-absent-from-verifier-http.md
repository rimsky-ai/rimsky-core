---
issue: egress-guard-absent-from-verifier-http
kind: audit
category: conflicting
artifacts:
  - concept:peer-auth
  - decision:allowlist-defaults-open
status: verified
opened: 2026-08-16T09:35:06Z
---

# The verifier-http executor dials caller-supplied URLs without the SSRF guard

Two bundled services that dial caller-supplied URLs — the http-node executor and the sensor-http sensor — pass every destination through a shared egress guard that blocks loopback, private, link-local and cloud-metadata addresses by default, with an operator allowlist to opt private ranges back in; the peer-auth concept describes the guard as a property of the bundled images. The verifier-http executor takes its URL from node attributes — the same trust class as http-node's — and dials with a bare client: a template author can point it at loopback, a private network, or the metadata endpoint with no configuration. The OpenLineage subscriber is out of scope: its destination is an operator-set backend, not template text. The ruling wires the guard.

## Options

- Wire the shared guard into verifier-http with its own opt-in allowlist variable in the established naming, and restate the concept's claim as a rule about destination trust class (every caller-supplied destination passes the guard); cost: none beyond the change.
- Narrow the concept to exclude verifier-http; cost: documents around a live SSRF hole.

The ruling closes the hole the sibling executor already closes.

## Ruling

> Generated ruling (/verify-issues): Route the verifier-http executor's outbound request through the shared egress guard, default-closed with its own allowlist variable named like its siblings, and restate the concept so the guard is a property of every bundled dialer of caller-supplied destinations. Forced by the concept's posture and the project's fix-every-bug rule; the gap is an omission in one of three sibling dialers. Verified against the tree as it stands; nothing was applied.

<!-- Owner: this is a generated ruling, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
