---
issue: no-way-to-export-deployment-ca-root
kind: human
category: config-surface
artifacts:
  - concept:peer-auth
  - concept:control-api
  - decision:peer-auth-mtls
  - story:service-enrollment
  - story:rimsky-deployment-bootstrap
status: promoted
sprint: 2026-08-08-ruled-intake-drain.md
opened: 2026-08-07T08:49:12Z
github: https://github.com/rimsky-ai/rimsky-core/issues/64
---

# Bootstrapping a mutual-TLS deployment requires a certificate rimsky will not give you

rimsky can run its internal service-to-service traffic under mutual TLS. In that
mode the platform stands up its own certificate authority, and every service —
executors, sensors, the host-agent proxy — enrolls with it once at startup to
get a client certificate.

Enrollment has a prerequisite. To make that first call over HTTPS, the service
must already hold the deployment CA's root certificate so it can verify who it
is talking to; without it the client refuses to dial, by design, because the
api-key crosses that hop as a bearer token
(`lib/protocols/peerauth/config.go::enrollHTTPClient`).

rimsky publishes no way to obtain that certificate. There is no route that
serves it and no CLI subcommand that prints it. The enrollment endpoint returns
it in its response — which is circular, since you need it to make that call. The
only other copy is a column in the platform's own database.

So the actual bootstrap procedure is: start the control plane in mutual-TLS
mode, connect to its database, and select a certificate out of a table by hand.
That is what operators are currently told to do, because there is nothing better
to point them at. It requires database access an operator may not have, it is
easy to get subtly wrong, and it has no affordance in the product — the one
step that gates every other service's startup happens outside the product
entirely.

The corpus treats the material itself as unremarkable: the decision governing
mutual-TLS peer auth notes that rimsky stores "only the api-key hash plus public
CA material," which puts the root certificate on the public side of the line. A
CA root is public by construction — it is what a server presents to every client
that connects. What no artifact specifies is a surface for it: no route shape, no
auth model, no CLI verb.

## Options

- **Serve it from the control API**, beside the enrollment endpoint. Reachable
  by exactly the things that need it, at exactly the moment they need it; costs
  a new public route.
- **Serve it, gated by the same permission enrollment already requires.**
  Same shape, no meaningful extra cost to callers since an enrolling service
  already holds that credential; buys little, because the certificate is public
  material.
- **A CLI subcommand that prints the certificate.** Good for the human operator
  writing a deployment manifest; does nothing for a service bootstrapping itself,
  and needs database or control-API access anyway.
- **Document the database column as the intended path** and add no surface.
  Free, and it leaves the product's most security-sensitive setup step as a
  manual database query.

The ruling decides whether rimsky ships a way to obtain its own CA root, and
through which surface.

## Ruling

> Recommended ruling (/verify-issues): serve the CA root from the control API,
> unauthenticated, alongside the enrollment endpoint. A CA root certificate is
> public by definition — every TLS server hands it to anyone who connects — so
> there is nothing to protect by gating it, and gating it only adds a step to
> the one procedure that must work before any service is running.
>
> Rationale: this is the bootstrap step for the platform's whole mutual-TLS
> story, and it currently lives outside the product as a hand-run database
> query, which is not a setup path a deployment should depend on. Putting it
> beside enrollment means the thing that needs it can fetch it over the same
> reachable surface it is about to enroll through. A CLI verb is a fine
> convenience on top and a poor substitute underneath, since a service starting
> itself has no shell. What would change this call: if knowing a deployment's CA
> root is considered a signal worth withholding from unauthenticated callers,
> gate it behind the permission enrollment already requires — every legitimate
> caller holds it, so the cost is near zero.
>
> Rule this with `issue:proxy-agent-hop-tls-asserted-but-plaintext` and
> `issue:plaintext-enrollment-hop-silently-accepted` — all three are the same
> mutual-TLS bootstrap story seen from different ends, and the posture chosen
> for one constrains the others.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to
accept — the next /plan-sprint carries it, naming the generated/recommended
batches at sign-off. Edit the text to redirect, empty the section to discuss
live, or delete this note to adopt the ruling as your own. -->
