---
decision: destination-allowlists-default-closed
---

# Destination allowlists default closed and take private ranges by opt-in

## Choice

A bundled service's destination allowlist defaults closed. When the operator env var carrying it is unset, the service blocks every loopback, private, unique-local, link-local, multicast and unspecified address, and admits only the http and https schemes. A destination allowlist covers the addresses a service dials when the destination reaches it from a caller, through node attributes or a subscription's config. Unset and set-but-empty are the same closed boundary. An operator opens a range by naming it in that var as a CIDR or a bare address. A malformed entry fails the service's boot. The service checks the resolved address at connect time, so it blocks a name that resolves into a blocked range and a redirect into one alike.

## Rationale

A service that dials a caller-supplied destination is a request-forgery surface. The service reaches the deployment's own network — the cloud metadata endpoint, internal admin ports, the stack's sibling containers — and the caller does not. The closed default makes an operator name the internal range they mean to reach. A caller then cannot steer a deployment that never configures egress into its own network. This polarity matches the fail-loud inbound boundary of `decision:webhook-auth-required`. It is the opposite of `decision:allowlist-defaults-open`, because a destination allowlist bounds where a service sends traffic rather than what a template may declare. A destination the operator sets rather than the caller sits outside this boundary (see `concept:service-auth`).

## Alternatives

- Default open, leaving operators to name the ranges to block — rejected: every deployment that ships without egress config reaches its own metadata endpoint from any caller-supplied destination.
- Match the allowlist against the requested hostname instead of the resolved address — rejected: a name that resolves into a blocked range, and a redirect into one, both pass a hostname check.
