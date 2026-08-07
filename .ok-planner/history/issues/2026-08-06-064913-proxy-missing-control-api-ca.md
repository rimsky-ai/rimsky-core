---
issue: proxy-missing-control-api-ca
kind: audit
category: config-surface
artifacts:
  - concept:host-agent-proxy
  - concept:peer-auth
status: promoted
opened: 2026-08-06T06:49:13Z
sprint: 2026-08-06-ruled-intake-drain.md
---

# The host-agent proxy cannot trust a control API signed by a private CA

The host-agent proxy talks to the control API over HTTPS with no way to
say which certificate authority to trust. Its HTTP clients are built
with no custom transport (`cmd/rimsky-host-agent-proxy/main.go:44`,
`executor_handler.go:34`, `claim_producer_handler.go:35`) and its
config carries no CA field, so an `https://` control-API URL verifies
against the operating system's trust store only. A deployment whose
control API serves a certificate from an internal CA — common for
private infrastructure — cannot run the proxy against it at all, and
the proxy's control-API calls are not optional: agent-registration
verification and cache-miss instance lookups both ride them,
unconditionally, in every `peer_auth` mode.

Every bundled service already solves this with `RIMSKY_CONTROL_API_CA`
(`lib/protocols/enroll/trust.go:13`) — but only inside the
`peer_auth: mtls` enrollment path, a one-time hop. The proxy's need is
differently shaped: an always-on dependency that exists whether or not
the mutual-TLS trust domain is enabled. The design corpus is silent on
this call path: `concept:host-agent-proxy` and
`decision:host-agent-proxy-tls` cover the agent-to-proxy and
agent-to-child hops only, and `decision:peer-tls-enforcement`'s
per-peer `tls` key is scoped to executor, store, and publisher peer
entries.

## Options

- Add `RIMSKY_CONTROL_API_CA` to the proxy, same name and format as
  the bundled services, honored whenever the control-API URL is
  `https://`. Cost: a third CA-pinning idiom — always-on where the
  bundled services' is mtls-gated.
- Enroll the proxy into the mutual-TLS machinery and let its existing
  CA handling cover this hop. Cost: real feature work, and it helps
  only deployments running `peer_auth: mtls`.
- Widen the per-peer `tls` enforcement decision to cover this dial
  site. Cost: reinterprets a decision explicitly scoped to a named
  list of peer entries.

The ruling decides how the proxy learns to trust a private-CA control
API.

## Ruling

> Recommended ruling (/verify-issues): give the proxy the same
> `RIMSKY_CONTROL_API_CA` variable the bundled services read, honored
> unconditionally whenever the control-API URL is HTTPS.
>
> Rationale: it is the smallest surface that unblocks private-CA
> deployments in every `peer_auth` mode; the enrollment option helps
> only mutual-TLS operators and is really the sibling issue's
> question — if that ruling brings enrollment into the proxy, this
> variable becomes its trust anchor rather than a competing idiom.
> The flip case: if the sibling ruling rejects proxy enrollment and
> the owner declares non-mtls internal HTTPS unsupported, this
> becomes working-as-intended and closes with documentation instead.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
